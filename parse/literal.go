package parse

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/avmnu-sng/quill-template-engine/ast"
	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/lex"
)

// parseIntLit decodes an INT token. The lexer already stripped '_' separators and
// preserved any 0x/0b/0o prefix (lex/literal.go); strconv handles the radix. A
// value out of int64 range is a syntax error.
func parseIntLit(p *parser, t lex.Token) int64 {
	text := t.Text
	var (
		v   int64
		err error
	)
	switch {
	case strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X"):
		v, err = strconv.ParseInt(text[2:], 16, 64)
	case strings.HasPrefix(text, "0b") || strings.HasPrefix(text, "0B"):
		v, err = strconv.ParseInt(text[2:], 2, 64)
	case strings.HasPrefix(text, "0o") || strings.HasPrefix(text, "0O"):
		v, err = strconv.ParseInt(text[2:], 8, 64)
	default:
		v, err = strconv.ParseInt(text, 10, 64)
	}
	if err != nil {
		p.failAt(t, "invalid integer literal %q: %v", text, err)
	}
	return v
}

// parseFloatLit decodes a FLOAT token (separators already stripped by the lexer).
func parseFloatLit(p *parser, t lex.Token) float64 {
	v, err := strconv.ParseFloat(t.Text, 64)
	if err != nil {
		p.failAt(t, "invalid float literal %q: %v", t.Text, err)
	}
	return v
}

// parseStringLit turns a STRING token into either a plain KindString (single,
// backtick, or escape-only double quote) or, for a double-quoted string with
// "#{ expr }" interpolation, a KindConcat-equivalent KindBinary("~") chain
// (spec 01 Section 1.5, design/expressions Section 10.3). A single-quoted or
// backtick string never interpolates.
func (p *parser) parseStringLit(t lex.Token) *ast.Node {
	p.advance()
	switch t.Quote {
	case lex.QuoteDouble:
		return p.parseInterpString(t)
	default:
		s, err := decodeString(t)
		if err != nil {
			p.failAt(t, "%s", err.Error())
		}
		n := p.node(ast.KindString, t)
		n.Str = s
		return n
	}
}

// parseInterpString splits a double-quoted string into literal runs and
// "#{ expr }" segments, compiling to a left-folded "~" concat chain. A string
// with no interpolation compiles to a single KindString.
func (p *parser) parseInterpString(t lex.Token) *ast.Node {
	raw := t.Text
	body := raw[1 : len(raw)-1] // strip the surrounding quotes
	var parts []*ast.Node
	var lit strings.Builder
	i := 0
	flushLit := func() {
		if lit.Len() > 0 {
			n := p.node(ast.KindString, t)
			n.Str = lit.String()
			parts = append(parts, n)
			lit.Reset()
		}
	}
	for i < len(body) {
		c := body[i]
		if c == '\\' && i+1 < len(body) {
			// \#{ is a literal "#{"; other escapes decode normally.
			if body[i+1] == '#' && i+2 < len(body) && body[i+2] == '{' {
				lit.WriteString("#{")
				i += 3
				continue
			}
			r, n, err := decodeEscape(body[i:], true)
			if err != nil {
				p.failAt(t, "%s", err.Error())
			}
			lit.WriteString(r)
			i += n
			continue
		}
		if c == '#' && i+1 < len(body) && body[i+1] == '{' {
			flushLit()
			// Find the balancing '}'.
			depth := 1
			j := i + 2
			for j < len(body) && depth > 0 {
				switch body[j] {
				case '{':
					depth++
				case '}':
					depth--
				}
				if depth == 0 {
					break
				}
				j++
			}
			if depth != 0 {
				p.failAt(t, "unterminated string interpolation '#{' in string literal")
			}
			exprSrc := body[i+2 : j]
			parts = append(parts, p.parseSubExpr(t, exprSrc))
			i = j + 1
			continue
		}
		lit.WriteByte(c)
		i++
	}
	flushLit()
	if len(parts) == 0 {
		n := p.node(ast.KindString, t)
		return n
	}
	if len(parts) == 1 {
		// A lone interpolation still renders to text via the surrounding {{ }}, but
		// to preserve "string" typing we wrap a sole expression segment in a concat
		// with an empty string when it is not itself a string literal. A
		// double-quoted "#{x}" is contractually a string (it "compiles to a ~ concat
		// chain", spec 01 Section 1.5 / design/expressions 10.3), so its static type
		// must be string -- "" ~ x -- not the raw expression x.
		if parts[0].Kind == ast.KindString {
			return parts[0]
		}
		empty := p.node(ast.KindString, t)
		return p.binary(t, "~", empty, parts[0])
	}
	// Left-fold into "a ~ b ~ c".
	acc := parts[0]
	for _, part := range parts[1:] {
		acc = p.binary(t, "~", acc, part)
	}
	return acc
}

