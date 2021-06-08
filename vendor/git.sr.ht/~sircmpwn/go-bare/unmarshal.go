package bare

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
)

// A type which implements this interface will be responsible for unmarshaling
// itself when encountered.
type Unmarshalable interface {
	Unmarshal(r *Reader) error
}

// Unmarshals a BARE message into val, which must be a pointer to a value of
// the message type.
func Unmarshal(data []byte, val interface{}) error {
	b := bytes.NewReader(data)
	r := NewReader(b)
	return UnmarshalBareReader(r, val)
}

// Unmarshals a BARE message into value (val, which must be a pointer), from a
// reader. See Unmarshal for details.
func UnmarshalReader(r io.Reader, val interface{}) error {
	r = newLimitedReader(r)
	return UnmarshalBareReader(NewReader(r), val)
}

type decodeFunc func(r *Reader, v reflect.Value) error

var decodeFuncCache sync.Map // map[reflect.Type]decodeFunc

func UnmarshalBareReader(r *Reader, val interface{}) error {
	t := reflect.TypeOf(val)
	v := reflect.ValueOf(val)
	if t.Kind() != reflect.Ptr {
		return errors.New("Expected val to be pointer type")
	}

	return getDecoder(t.Elem())(r, v.Elem())
}

// get decoder from cache
func getDecoder(t reflect.Type) decodeFunc {
	if f, ok := decodeFuncCache.Load(t); ok {
		return f.(decodeFunc)
	}

	f := decoderFunc(t)
	decodeFuncCache.Store(t, f)
	return f
}

var unmarshalableInterface = reflect.TypeOf((*Unmarshalable)(nil)).Elem()

func decoderFunc(t reflect.Type) decodeFunc {
	if reflect.PtrTo(t).Implements(unmarshalableInterface) {
		return func(r *Reader, v reflect.Value) error {
			uv := v.Addr().Interface().(Unmarshalable)
			return uv.Unmarshal(r)
		}
	}

	if t.Kind() == reflect.Interface && t.Implements(unionInterface) {
		return decodeUnion(t)
	}

	switch t.Kind() {
	case reflect.Ptr:
		return decodeOptional(t.Elem())
	case reflect.Struct:
		return decodeStruct(t)
	case reflect.Array:
		return decodeArray(t)
	case reflect.Slice:
		return decodeSlice(t)
	case reflect.Map:
		return decodeMap(t)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return decodeUint
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return decodeInt
	case reflect.Float32, reflect.Float64:
		return decodeFloat
	case reflect.Bool:
		return decodeBool
	case reflect.String:
		return decodeString
	}

	return func(r *Reader, v reflect.Value) error {
		return &UnsupportedTypeError{v.Type()}
	}
}

func decodeOptional(t reflect.Type) decodeFunc {
	return func(r *Reader, v reflect.Value) error {
		s, err := r.ReadU8()
		if err != nil {
			return err
		}

		if s > 1 {
			return fmt.Errorf("Invalid optional value: %#x", s)
		}

		if s == 0 {
			return nil
		}

		v.Set(reflect.New(t))
		return getDecoder(t)(r, v.Elem())
	}
}

