package service

import "io"

// Flush before the scanner waits for another upstream read. Already buffered
// chunks leave together without adding a timer or delaying the next byte.
type flushBeforeUpstreamRead struct {
	r     io.Reader
	flush func()
}

func (f *flushBeforeUpstreamRead) Read(p []byte) (int, error) {
	f.flush()
	return f.r.Read(p)
}
