package unsafereflect

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"
)

type Type struct {
	// reflectType is the reflect.Type that this *Type was created
	// from
	reflectType reflect.Type

	// size in bytes of the type
	size int

	uintData [1]uint
}

const (
	abiType = iota
	abiPtrType
	usrType
	usrPtrType
	typeUnsafePointers
)

const (
	udHasLenBit = iota
	udIsArrayBit
	uintDataBits

	udHasLenMask  uint = 1 << udHasLenBit
	udIsArrayMask uint = 1 << udIsArrayBit
)

type typeWithPtrs struct {
	t        Type
	pointers [1]unsafe.Pointer
}

var (
	typePtrReflectType            = reflect.TypeOf((*Type)(nil))
	reflectStructFieldReflectType = reflect.TypeOf((*reflect.StructField)(nil)).Elem()
	unsafePointerReflectType      = reflect.TypeOf((*unsafe.Pointer)(nil)).Elem()
)

var types sync.Map // map[reflect.Type]*Type

// TypeOf gets the unsafe reflect type of a value.
func TypeOf(v interface{}) *Type {
	rt := reflect.TypeOf(v)
	return TypeFromReflectType(rt)
}

// TypeFromReflectType gets the unsafe reflect type from a reflect.Type.
func TypeFromReflectType(rt reflect.Type) (t *Type) {
	key := interface{}(rt)
	if v, loaded := types.Load(key); loaded {
		return v.(*Type)
	}
	numFields := 0
	initFuncs := make([]func(t *Type), 0, 2)
	switch rt.Kind() {
	case reflect.Array:
		initFuncs = append(initFuncs, func(t *Type) {
			t.uintData[0] = (uint(t.ReflectType().Len()) << uintDataBits) | udIsArrayBit | udHasLenMask
		})
		fallthrough
	case reflect.Slice, reflect.Pointer, reflect.UnsafePointer:
		// elem type
		numFields++
		initFuncs = append(initFuncs, func(t *Type) {
			elemType := TypeFromReflectType(t.ReflectType().Elem())
			t.ReflectStructFields()[0] = reflect.StructField{
				Name: "Elem",
				Type: elemType.ReflectType(),
			}
			t.fieldABITypes()[0] = *elemType.abiType()
			t.ptrToFieldABITypes()[0] = *elemType.abiPtrType()
			t.fieldUSRTypes()[0] = elemType
			t.ptrToFieldUSRTypes()[0] = TypeFromReflectType(reflect.PointerTo(elemType.ReflectType()))
		})
	case reflect.Struct:
		// field types + ptr to field types
		numFields += rt.NumField()
		initFuncs = append(initFuncs, func(t *Type) {
			t.uintData[0] |= udHasLenMask
			fieldABITypes := t.fieldABITypes()
			ptrToFieldABITypes := t.ptrToFieldABITypes()
			fieldUSRTypes := t.fieldUSRTypes()
			ptrToFieldUSRTypes := t.ptrToFieldUSRTypes()
			rt := t.ReflectType()
			numFields := t.numFields()
			reflectStructFields := t.ReflectStructFields()
			for i := 0; i < numFields; i++ {
				reflectStructFields[i] = rt.Field(i)
				fieldUSRTypes[i] = TypeFromReflectType(reflectStructFields[i].Type)
				ptrToFieldABITypes[i] = *fieldUSRTypes[i].abiPtrType()
				fieldABITypes[i] = *fieldUSRTypes[i].abiType()
				ptrToFieldUSRTypes[i] = TypeFromReflectType(reflect.PointerTo(reflectStructFields[i].Type))
			}
		})
	}
	// TODO: initFunc for methods
	numPtrs := 2 /* self + ptr to self */ + (typeUnsafePointers * numFields) /* *unsafereflect.Type and *abi.Type of elem/field & ptr to elem/field */
	tv := reflect.New(reflect.StructOf([]reflect.StructField{
		{Name: "Type", Type: typePtrReflectType.Elem()},
		{Name: "Ptrs", Type: reflect.ArrayOf(numPtrs, unsafePointerReflectType)},
		{Name: "Fields", Type: reflect.ArrayOf(numFields, reflectStructFieldReflectType)},
	}))
	t = tv.Elem().Field(0).Addr().Interface().(*Type)
	t.reflectType = rt
	t.size = int(rt.Size())
	t.uintData[0] = uint(numFields) << uintDataBits
	{
		rv := reflect.New(rt)
		v := rv.Interface()
		id := InterfaceDataOf(unsafe.Pointer(&v))
		*t.abiPtrType() = id.Type
		v = rv.Elem().Interface()
		*t.abiType() = id.Type
	}
	// add the type before initializing in case of recursive types
	if v, loaded := types.LoadOrStore(key, t); loaded {
		return v.(*Type)
	}
	tReflectStructFields := t.ReflectStructFields()
	tvReflectStructFields := tv.Elem().Field(2).Slice(0, numFields).Interface().([]reflect.StructField)
	if len(tReflectStructFields) != len(tvReflectStructFields) {
		panic(fmt.Sprintf(
			"len(tReflectStructFields) (%d) != "+
				"len(tvReflectStructFields) (%d)",
			len(tReflectStructFields),
			len(tvReflectStructFields),
		))
	}
	if len(tReflectStructFields) > 0 && &tReflectStructFields[0] != &tvReflectStructFields[0] {
		panic(fmt.Sprintf(
			"&tReflectStructFields[0] (%p) != "+
				"&tvReflectStructFields[0] (%p) = (%x)",
			&tReflectStructFields[0],
			&tvReflectStructFields[0],
			uintptr(unsafe.Pointer(&tvReflectStructFields[0]))-uintptr(unsafe.Pointer(&tReflectStructFields[0])),
		))
	}
	fieldABITypes := t.fieldABITypes()
	ptrToFieldABITypes := t.ptrToFieldABITypes()
	fieldUSRTypes := t.fieldUSRTypes()
	ptrToFieldUSRTypes := t.ptrToFieldUSRTypes()
	numFields = t.numFields()
	if len(fieldABITypes) != numFields {
		panic("len(fieldABITypes) != t.numFields")
	}
	if len(ptrToFieldABITypes) != numFields {
		panic("len(ptrToFieldABITypes) != t.numFields")
	}
	if len(fieldUSRTypes) != numFields {
		panic("len(fieldUSRTypes) != t.numFields")
	}
	if len(ptrToFieldUSRTypes) != numFields {
		panic("len(ptrToFieldUSRTypes) != t.numFields")
	}
	for _, initFunc := range initFuncs {
		initFunc(t)
	}
	return
}

