package bare

import (
	"errors"
	"io"
)

var maxUnmarshalBytes uint64 = 33554432 /* 32 MiB */

// Sets the maximum size of a message decoded by unmarshal. By default, this is
// set to 32 MiB.
func MaxUnmarshalBytes(bytes uint64) {
	maxUnmarshalBytes = bytes
}

// Use MaxUnmarshalBytes to prevent this error from occuring on messages which
// are large by design.
var ErrLimitExceeded = errors.New("Maximum message size exceeded")

// Identical to io.LimitedReader, except it returns our custom error instead of
// EOF if the limit is reached.
type limitedReader struct {
	R io.Reader
	N uint64
}

func (l *limitedReader) Read(p []byte) (n int, err error) {
	if l.N <= 0 {
		return 0, ErrLimitExceeded
	}
	if uint64(len(p)) > l.N {
		p = p[0:l.N]
	}
	n, err = l.R.Read(p)
	l.N -= uint64(n)
	return
}

func newLimitedReader(r io.Reader) *limitedReader {
	return &limitedReader{r, maxUnmarshalBytes}
}
