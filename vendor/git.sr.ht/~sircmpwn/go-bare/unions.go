package bare

import (
	"fmt"
	"reflect"
)

// Any type which is a union member must implement this interface. You must
// also call RegisterUnion for go-bare to marshal or unmarshal messages which
// utilize your union type.
type Union interface {
	IsUnion()
}

type UnionTags struct {
	iface reflect.Type
	tags  map[reflect.Type]uint64
	types map[uint64]reflect.Type
}

var unionInterface = reflect.TypeOf((*Union)(nil)).Elem()
var unionRegistry map[reflect.Type]*UnionTags

func init() {
	unionRegistry = make(map[reflect.Type]*UnionTags)
}

// Registers a union type in this context. Pass the union interface and the
// list of types associated with it, sorted ascending by their union tag.
func RegisterUnion(iface interface{}) *UnionTags {
	ity := reflect.TypeOf(iface).Elem()
	if _, ok := unionRegistry[ity]; ok {
		panic(fmt.Errorf("Type %s has already been registered", ity.Name()))
	}

	if !ity.Implements(reflect.TypeOf((*Union)(nil)).Elem()) {
		panic(fmt.Errorf("Type %s does not implement bare.Union", ity.Name()))
	}

	utypes := &UnionTags{
		iface: ity,
		tags:  make(map[reflect.Type]uint64),
		types: make(map[uint64]reflect.Type),
	}
	unionRegistry[ity] = utypes
	return utypes
}

func (ut *UnionTags) Member(t interface{}, tag uint64) *UnionTags {
	ty := reflect.TypeOf(t)
	if !ty.AssignableTo(ut.iface) {
		panic(fmt.Errorf("Type %s does not implement interface %s",
			ty.Name(), ut.iface.Name()))
	}
	if _, ok := ut.tags[ty]; ok {
		panic(fmt.Errorf("Type %s is already registered for union %s",
			ty.Name(), ut.iface.Name()))
	}
	if _, ok := ut.types[tag]; ok {
		panic(fmt.Errorf("Tag %d is already registered for union %s",
			tag, ut.iface.Name()))
	}
	ut.tags[ty] = tag
	ut.types[tag] = ty
	return ut
}

func (ut *UnionTags) TagFor(v interface{}) (uint64, bool) {
	tag, ok := ut.tags[reflect.TypeOf(v)]
	return tag, ok
}

func (ut *UnionTags) TypeFor(tag uint64) (reflect.Type, bool) {
	t, ok := ut.types[tag]
	return t, ok
}
