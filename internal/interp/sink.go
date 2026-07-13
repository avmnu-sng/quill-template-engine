package interp

import "io"

// writerSink adapts an io.Writer to the push-based Sink so a render can stream
// output directly to the caller's writer with bounded memory. It records the
// first write error and short-circuits every later write, so a failed
// destination (a closed connection) stops the walk promptly and the error
// surfaces at RenderTo.
type writerSink struct {
	w   io.Writer
	sw  io.StringWriter // non-nil when w natively accepts strings
	err error
}

// newWriterSink wraps w, preferring its io.StringWriter fast path when present
// so streamed strings avoid a []byte conversion copy per write.
func newWriterSink(w io.Writer) *writerSink {
	s := &writerSink{w: w}
	if sw, ok := w.(io.StringWriter); ok {
		s.sw = sw
	}
	return s
}

// WriteString streams s to the underlying writer. Once any write has failed it
// writes nothing and returns the recorded error, so the render walk unwinds on
// the first destination failure instead of pushing bytes into a dead writer.
func (s *writerSink) WriteString(str string) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	var n int
	var err error
	if s.sw != nil {
		n, err = s.sw.WriteString(str)
	} else {
		n, err = s.w.Write([]byte(str))
	}
	if err != nil {
		s.err = err
	}
	return n, err
}

// flush forwards a @flush statement to the destination when the active sink is
// a streaming writerSink over a flushable writer (a bufio.Writer). On every
// other sink (a capture buffer or a buffered render's strings.Builder) it
// stays the documented no-op (spec 01 Section 4.4), so buffered output is
// byte-identical with or without @flush.
func (in *interp) flush() error {
	ws, ok := in.out.(*writerSink)
	if !ok {
		return nil
	}
	if ws.err != nil {
		return ws.err
	}
	if f, ok := ws.w.(interface{ Flush() error }); ok {
		if err := f.Flush(); err != nil {
			ws.err = err
			return err
		}
	}
	return nil
}
