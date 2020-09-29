package bare

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
)

// A type which implements this interface will be responsible for marshaling
// itself when encountered.
type Marshalable interface {
	Marshal(w *Writer) error
}

// Marshals a value (val, which must be a pointer) into a BARE message.
//
// Go "int" and "uint" types are represented as BARE u32 and i32 types
// respectively, for message compatibility with both 32-bit and 64-bit systems.
func Marshal(val interface{}) ([]byte, error) {
	b := bytes.NewBuffer([]byte{})
	w := NewWriter(b)
	err := MarshalWriter(w, val)
	return b.Bytes(), err
}

// Marshals a value (val, which must be a pointer) into a BARE message and
// writes it to a Writer. See Marshal for details.
func MarshalWriter(w *Writer, val interface{}) error {
	t := reflect.TypeOf(val)
	v := reflect.ValueOf(val)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
		v = v.Elem()
	} else {
		return errors.New("Expected val to be pointer type, was " +
			t.Kind().String())
	}

	return marshalWriter(w, t, v)
}

func marshalWriter(w *Writer,
	t reflect.Type, v reflect.Value) error {
	if t.Kind() == reflect.Ptr {
		// optional<type>
		var set uint8
		if !v.IsZero() {
			t = t.Elem()
			v = v.Elem()
			set = 1
		}
		err := w.WriteU8(set)
		if err != nil {
			return err
		}
		if set == 0 {
			return nil
		}
	}

	if t.Kind() == reflect.Interface &&
		v.Type().Implements(reflect.TypeOf((*Union)(nil)).Elem()) {
		ut, ok := unionRegistry[t]
		if !ok {
			return fmt.Errorf("Union type %s is not registered", t.Name())
		}

		if tag, ok := ut.TagFor(v.Interface()); ok {
			err := w.WriteU8(uint8(tag))
			if err != nil {
				return err
			}
		} else {
			return fmt.Errorf("Union type %s is not a registered member of %s",
				reflect.TypeOf(v.Interface()).Name(), t.Name())
		}

		v = reflect.ValueOf(v.Interface())
		t = v.Type()
	}

	if v.CanAddr() {
		if marshal, ok := v.Addr().Interface().(Marshalable); ok {
			return marshal.Marshal(w)
		}
	}

	// TODO: Struct tags for explicit varints (useful on 32-bit systems)
	switch t.Kind() {
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
	case reflect.Float32:
		return w.WriteF32(float32(v.Float()))
	case reflect.Float64:
		return w.WriteF64(float64(v.Float()))
	case reflect.Bool:
		return w.WriteBool(v.Bool())
	case reflect.String:
		return w.WriteString(v.String())
	case reflect.Array:
		return marshalArray(w, t, v)
	case reflect.Slice:
		return marshalSlice(w, t, v)
	case reflect.Struct:
		return marshalStruct(w, t, v)
	case reflect.Map:
		return marshalMap(w, t, v)
	}

	return &UnsupportedTypeError{t}
}

func marshalStruct(w *Writer, t reflect.Type, v reflect.Value) error {
	for i := 0; i < t.NumField(); i++ {
		value := v.Field(i)
		err := MarshalWriter(w, value.Addr().Interface())
		if err != nil {
			return err
		}
	}
	return nil
}

func marshalArray(w *Writer, t reflect.Type, v reflect.Value) error {
	for i := 0; i < t.Len(); i++ {
		value := v.Index(i)
		err := MarshalWriter(w, value.Addr().Interface())
		if err != nil {
			return err
		}
	}
	return nil
}

func marshalSlice(w *Writer, t reflect.Type, v reflect.Value) error {
	err := w.WriteUint(uint64(v.Len()))
	if err != nil {
		return err
	}
	for i := 0; i < v.Len(); i++ {
		value := v.Index(i)
		err := MarshalWriter(w, value.Addr().Interface())
		if err != nil {
			return err
		}
	}
	return nil
}

func marshalMap(w *Writer, t reflect.Type, v reflect.Value) error {
	err := w.WriteUint(uint64(v.Len()))
	if err != nil {
		return err
	}
	for _, key := range v.MapKeys() {
		value := v.MapIndex(key)
		err := marshalWriter(w, key.Type(), key)
		if err != nil {
			return err
		}
		err = marshalWriter(w, value.Type(), value)
		if err != nil {
			return err
		}
	}
	return nil
}
