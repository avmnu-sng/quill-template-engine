// Package loader resolves a template name to its source bytes. The engine asks a
// Loader for a template by name when it must render an @extends parent, an
// @include target, or an @import/@from source. Two loaders ship: an ArrayLoader
// backed by an in-memory name->source map (for tests and embedded templates) and
// a FilesystemLoader rooted at a directory.
//
// A miss is reported as a *errors.Error of KindRuntime so the engine can
// distinguish "not found" (which ignore-missing tolerates and a candidate list
// skips) from a genuine I/O failure.
package loader

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
	"github.com/avmnu-sng/quill-template-engine/pkg/source"
)

// ErrNotFound is the sentinel every loader miss wraps. Test for a missing
// template with errors.Is(err, loader.ErrNotFound) rather than inspecting the
// message text: error strings are not part of the compatibility contract, but
// this sentinel is.
var ErrNotFound = stderrors.New("template not found")

// Loader resolves a template name to a *source.Source. Exists is a cheap
// presence probe used by candidate lists and the block("name", "other") /
// ignore-missing paths so the engine need not catch a Get error to test
// existence.
type Loader interface {
	Get(name string) (*source.Source, error)
	Exists(name string) bool
}

// ArrayLoader serves templates from an in-memory map. It is the canonical loader
// for tests and for hosts that compile templates from strings.
type ArrayLoader struct {
	templates map[string]string
}

// NewArrayLoader builds an ArrayLoader over the given name->source map. The map
// is copied so later mutation of the caller's map does not affect the loader.
func NewArrayLoader(templates map[string]string) *ArrayLoader {
	cp := make(map[string]string, len(templates))
	for k, v := range templates {
		cp[k] = v
	}
	return &ArrayLoader{templates: cp}
}

// Set adds or replaces a template, allowing incremental population.
func (l *ArrayLoader) Set(name, src string) { l.templates[name] = src }

// Get returns the named template's source, or a not-found error.
func (l *ArrayLoader) Get(name string) (*source.Source, error) {
	src, ok := l.templates[name]
	if !ok {
		return nil, notFound(name)
	}
	return source.New(name, src), nil
}

// Exists reports whether the named template is present.
func (l *ArrayLoader) Exists(name string) bool {
	_, ok := l.templates[name]
	return ok
}

// FilesystemLoader serves templates from files under a root directory. A name is
// joined to the root and constrained to stay within it, so a "../" name cannot
// escape the configured root.
type FilesystemLoader struct {
	root string
}

// NewFilesystemLoader builds a loader rooted at dir.
func NewFilesystemLoader(dir string) *FilesystemLoader {
	return &FilesystemLoader{root: dir}
}

// resolve joins name to the root and rejects a path that escapes it.
func (l *FilesystemLoader) resolve(name string) (string, error) {
	clean := filepath.Clean("/" + name) // make name root-relative, collapsing ..
	full := filepath.Join(l.root, clean)
	rootAbs, err := filepath.Abs(l.root)
	if err != nil {
		return "", errors.Wrap(errors.KindRuntime, err, "cannot resolve loader root")
	}
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", errors.Wrap(errors.KindRuntime, err, "cannot resolve template path %q", name)
	}
	if fullAbs != rootAbs && !strings.HasPrefix(fullAbs, rootAbs+string(os.PathSeparator)) {
		return "", errors.New(errors.KindRuntime,
			"template %q escapes the loader root", name)
	}
	return fullAbs, nil
}

// Get reads the named file's bytes, or returns a not-found error when absent.
func (l *FilesystemLoader) Get(name string) (*source.Source, error) {
	path, err := l.resolve(name)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, notFound(name)
		}
		return nil, errors.Wrap(errors.KindRuntime, err, "cannot read template %q", name)
	}
	return source.New(name, string(b)), nil
}

// Exists reports whether the named file is present and readable.
func (l *FilesystemLoader) Exists(name string) bool {
	path, err := l.resolve(name)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// notFound builds the canonical not-found error: a KindRuntime *errors.Error
// wrapping ErrNotFound, so callers match it precisely with errors.Is while the
// rendered message still names the missing template.
func notFound(name string) error {
	return errors.Wrap(errors.KindRuntime, ErrNotFound, "template %q not found", name)
}

// IsNotFound reports whether err is (or wraps) a loader not-found error. It is
// errors.Is(err, ErrNotFound); the prior message-substring heuristic is gone, so
// an unrelated error whose text merely contains "not found" no longer matches.
func IsNotFound(err error) bool {
	return stderrors.Is(err, ErrNotFound)
}
