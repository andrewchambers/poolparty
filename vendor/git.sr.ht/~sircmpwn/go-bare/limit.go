package bare

import (
	"errors"
	"io"
)

var (
	maxUnmarshalBytes uint64 = 1024 * 1024 * 32 /* 32 MiB */
	maxArrayLength    uint64 = 1024 * 4         /* 4096 elements */
	maxMapSize        uint64 = 1024
)

// MaxUnmarshalBytes sets the maximum size of a message decoded by unmarshal.
// By default, this is set to 32 MiB.
func MaxUnmarshalBytes(bytes uint64) {
	maxUnmarshalBytes = bytes
}

// MaxArrayLength sets maximum number of elements in array. Defaults to 4096 elements
func MaxArrayLength(length uint64) {
	maxArrayLength = length
}

// MaxMapSize sets maximum size of map. Defaults to 1024 key/value pairs
func MaxMapSize(size uint64) {
	maxMapSize = size
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