func decodeStruct(t reflect.Type) decodeFunc {
	n := t.NumField()
	decoders := make([]decodeFunc, n)
	for i := 0; i < n; i++ {
		field := t.Field(i)
		if field.Tag.Get("bare") == "-" {
			continue
		}
		decoders[i] = getDecoder(field.Type)
	}

	return func(r *Reader, v reflect.Value) error {
		for i := 0; i < n; i++ {
			if decoders[i] == nil {
				continue
			}
			err := decoders[i](r, v.Field(i))
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func decodeArray(t reflect.Type) decodeFunc {
	f := getDecoder(t.Elem())
	len := t.Len()

	return func(r *Reader, v reflect.Value) error {
		for i := 0; i < len; i++ {
			err := f(r, v.Index(i))
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func decodeSlice(t reflect.Type) decodeFunc {
	elem := t.Elem()
	f := getDecoder(elem)

	return func(r *Reader, v reflect.Value) error {
		len, err := r.ReadUint()
		if err != nil {
			return err
		}

		if len > maxArrayLength {
			return fmt.Errorf("Array length %d exceeds configured limit of %d", len, maxArrayLength)
		}

		v.Set(reflect.MakeSlice(t, int(len), int(len)))

		for i := 0; i < int(len); i++ {
			if err := f(r, v.Index(i)); err != nil {
				return err
			}
		}
		return nil
	}
}

func decodeMap(t reflect.Type) decodeFunc {
	keyType := t.Key()
	keyf := getDecoder(keyType)

	valueType := t.Elem()
	valf := getDecoder(valueType)

	return func(r *Reader, v reflect.Value) error {
		size, err := r.ReadUint()
		if err != nil {
			return err
		}

		if size > maxMapSize {
			return fmt.Errorf("Map size %d exceeds configured limit of %d", size, maxMapSize)
		}

		v.Set(reflect.MakeMapWithSize(t, int(size)))

		key := reflect.New(keyType).Elem()
		value := reflect.New(valueType).Elem()

		for i := uint64(0); i < size; i++ {
			if err := keyf(r, key); err != nil {
				return err
			}

			if v.MapIndex(key).Kind() > reflect.Invalid {
				return fmt.Errorf("Encountered duplicate map key: %v", key.Interface())
			}

			if err := valf(r, value); err != nil {
				return err
			}

			v.SetMapIndex(key, value)
		}
		return nil
	}
}

func decodeUnion(t reflect.Type) decodeFunc {
	ut, ok := unionRegistry[t]
	if !ok {
		return func(r *Reader, v reflect.Value) error {
			return fmt.Errorf("Union type %s is not registered", t.Name())
		}
	}

	decoders := make(map[uint64]decodeFunc)
	for tag, t := range ut.types {
		t := t
		f := getDecoder(t)

		decoders[tag] = func(r *Reader, v reflect.Value) error {
			nv := reflect.New(t)
			if err := f(r, nv.Elem()); err != nil {
				return err
			}

			v.Set(nv)
			return nil
		}
	}

	return func(r *Reader, v reflect.Value) error {
		tag, err := r.ReadUint()
		if err != nil {
			return err
		}

		if f, ok := decoders[tag]; ok {
			return f(r, v)
		}

		return fmt.Errorf("Invalid union tag %d for type %s", tag, t.Name())
	}
}

func decodeUint(r *Reader, v reflect.Value) error {
	var err error
	switch getIntKind(v.Type()) {
	case reflect.Uint:
		var u uint64
		u, err = r.ReadUint()
		v.SetUint(u)

	case reflect.Uint8:
		var u uint8
		u, err = r.ReadU8()
		v.SetUint(uint64(u))

	case reflect.Uint16:
		var u uint16
		u, err = r.ReadU16()
		v.SetUint(uint64(u))
	case reflect.Uint32:
		var u uint32
		u, err = r.ReadU32()
		v.SetUint(uint64(u))

	case reflect.Uint64:
		var u uint64
		u, err = r.ReadU64()
		v.SetUint(uint64(u))

	default:
		panic("not an uint")
	}

	return err
}

func decodeInt(r *Reader, v reflect.Value) error {
	var err error
	switch getIntKind(v.Type()) {
	case reflect.Int:
		var i int64
		i, err = r.ReadInt()
		v.SetInt(i)

	case reflect.Int8:
		var i int8
		i, err = r.ReadI8()
		v.SetInt(int64(i))

	case reflect.Int16:
		var i int16
		i, err = r.ReadI16()
		v.SetInt(int64(i))
	case reflect.Int32:
		var i int32
		i, err = r.ReadI32()
		v.SetInt(int64(i))

	case reflect.Int64:
		var i int64
		i, err = r.ReadI64()
		v.SetInt(int64(i))

	default:
		panic("not an int")
	}

	return err
}

func decodeFloat(r *Reader, v reflect.Value) error {
	var err error
	switch v.Type().Kind() {
	case reflect.Float32:
		var f float32
		f, err = r.ReadF32()
		v.SetFloat(float64(f))
	case reflect.Float64:
		var f float64
		f, err = r.ReadF64()
		v.SetFloat(f)
	default:
		panic("not a float")
	}
	return err
}

func decodeBool(r *Reader, v reflect.Value) error {
	b, err := r.ReadBool()
	v.SetBool(b)
	return err
}

func decodeString(r *Reader, v reflect.Value) error {
	s, err := r.ReadString()
	v.SetString(s)
	return err
}
