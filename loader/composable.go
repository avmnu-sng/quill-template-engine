package loader

import (
	stderrors "errors"
	"io/fs"
	"path"
	"strings"

	"github.com/avmnu-sng/quill-template-engine/core/source"
	"github.com/avmnu-sng/quill-template-engine/errors"
)

// ChainLoader consults a sequence of loaders in order and serves the first hit.
// It layers a base set of templates under one or more overrides: an earlier
// loader shadows a later one for the same name, so a host ships defaults in the
// last loader and lets earlier loaders replace individual templates. A name
// missing from every loader is reported as the canonical not-found error.
type ChainLoader struct {
	loaders []Loader
}

// NewChainLoader builds a ChainLoader over the given loaders, tried left to
// right. The slice is copied so later mutation of the caller's slice does not
// affect the chain.
func NewChainLoader(loaders ...Loader) *ChainLoader {
	cp := make([]Loader, len(loaders))
	copy(cp, loaders)
	return &ChainLoader{loaders: cp}
}

// Get returns the source from the first loader that has the name. A not-found
// from one loader falls through to the next; any other error stops the chain
// and propagates, so a genuine I/O failure is never masked by a later miss.
func (l *ChainLoader) Get(name string) (*source.Source, error) {
	for _, sub := range l.loaders {
		src, err := sub.Get(name)
		if err == nil {
			return src, nil
		}
		if !IsNotFound(err) {
			return nil, err
		}
	}
	return nil, notFound(name)
}

// Exists reports whether any loader in the chain has the name.
func (l *ChainLoader) Exists(name string) bool {
	for _, sub := range l.loaders {
		if sub.Exists(name) {
			return true
		}
	}
	return false
}

// prefixRoute pairs a name prefix with the loader that serves names under it.
type prefixRoute struct {
	prefix string
	loader Loader
}

// PrefixLoader routes a name by its leading prefix to a sub-loader, stripping
// the prefix (and the delimiter after it) before delegating. It composes
// several independently rooted loaders into one namespace: "lang/header"
// reaches the loader registered for "lang" as plain "header". Routes are tested
// longest-prefix first, so a more specific prefix wins over a shorter one that
// is also a match.
type PrefixLoader struct {
	delimiter string
	routes    []prefixRoute
}

// NewPrefixLoader builds a PrefixLoader from a prefix->loader map, using "/" as
// the delimiter between the prefix and the remaining name. Use
// NewPrefixLoaderDelim to choose a different delimiter.
func NewPrefixLoader(routes map[string]Loader) *PrefixLoader {
	return NewPrefixLoaderDelim("/", routes)
}

// NewPrefixLoaderDelim builds a PrefixLoader with an explicit delimiter. A route
// key already ending in the delimiter is used verbatim; otherwise the delimiter
// is appended, so both "lang" and "lang/" register the same route.
func NewPrefixLoaderDelim(delimiter string, routes map[string]Loader) *PrefixLoader {
	l := &PrefixLoader{delimiter: delimiter, routes: make([]prefixRoute, 0, len(routes))}
	for prefix, sub := range routes {
		full := prefix
		if !strings.HasSuffix(full, delimiter) {
			full += delimiter
		}
		l.routes = append(l.routes, prefixRoute{prefix: full, loader: sub})
	}
	// Longest prefix first so a specific route shadows a shorter overlapping one.
	for i := 1; i < len(l.routes); i++ {
		for j := i; j > 0 && len(l.routes[j].prefix) > len(l.routes[j-1].prefix); j-- {
			l.routes[j], l.routes[j-1] = l.routes[j-1], l.routes[j]
		}
	}
	return l
}

// route finds the sub-loader for name and returns it with the prefix stripped.
func (l *PrefixLoader) route(name string) (Loader, string, bool) {
	for _, r := range l.routes {
		if strings.HasPrefix(name, r.prefix) {
			return r.loader, name[len(r.prefix):], true
		}
	}
	return nil, "", false
}

