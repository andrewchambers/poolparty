package bare

import (
	"fmt"
	"reflect"
)

type UnsupportedTypeError struct {
	Type reflect.Type
}

func (e *UnsupportedTypeError) Error() string {
	return fmt.Sprintf("Unsupported type for marshaling: %s\n", e.Type.String())
}