// FieldPointer returns an interface{} of the address of the field
// of the given struct.  Note that this will work, but is unsafe if
// struc is a struct type and not a pointer to struct type.
func (t *Type) FieldPointer(struc interface{}, fieldIndex int) (fieldPointer interface{}) {
	return t.field(struc, fieldIndex, (*Type).ptrToFieldABITypes)
}

// FieldType gets the unsafe reflect type of the field at the given
// index.
func (t *Type) FieldType(fieldIndex int) *Type {
	return t.fieldUSRTypes()[fieldIndex]
}

// Len gets the number of fields if the Type is a struct or the number
// of elements if it is an array.  Otherwise, the return value is
// undefined.
func (t *Type) Len() int { return t.numFields() }

// ReflectStructFields returns a borrowed slice of reflect.StructField
// of all of the type's struct fields.  Do not mutate the elements of
// the slice.  If the type is an array or slice, then this returns a
// single-element slice whose Type is the type of the element of the
// array or slice.
func (t *Type) ReflectStructFields() []reflect.StructField {
	ups := t.unsafePointers()
	numFields := t.numFields()
	return unsafe.Slice(
		(*reflect.StructField)(unsafe.Add(unsafe.Pointer(ups), (2+numFields*typeUnsafePointers)*int(unsafe.Sizeof(unsafe.Pointer(nil))))),
		numFields,
	)
}

func (t *Type) ReflectType() reflect.Type {
	return t.reflectType
}

// Size of the type
func (t *Type) Size() int { return t.size }

// UnsafeFieldValue will return an interface{} of the value of the
// field of struct at the given field index.  Note that the backing
// memory of the fieldValue is the actual struct's field, so it is
// unsafe to preserve this interface{} unless the struc is immutable.
func (t *Type) UnsafeFieldValue(struc interface{}, fieldIndex int) (fieldValue interface{}) {
	return t.field(struc, fieldIndex, (*Type).fieldABITypes)
}

// field is the implementation of FieldPointer and UnsafeFieldValue.
func (t *Type) field(
	struc interface{},
	fieldIndex int,
	fieldTypesSelector func(*Type) []unsafe.Pointer,
) (field interface{}) {
	strucID := InterfaceDataOf(unsafe.Pointer(&struc))
	if strucID.Type != *t.abiType() && strucID.Type != *t.abiPtrType() {
		panic(fmt.Sprintf(
			"cannot get field of %T with %v",
			struc, t.reflectType.Name(),
		))
	}
	base := strucID.Data
	if t.ReflectType().Kind() == reflect.Slice {
		base = SliceDataOf(strucID.Data).Data
	}
	fID := InterfaceDataOf(unsafe.Pointer(&field))
	atomic.StorePointer(&fID.Type, fieldTypesSelector(t)[fieldIndex*t.fieldIndexMultiplier()])
	atomic.StorePointer(
		&fID.Data,
		unsafe.Add(base, t.fieldOffset(fieldIndex)),
	)
	return
}

