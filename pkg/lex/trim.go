package lex

import "strings"

// applyTrims rewrites the TEXT tokens that border trim-flagged delimiters, in
// place, so the renderer never needs to know about whitespace control: by the
// time the parser sees the stream the affected TEXT bytes are already gone. This
// is the propagation step that turns the lexer-recorded TrimL/TrimR flags into
// actual output (spec 01 Section 1.4, spec 02 R14).
//
// Direction rule:
//   - An open-side trim (OPEN_INTERP.TrimL) strips the TRAILING whitespace of the
//     TEXT immediately before the delimiter.
//   - A close-side trim (CLOSE_INTERP.TrimR, BLOCK_OPEN.TrimR, BLOCK_CLOSE.TrimR)
//     strips the LEADING whitespace of the TEXT immediately after the delimiter.
//
// BLOCK_OPEN.TrimR is the inner-edge "{-" / "{~" form, so it trims into the block
// body (the following TEXT); BLOCK_CLOSE.TrimR is the "@}-" / "@}~" form, trimming
// the TEXT after the close. TrimKeep ('+') changes only the lexer's structural
// newline-eating, which already happened during scanning, so it is a no-op here.
func applyTrims(toks []Token) {
	for i := range toks {
		t := toks[i]
		switch t.Kind {
		case OPEN_INTERP:
			if t.TrimL != TrimNone && i > 0 && toks[i-1].Kind == TEXT {
				toks[i-1].Text = trimRight(toks[i-1].Text, t.TrimL)
			}
		case CLOSE_INTERP, BLOCK_OPEN, BLOCK_CLOSE:
			if t.TrimR != TrimNone && t.TrimR != TrimKeep && i+1 < len(toks) && toks[i+1].Kind == TEXT {
				toks[i+1].Text = trimLeft(toks[i+1].Text, t.TrimR)
			}
		}
	}
}

// lineWS is the horizontal-whitespace set a line trim ('~') strips: spaces,
// tabs, NUL, and vertical tab, but never a newline (spec 01 Section 1.4).
const lineWS = " \t\x00\v"

// hardWS adds the newline and carriage return to lineWS, so a hard trim ('-')
// strips across line boundaries.
const hardWS = lineWS + "\n\r"

// trimRight strips trailing whitespace from s according to the trim mode.
func trimRight(s string, mode Trim) string {
	switch mode {
	case TrimHard:
		return strings.TrimRight(s, hardWS)
	case TrimLine:
		return strings.TrimRight(s, lineWS)
	default:
		return s
	}
}

// trimLeft strips leading whitespace from s according to the trim mode.
func trimLeft(s string, mode Trim) string {
	switch mode {
	case TrimHard:
		return strings.TrimLeft(s, hardWS)
	case TrimLine:
		return strings.TrimLeft(s, lineWS)
	default:
		return s
	}
}