// Get routes name to its sub-loader with the prefix stripped, or returns a
// not-found error when no route matches.
func (l *PrefixLoader) Get(name string) (*source.Source, error) {
	sub, rest, ok := l.route(name)
	if !ok {
		return nil, notFound(name)
	}
	src, err := sub.Get(rest)
	if err != nil {
		if IsNotFound(err) {
			return nil, notFound(name)
		}
		return nil, err
	}
	// Keep the fully-qualified name on the returned source so diagnostics point
	// at the name the engine asked for, not the stripped delegate name.
	return source.New(name, src.Code()), nil
}

// Exists reports whether the routed sub-loader has the prefix-stripped name.
func (l *PrefixLoader) Exists(name string) bool {
	sub, rest, ok := l.route(name)
	if !ok {
		return false
	}
	return sub.Exists(rest)
}

// FSLoader serves templates from an fs.FS, such as an embed.FS baked into the
// binary. An optional root scopes lookups to a sub-tree of the filesystem: a
// name is joined under the root before it is opened. Names are cleaned to a
// slash-separated, root-relative form so a "../" segment cannot escape the
// configured root.
type FSLoader struct {
	fsys fs.FS
	root string
}

// NewFSLoader builds a loader over fsys. An optional root, when given, scopes
// every lookup to that sub-directory of fsys.
func NewFSLoader(fsys fs.FS, root ...string) *FSLoader {
	r := ""
	if len(root) > 0 {
		r = strings.Trim(path.Clean("/"+root[0]), "/")
	}
	return &FSLoader{fsys: fsys, root: r}
}

// resolve turns a template name into an fs.FS path under the root, rejecting a
// name that escapes the root. fs.FS paths are always slash-separated and never
// start with "/", so the cleaned name is joined to the root directly.
func (l *FSLoader) resolve(name string) (string, bool) {
	clean := strings.TrimPrefix(path.Clean("/"+name), "/")
	if clean == "" {
		return "", false
	}
	if l.root == "" {
		return clean, true
	}
	return l.root + "/" + clean, true
}

// Get reads the named file from the filesystem, or returns a not-found error
// when it is absent.
func (l *FSLoader) Get(name string) (*source.Source, error) {
	p, ok := l.resolve(name)
	if !ok {
		return nil, notFound(name)
	}
	b, err := fs.ReadFile(l.fsys, p)
	if err != nil {
		if isFSNotExist(err) {
			return nil, notFound(name)
		}
		return nil, errors.Wrap(errors.KindRuntime, err, "cannot read template %q", name)
	}
	return source.New(name, string(b)), nil
}

// Exists reports whether the named file is present and is not a directory.
func (l *FSLoader) Exists(name string) bool {
	p, ok := l.resolve(name)
	if !ok {
		return false
	}
	info, err := fs.Stat(l.fsys, p)
	return err == nil && !info.IsDir()
}

// isFSNotExist reports whether err is an fs "does not exist" error, including
// the fs.ErrInvalid an fs.FS returns for a malformed path.
func isFSNotExist(err error) bool {
	return stderrors.Is(err, fs.ErrNotExist) || stderrors.Is(err, fs.ErrInvalid)
}

// FuncLoader adapts a plain callback into a Loader. The callback returns the
// template source and a boolean reporting whether the name is known; a false
// second result becomes the canonical not-found error. It is the lightest way
// to source templates from a database, a config object, or any lookup a host
// already owns.
type FuncLoader struct {
	fn func(name string) (string, bool)
}

// NewFuncLoader builds a FuncLoader over fn. A nil fn yields a loader for which
// every name is not found.
func NewFuncLoader(fn func(name string) (string, bool)) *FuncLoader {
	return &FuncLoader{fn: fn}
}

// Get calls the callback and returns the source, or a not-found error when the
// callback reports the name is unknown.
func (l *FuncLoader) Get(name string) (*source.Source, error) {
	if l.fn == nil {
		return nil, notFound(name)
	}
	code, ok := l.fn(name)
	if !ok {
		return nil, notFound(name)
	}
	return source.New(name, code), nil
}

// Exists reports whether the callback knows the name.
func (l *FuncLoader) Exists(name string) bool {
	if l.fn == nil {
		return false
	}
	_, ok := l.fn(name)
	return ok
}