func (t *Type) abiType() *unsafe.Pointer { return t.unsafePointers() }

func (t *Type) abiPtrType() *unsafe.Pointer {
	return (*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(t.unsafePointers()), unsafe.Sizeof(unsafe.Pointer(nil))))
}

func (t *Type) fieldABITypes() []unsafe.Pointer {
	return unsafe.Slice(t.unsafePointers(), 2+t.numFields())[2:]
}

func (t *Type) fieldIndexMultiplier() int {
	return int((t.uintData[0] >> udHasLenBit) & 1)
}

func (t *Type) fieldOffset(i int) int {
	m := t.fieldIndexMultiplier()
	return int(t.ReflectStructFields()[i*m].Offset) + (i*int((^m)&1))*t.fieldUSRTypes()[0].size
}

func (t *Type) fieldUSRTypes() []*Type {
	ptrs := (**Type)(unsafe.Pointer(t.unsafePointers()))
	return unsafe.Slice(ptrs, 2+(usrPtrType*t.numFields()))[2+(usrType*t.numFields()):]
}

func (t *Type) numFields() int { return int(t.uintData[0] >> uintDataBits) }

func (t *Type) ptrToFieldABITypes() []unsafe.Pointer {
	return unsafe.Slice(t.unsafePointers(), 2+(usrType*t.numFields()))[2+(abiPtrType*t.numFields()):]
}

func (t *Type) ptrToFieldUSRTypes() []*Type {
	ptrs := (**Type)(unsafe.Pointer(t.unsafePointers()))
	return unsafe.Slice(ptrs, 2+(typeUnsafePointers*t.numFields()))[2+(usrPtrType*t.numFields()):]
}

func (t *Type) unsafePointers() *unsafe.Pointer {
	return &((*typeWithPtrs)(unsafe.Pointer(t))).pointers[0]
}

// AppendUnsafeFieldValues appends the passed-in structs' field values to
// slice.  The values alias the struct's actual fields, which saves on
// allocations, but means these values are unsafe to use except
// temporarily.
func AppendUnsafeFieldValues(slice []interface{}, structs ...interface{}) []interface{} {
	return appendFields(
		slice,
		(*Type).fieldABITypes,
		structs...,
	)
}

// AppendFieldPointers appends addresses of the passed-in structs'
// fields to slice.
func AppendFieldPointers(slice []interface{}, structs ...interface{}) []interface{} {
	return appendFields(
		slice,
		(*Type).ptrToFieldABITypes,
		structs...,
	)
}

// appendFields is the implementation of AppendUnsafeFieldValues and
// AppendFieldPointers.
func appendFields(
	slice []interface{},
	fieldsSelector func(*Type) []unsafe.Pointer,
	structPointers ...interface{},
) []interface{} {
	var v interface{}
	id := InterfaceDataOf(unsafe.Pointer(&v))
	for _, st := range structPointers {
		structType := TypeOf(st)
		structKind := structType.ReflectType().Kind()
		if structKind == reflect.Pointer {
			structType = structType.fieldUSRTypes()[0]
			structKind = structType.ReflectType().Kind()
			InterfaceDataOf(unsafe.Pointer(&st)).Type = *structType.abiType()
		}
		types := fieldsSelector(structType)
		base := InterfaceDataOf(unsafe.Pointer(&st)).Data
		if structKind == reflect.Slice {
			base = SliceDataOf(base).Data
		}
		m := structType.fieldIndexMultiplier()
		for i, length := 0, Len(st); i < length; i++ {
			id.Data = unsafe.Add(base, structType.fieldOffset(i))
			id.Type = types[i*m]
			slice = append(slice, v)
		}
	}
	return slice
}

// InterfaceData allows you to access the implementation of interface
// values' type and data pointers.  This will break if Go ever changes
// the implementation of interfaces.
type InterfaceData struct {
	Type unsafe.Pointer
	Data unsafe.Pointer
}

// InterfaceDataOf must always be passed a pointer to an interface.
func InterfaceDataOf(v unsafe.Pointer) *InterfaceData {
	return (*InterfaceData)(v)
}

func Len(v interface{}) int {
	t := TypeOf(v)
	if t.uintData[0]&udHasLenMask == udHasLenMask {
		return t.numFields()
	}
	if t.ReflectType().Kind() == reflect.Slice {
		return SliceDataOf(InterfaceDataOf(unsafe.Pointer(&v)).Data).Len
	}
	// TODO: Should this panic?
	return 0
}

type SliceData struct {
	Data unsafe.Pointer
	Len  int
	Cap  int
}

func SliceDataOf(v unsafe.Pointer) *SliceData {
	return (*SliceData)(v)
}
