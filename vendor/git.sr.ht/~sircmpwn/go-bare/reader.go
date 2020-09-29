package bare

import (
	"encoding/binary"
	"io"
)

// A Reader for BARE primitive types.
type Reader struct {
	buf    bufferedReader
	buffer [1]byte
}

type bufferedReader struct {
	base   io.Reader
	buffer [1]byte
}

func (r bufferedReader) ReadByte() (byte, error) {
	_, err := r.Read(r.buffer[:])
	return r.buffer[0], err
}

func (r bufferedReader) Read(p []byte) (int, error) {
	return r.base.Read(p)
}

// Returns a new BARE primitive reader wrapping the given io.Reader.
func NewReader(base io.Reader) *Reader {
	return &Reader{buf: bufferedReader{base: base}}
}

func (r *Reader) ReadUint() (uint64, error) {
	return binary.ReadUvarint(r.buf)
}

func (r *Reader) ReadU8() (uint8, error) {
	var i uint8
	err := binary.Read(r.buf, binary.LittleEndian, &i)
	return i, err
}

func (r *Reader) ReadU16() (uint16, error) {
	var i uint16
	err := binary.Read(r.buf, binary.LittleEndian, &i)
	return i, err
}

func (r *Reader) ReadU32() (uint32, error) {
	var i uint32
	err := binary.Read(r.buf, binary.LittleEndian, &i)
	return i, err
}

func (r *Reader) ReadU64() (uint64, error) {
	var i uint64
	err := binary.Read(r.buf, binary.LittleEndian, &i)
	return i, err
}

func (r *Reader) ReadInt() (int64, error) {
	return binary.ReadVarint(r.buf)
}

func (r *Reader) ReadI8() (int8, error) {
	var i int8
	err := binary.Read(r.buf, binary.LittleEndian, &i)
	return i, err
}

func (r *Reader) ReadI16() (int16, error) {
	var i int16
	err := binary.Read(r.buf, binary.LittleEndian, &i)
	return i, err
}

func (r *Reader) ReadI32() (int32, error) {
	var i int32
	err := binary.Read(r.buf, binary.LittleEndian, &i)
	return i, err
}

func (r *Reader) ReadI64() (int64, error) {
	var i int64
	err := binary.Read(r.buf, binary.LittleEndian, &i)
	return i, err
}

func (r *Reader) ReadF32() (float32, error) {
	var f float32
	err := binary.Read(r.buf, binary.LittleEndian, &f)
	return f, err
}

func (r *Reader) ReadF64() (float64, error) {
	var f float64
	err := binary.Read(r.buf, binary.LittleEndian, &f)
	return f, err
}

func (r *Reader) ReadBool() (bool, error) {
	var b bool
	err := binary.Read(r.buf, binary.LittleEndian, &b)
	return b, err
}

func (r *Reader) ReadString() (string, error) {
	buf, err := r.ReadData()
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// Reads a fixed amount of arbitrary data, defined by the length of the slice.
func (r *Reader) ReadDataFixed(dest []byte) error {
	var amt int = 0
	for amt < len(dest) {
		n, err := r.buf.Read(dest[amt:])
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
		n, err := r.buf.Read(buf[amt:])
		if err != nil {
			return nil, err
		}
		amt += uint64(n)
	}
	return buf, nil
}
