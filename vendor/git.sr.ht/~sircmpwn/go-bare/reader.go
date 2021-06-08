package bare

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"unicode/utf8"
)

type byteReader interface {
	io.Reader
	io.ByteReader
}

// A Reader for BARE primitive types.
type Reader struct {
	base    byteReader
	scratch [8]byte
}

type simpleByteReader struct {
	io.Reader
	scratch [1]byte
}

func (r simpleByteReader) ReadByte() (byte, error) {
	// using reference type here saves us allocations
	_, err := r.Read(r.scratch[:])
	return r.scratch[0], err
}

// Returns a new BARE primitive reader wrapping the given io.Reader.
func NewReader(base io.Reader) *Reader {
	br, ok := base.(byteReader)
	if !ok {
		br = simpleByteReader{Reader: base}
	}
	return &Reader{base: br}
}

func (r *Reader) ReadUint() (uint64, error) {
	x, err := binary.ReadUvarint(r.base)
	if err != nil {
		return x, err
	}
	return x, nil
}

func (r *Reader) ReadU8() (uint8, error) {
	return r.base.ReadByte()
}

func (r *Reader) ReadU16() (uint16, error) {
	var i uint16
	if _, err := io.ReadAtLeast(r.base, r.scratch[:2], 2); err != nil {
		return i, err
	}
	return binary.LittleEndian.Uint16(r.scratch[:]), nil
}

func (r *Reader) ReadU32() (uint32, error) {
	var i uint32
	if _, err := io.ReadAtLeast(r.base, r.scratch[:4], 4); err != nil {
		return i, err
	}
	return binary.LittleEndian.Uint32(r.scratch[:]), nil
}

func (r *Reader) ReadU64() (uint64, error) {
	var i uint64
	if _, err := io.ReadAtLeast(r.base, r.scratch[:8], 8); err != nil {
		return i, err
	}
	return binary.LittleEndian.Uint64(r.scratch[:]), nil
}

func (r *Reader) ReadInt() (int64, error) {
	return binary.ReadVarint(r.base)
}

func (r *Reader) ReadI8() (int8, error) {
	b, err := r.base.ReadByte()
	return int8(b), err
}

func (r *Reader) ReadI16() (int16, error) {
	var i int16
	if _, err := io.ReadAtLeast(r.base, r.scratch[:2], 2); err != nil {
		return i, err
	}
	return int16(binary.LittleEndian.Uint16(r.scratch[:])), nil
}

func (r *Reader) ReadI32() (int32, error) {
	var i int32
	if _, err := io.ReadAtLeast(r.base, r.scratch[:4], 4); err != nil {
		return i, err
	}
	return int32(binary.LittleEndian.Uint32(r.scratch[:])), nil
}

func (r *Reader) ReadI64() (int64, error) {
	var i int64
	if _, err := io.ReadAtLeast(r.base, r.scratch[:], 8); err != nil {
		return i, err
	}
	return int64(binary.LittleEndian.Uint64(r.scratch[:])), nil
}

func (r *Reader) ReadF32() (float32, error) {
	u, err := r.ReadU32()
	f := math.Float32frombits(u)
	if math.IsNaN(float64(f)) {
		return 0.0, fmt.Errorf("NaN is not permitted in BARE floats")
	}
	return f, err
}

func (r *Reader) ReadF64() (float64, error) {
	u, err := r.ReadU64()
	f := math.Float64frombits(u)
	if math.IsNaN(f) {
		return 0.0, fmt.Errorf("NaN is not permitted in BARE floats")
	}
	return f, err
}

func (r *Reader) ReadBool() (bool, error) {
	b, err := r.ReadU8()
	if err != nil {
		return false, err
	}

	if b > 1 {
		return false, fmt.Errorf("Invalid bool value: %#x", b)
	}

	return b == 1, nil
}

func (r *Reader) ReadString() (string, error) {
	buf, err := r.ReadData()
	if err != nil {
		return "", err
	}
	if !utf8.Valid(buf) {
		return "", ErrInvalidStr
	}
	return string(buf), nil
}

// Reads a fixed amount of arbitrary data, defined by the length of the slice.
func (r *Reader) ReadDataFixed(dest []byte) error {
	var amt int = 0
	for amt < len(dest) {
		n, err := r.base.Read(dest[amt:])
		if err != nil {
			return err
		}
		amt += n
	}
	return nil
}

// Reads arbitrary data whose length is read from the message.
func (r *Reader) ReadData() ([]byte, error) {
	l, err := r.ReadUint()
	if err != nil {
		return nil, err
	}
	if l >= maxUnmarshalBytes {
		return nil, ErrLimitExceeded
	}
	buf := make([]byte, l)
	var amt uint64 = 0
	for amt < l {
		n, err := r.base.Read(buf[amt:])
		if err != nil {
			return nil, err
		}
		amt += uint64(n)
	}
	return buf, nil
}
