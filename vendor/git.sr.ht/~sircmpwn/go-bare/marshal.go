package bare

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

// A type which implements this interface will be responsible for marshaling
// itself when encountered.
type Marshalable interface {
	Marshal(w *Writer) error
}

var encoderBufferPool = sync.Pool{
	New: func() interface{} {
		buf := &bytes.Buffer{}
		buf.Grow(32)
		return buf
	},
}

// Marshals a value (val, which must be a pointer) into a BARE message.
//
// The encoding of each struct field can be customized by the format string
// stored under the "bare" key in the struct field's tag.
//
// As a special case, if the field tag is "-", the field is always omitted.
func Marshal(val interface{}) ([]byte, error) {
	// reuse buffers from previous serializations
	b := encoderBufferPool.Get().(*bytes.Buffer)
	defer func() {
		b.Reset()
		encoderBufferPool.Put(b)
	}()

	w := NewWriter(b)
	err := MarshalWriter(w, val)

	msg := make([]byte, b.Len())
	copy(msg, b.Bytes())

	return msg, err
}

// Marshals a value (val, which must be a pointer) into a BARE message and
// writes it to a Writer. See Marshal for details.
func MarshalWriter(w *Writer, val interface{}) error {
	t := reflect.TypeOf(val)
	v := reflect.ValueOf(val)
	if t.Kind() != reflect.Ptr {
		return errors.New("Expected val to be pointer type")
	}

	return getEncoder(t.Elem())(w, v.Elem())
}

type encodeFunc func(w *Writer, v reflect.Value) error

var encodeFuncCache sync.Map // map[reflect.Type]encodeFunc

// get decoder from cache
func getEncoder(t reflect.Type) encodeFunc {
	if f, ok := encodeFuncCache.Load(t); ok {
		return f.(encodeFunc)
	}

	f := encoderFunc(t)
	encodeFuncCache.Store(t, f)
	return f
}

var marshalableInterface = reflect.TypeOf((*Unmarshalable)(nil)).Elem()

func encoderFunc(t reflect.Type) encodeFunc {
	if reflect.PtrTo(t).Implements(marshalableInterface) {
		return func(w *Writer, v reflect.Value) error {
			uv := v.Addr().Interface().(Marshalable)
			return uv.Marshal(w)
		}
	}

	if t.Kind() == reflect.Interface && t.Implements(unionInterface) {
		return encodeUnion(t)
	}

	switch t.Kind() {
	case reflect.Ptr:
		return encodeOptional(t.Elem())
	case reflect.Struct:
		return encodeStruct(t)
	case reflect.Array:
		return encodeArray(t)
	case reflect.Slice:
		return encodeSlice(t)
	case reflect.Map:
		return encodeMap(t)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return encodeUint
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return encodeInt
	case reflect.Float32, reflect.Float64:
		return encodeFloat
	case reflect.Bool:
		return encodeBool
	case reflect.String:
		return encodeString
	}

	return func(w *Writer, v reflect.Value) error {
		return &UnsupportedTypeError{v.Type()}
	}
}

func encodeOptional(t reflect.Type) encodeFunc {
	return func(w *Writer, v reflect.Value) error {
		if v.IsNil() {
			return w.WriteBool(false)
		}

		if err := w.WriteBool(true); err != nil {
			return err
		}

		return getEncoder(t)(w, v.Elem())
	}
}

func encodeStruct(t reflect.Type) encodeFunc {
	n := t.NumField()
	encoders := make([]encodeFunc, n)
	for i := 0; i < n; i++ {
		field := t.Field(i)
		if field.Tag.Get("bare") == "-" {
			continue
		}
		encoders[i] = getEncoder(field.Type)
	}

	return func(w *Writer, v reflect.Value) error {
		for i := 0; i < n; i++ {
			if encoders[i] == nil {
				continue
			}
			err := encoders[i](w, v.Field(i))
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func encodeArray(t reflect.Type) encodeFunc {
	f := getEncoder(t.Elem())
	len := t.Len()

	return func(w *Writer, v reflect.Value) error {
		for i := 0; i < len; i++ {
			if err := f(w, v.Index(i)); err != nil {
				return err
			}
		}
		return nil
	}
}

func encodeSlice(t reflect.Type) encodeFunc {
	elem := t.Elem()
	f := getEncoder(elem)

	return func(w *Writer, v reflect.Value) error {
		if err := w.WriteUint(uint64(v.Len())); err != nil {
			return err
		}

		for i := 0; i < v.Len(); i++ {
			if err := f(w, v.Index(i)); err != nil {
				return err
			}
		}
		return nil
	}
}

func encodeMap(t reflect.Type) encodeFunc {
	keyType := t.Key()
	keyf := getEncoder(keyType)

	valueType := t.Elem()
	valf := getEncoder(valueType)

	return func(w *Writer, v reflect.Value) error {
		if err := w.WriteUint(uint64(v.Len())); err != nil {
			return err
		}

		iter := v.MapRange()
		for iter.Next() {
			if err := keyf(w, iter.Key()); err != nil {
				return err
			}
			if err := valf(w, iter.Value()); err != nil {
				return err
			}
		}
		return nil
	}
}

func encodeUnion(t reflect.Type) encodeFunc {
	ut, ok := unionRegistry[t]
	if !ok {
		return func(w *Writer, v reflect.Value) error {
			return fmt.Errorf("Union type %s is not registered", t.Name())
		}
	}

	encoders := make(map[uint64]encodeFunc)
	for tag, t := range ut.types {
		encoders[tag] = getEncoder(t)
	}

	return func(w *Writer, v reflect.Value) error {
		t := v.Elem().Type()
		if t.Kind() == reflect.Ptr {
			// If T is a valid union value type, *T is valid too.
			t = t.Elem()
			v = v.Elem()
		}
		tag, ok := ut.tags[t]
		if !ok {
			return fmt.Errorf("Invalid union value: %s", v.Elem().String())
		}

		if err := w.WriteUint(tag); err != nil {
			return err
		}

		return encoders[tag](w, v.Elem())
	}
}

func encodeUint(w *Writer, v reflect.Value) error {
	switch getIntKind(v.Type()) {
	case reflect.Uint:
		return w.WriteUint(v.Uint())

	case reflect.Uint8:
		return w.WriteU8(uint8(v.Uint()))

	case reflect.Uint16:
		return w.WriteU16(uint16(v.Uint()))

	case reflect.Uint32:
		return w.WriteU32(uint32(v.Uint()))

	case reflect.Uint64:
		return w.WriteU64(uint64(v.Uint()))
	}

	panic("not uint")
}

func encodeInt(w *Writer, v reflect.Value) error {
	switch getIntKind(v.Type()) {
	case reflect.Int:
		return w.WriteInt(v.Int())

	case reflect.Int8:
		return w.WriteI8(int8(v.Int()))

	case reflect.Int16:
		return w.WriteI16(int16(v.Int()))

	case reflect.Int32:
		return w.WriteI32(int32(v.Int()))

	case reflect.Int64:
		return w.WriteI64(int64(v.Int()))
	}

	panic("not int")
}

func encodeFloat(w *Writer, v reflect.Value) error {
	switch v.Type().Kind() {
	case reflect.Float32:
		return w.WriteF32(float32(v.Float()))
	case reflect.Float64:
		return w.WriteF64(v.Float())
	}

	panic("not float")
}

func encodeBool(w *Writer, v reflect.Value) error {
	return w.WriteBool(v.Bool())
}

func encodeString(w *Writer, v reflect.Value) error {
	if v.Kind() != reflect.String {
		panic("not string")
	}
	return w.WriteString(v.String())
}
