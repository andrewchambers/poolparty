package bare

import (
	"errors"
	"fmt"
	"reflect"
)

var ErrInvalidStr = errors.New("String contains invalid UTF-8 sequences")

type UnsupportedTypeError struct {
	Type reflect.Type
}

func (e *UnsupportedTypeError) Error() string {
	return fmt.Sprintf("Unsupported type for marshaling: %s\n", e.Type.String())
}
