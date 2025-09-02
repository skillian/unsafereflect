package unsafereflect

import (
	"fmt"
	"math/bits"
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
			t.uintData[0] = (uint(t.ReflectType().Len()) << uintDataBits) | udIsArrayMask
		})
		fallthrough
	case reflect.Slice, reflect.Pointer:
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
	numTypes := t.numTypes()
	if len(fieldABITypes) != numTypes {
		panic("len(fieldABITypes) != t.numTypes")
	}
	if len(ptrToFieldABITypes) != numTypes {
		panic("len(ptrToFieldABITypes) != t.numTypes")
	}
	if len(fieldUSRTypes) != numTypes {
		panic("len(fieldUSRTypes) != t.numTypes")
	}
	if len(ptrToFieldUSRTypes) != numTypes {
		panic("len(ptrToFieldUSRTypes) != t.numTypes")
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
	if t.ReflectType().Kind() == reflect.Pointer {
		t = t.FieldType(0)
	}
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
	numTypes := t.numTypes()
	return unsafe.Slice(t.unsafePointers(), 2+numTypes)[2:]
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
	numTypes := t.numTypes()
	return unsafe.Slice(ptrs, 2+(usrPtrType*numTypes))[2+(usrType*numTypes):]
}

func (t *Type) numFields() int { return int(t.uintData[0] >> uintDataBits) }
func (t *Type) numTypes() int  { return (t.numFields()-1)*t.fieldIndexMultiplier() + 1 }

func (t *Type) ptrToFieldABITypes() []unsafe.Pointer {
	numTypes := t.numTypes()
	return unsafe.Slice(t.unsafePointers(), 2+(usrType*numTypes))[2+(abiPtrType*numTypes):]
}

func (t *Type) ptrToFieldUSRTypes() []*Type {
	ptrs := (**Type)(unsafe.Pointer(t.unsafePointers()))
	numTypes := t.numTypes()
	return unsafe.Slice(ptrs, 2+(typeUnsafePointers*numTypes))[2+(usrPtrType*numTypes):]
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

// Copy is like
//
//	func Copy[T any](dest, src *T) int {
//		*dest = *src
//		return int(unsafe.Sizeof(*dest))
//	}
//
// # Except that it works with non-generic parameters
//
// The way this works is by copying the bytes from src into dest.
// If src contains a noCopy type, you will introduce undefined behavior
// into your code.
func Copy(dest, src interface{}) (bytesCopied int) {
	destType, destMemory := typeAndMemoryOf(dest)
	srcType, srcMemory := typeAndMemoryOf(src)
	if destType != srcType {
		panic(fmt.Errorf(
			"%[1]v (type: %[1]T) is incompatible with "+
				"%[1]v (type: %[2]T)",
			dest, src,
		))
	}
	return copy(destMemory, srcMemory)
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
	if t.uintData[0]&(udIsArrayMask|udHasLenMask) != 0 {
		return t.numFields()
	}
	if t.ReflectType().Kind() == reflect.Slice {
		return SliceDataOf(InterfaceDataOf(unsafe.Pointer(&v)).Data).Len
	}
	// TODO: Should this panic?
	return 0
}

var (
	atomicLoadInt, atomicStoreInt = func() (func(*int) int, func(*int, int)) {
		switch bits.UintSize {
		case 64:
			return func(i *int) int {
					return int(atomic.LoadInt64((*int64)(unsafe.Pointer(i))))
				}, func(i1 *int, i2 int) {
					atomic.StoreInt64((*int64)(unsafe.Pointer(i1)), int64(i2))
				}
		case 32:
			return func(i *int) int {
					return int(atomic.LoadInt32((*int32)(unsafe.Pointer(i))))
				}, func(i1 *int, i2 int) {
					atomic.StoreInt32((*int32)(unsafe.Pointer(i1)), int32(i2))
				}
		default:
			panic(fmt.Errorf("unknown bit size: %d", bits.UintSize))
		}
	}()
	zeroData = func() unsafe.Pointer {
		var v interface{} = []int{0}
		id := InterfaceDataOf(unsafe.Pointer(&v))
		sd := SliceDataOf(id.Data)
		return sd.Data
	}()
)

// MemoryOf exposes the bytes of a value as a byte slice.  Pass in
// a pointer to the memory you want.
func MemoryOf(v interface{}) (memory []byte) {
	_, memory = typeAndMemoryOf(v)
	return
}

func typeAndMemoryOf(v interface{}) (t *Type, memory []byte) {
	// this function uses atomic operations because it is my
	// understanding that the state of interfaces must be valid
	// at all points in time in case the runtime preempts the
	// goroutine.
	t = TypeOf(v)
	id := InterfaceDataOf(unsafe.Pointer(&v))
	// if t.ReflectType().Kind() == reflect.Pointer {
	// 	data := id.Data
	// 	atomic.StorePointer(&id.Data, zeroData)
	// 	atomic.StorePointer(&id.Type, t.fieldABITypes()[0])
	// 	atomic.StorePointer(&id.Data, *(*unsafe.Pointer)(data))
	// 	t = t.fieldUSRTypes()[0]
	// }
	sd := SliceDataOf(unsafe.Pointer(&memory))
	atomic.StorePointer(&sd.Data, id.Data)
	size := t.fieldUSRTypes()[0].Size()
	atomicStoreInt(&sd.Cap, size)
	atomicStoreInt(&sd.Len, size)
	return
}

type SliceData struct {
	Data unsafe.Pointer
	Len  int
	Cap  int
}

func SliceDataOf(v unsafe.Pointer) *SliceData {
	return (*SliceData)(v)
}

type StringData struct {
	Data unsafe.Pointer
	Len  int
}

func StringDataOf(s *string) *StringData {
	return (*StringData)(unsafe.Pointer(s))
}
