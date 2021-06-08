package bare

import (
	"reflect"
)

// Int is a variable-length encoded signed integer.
type Int int64

// Uint is a variable-length encoded unsigned integer.
type Uint uint64

var (
	intType  = reflect.TypeOf(Int(0))
	uintType = reflect.TypeOf(Uint(0))
)

func getIntKind(t reflect.Type) reflect.Kind {
	switch t {
	case intType:
		return reflect.Int
	case uintType:
		return reflect.Uint
	default:
		return t.Kind()
	}
}
