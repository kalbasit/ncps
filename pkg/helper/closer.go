package helper

import (
	"errors"
	"io"
)

// MultiReadCloser wraps an io.Reader and multiple io.Closers into a single io.ReadCloser.
// When Close() is called, it calls Close() on all provided closers.
type MultiReadCloser struct {
	io.Reader
	closers []io.Closer
}

// NewMultiReadCloser creating a new MultiReadCloser that wraps the given reader and closers.
func NewMultiReadCloser(r io.Reader, closers ...io.Closer) io.ReadCloser {
	return &MultiReadCloser{
		Reader:  r,
		closers: closers,
	}
}

// Close calls Close() on all underlying closers. It returns the first error encountered.
func (m *MultiReadCloser) Close() error {
	var errs []error

	for _, c := range m.closers {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