// parseSubExpr parses an expression embedded in a string interpolation. It lexes
// the fragment as CODE (by wrapping it in an interpolation so the lexer enters
// CODE mode) and parses one expression, mapping any fault back to the enclosing
// string's position so diagnostics stay anchored.
func (p *parser) parseSubExpr(t lex.Token, src string) (result *ast.Node) {
	toks := codeTokens(p, t, src, "in string interpolation")
	sub := &parser{src: p.src, toks: toks}
	defer func() {
		if r := recover(); r != nil {
			if se, ok := r.(*errors.Error); ok {
				panic(se) // a positioned fault already; let it propagate
			}
			p.failAt(t, "in string interpolation: malformed expression")
		}
	}()
	expr := sub.parseExpr()
	if !sub.at(lex.EOF) {
		p.failAt(t, "in string interpolation: trailing tokens after expression")
	}
	return expr
}

// codeTokens lexes a bare CODE fragment (an expression or a type/decl run) by
// wrapping it in an interpolation so the lexer enters CODE mode, then strips the
// surrounding OPEN_INTERP / CLOSE_INTERP and trailing EOF, leaving a token slice
// terminated by a synthetic EOF. A lexical fault is reported against t.
func codeTokens(p *parser, t lex.Token, src, where string) []lex.Token {
	full := lex.Lex(p.src.WithCode("{{" + src + "}}")).Tokens
	var out []lex.Token
	for _, tk := range full {
		switch tk.Kind {
		case lex.ERROR:
			p.failAt(t, "%s: %s", where, tk.Text)
		case lex.OPEN_INTERP, lex.CLOSE_INTERP, lex.EOF:
			continue
		}
		out = append(out, tk)
	}
	// Append the trailing EOF so the sub-parser's cursor has a sentinel.
	out = append(out, lex.Token{Kind: lex.EOF, Line: t.Line})
	return out
}

// decodeString decodes a STRING token's body per its quote form (spec 01 Section
// 1.5). Single-quoted: \\ \' \n \t \xHH. Double-quoted (escape-only path):
// \n \t \r \\ \" \xHH \u{...}. Backtick: raw, no escapes.
func decodeString(t lex.Token) (string, error) {
	raw := t.Text
	if len(raw) < 2 {
		return "", fmt.Errorf("malformed string literal")
	}
	body := raw[1 : len(raw)-1]
	switch t.Quote {
	case lex.QuoteBacktick:
		return body, nil
	case lex.QuoteSingle:
		return decodeWith(body, false)
	default:
		return decodeWith(body, true)
	}
}

// decodeWith applies the escape rules to body. double selects the double-quoted
// escape set (adds \r and \u{...}); single allows \' .
func decodeWith(body string, double bool) (string, error) {
	var b strings.Builder
	for i := 0; i < len(body); {
		if body[i] == '\\' {
			r, n, err := decodeEscape(body[i:], double)
			if err != nil {
				return "", err
			}
			b.WriteString(r)
			i += n
			continue
		}
		b.WriteByte(body[i])
		i++
	}
	return b.String(), nil
}

// decodeEscape decodes one escape sequence at the start of s (which begins with
// '\'), returning the decoded text and the number of source bytes consumed. The
// double flag enables the double-quoted-only escapes (\r, \u{...}); single quotes
// additionally accept \'.
func decodeEscape(s string, double bool) (string, int, error) {
	if len(s) < 2 {
		return "", 0, fmt.Errorf("dangling backslash in string literal")
	}
	switch s[1] {
	case '\\':
		return "\\", 2, nil
	case 'n':
		return "\n", 2, nil
	case 't':
		return "\t", 2, nil
	case '\'':
		return "'", 2, nil
	case '"':
		return "\"", 2, nil
	case 'r':
		if double {
			return "\r", 2, nil
		}
	case 'x':
		if len(s) < 4 {
			return "", 0, fmt.Errorf("incomplete \\xHH escape in string literal")
		}
		v, err := strconv.ParseUint(s[2:4], 16, 8)
		if err != nil {
			return "", 0, fmt.Errorf("invalid \\xHH escape in string literal")
		}
		return string([]byte{byte(v)}), 4, nil
	case 'u':
		if double {
			return decodeUnicodeEscape(s)
		}
	}
	return "", 0, fmt.Errorf("invalid escape sequence %q in string literal", s[:2])
}

// decodeUnicodeEscape decodes a \u{...} escape (double-quoted only).
func decodeUnicodeEscape(s string) (string, int, error) {
	if len(s) < 4 || s[2] != '{' {
		return "", 0, fmt.Errorf("invalid \\u{...} escape in string literal")
	}
	end := strings.IndexByte(s, '}')
	if end < 0 {
		return "", 0, fmt.Errorf("unterminated \\u{...} escape in string literal")
	}
	v, err := strconv.ParseUint(s[3:end], 16, 32)
	if err != nil || v > 0x10FFFF {
		return "", 0, fmt.Errorf("invalid code point in \\u{...} escape")
	}
	return string(rune(v)), end + 1, nil
}
