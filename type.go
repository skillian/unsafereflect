package unsafereflect

import (
	"fmt"
	"math/bits"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"
)

const (
	// Checked indicates if the unsafereflect module is compiled
	// to perform runtime checks.  Set this to false before
	// compilation to remove runtime checks.
	Checked = true
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
	if rt == nil {
		panic("cannot get type of nil")
	}
	key := interface{}(rt)
	if v, loaded := types.Load(key); loaded {
		return v.(*Type)
	}
	defer func() {
		var err error
		switch v := recover().(type) {
		case error:
			err = v
		default:
			if v == nil {
				return
			}
			err = fmt.Errorf("%#v", v)
		}
		panic(fmt.Errorf(
			"unsafereflect.Type of %v: %w",
			rt, err,
		))
	}()
	numFields := 0
	initFuncs := make([]func(t *Type), 0, 2)
	handleArraySliceAndPointer := func(t *Type) {
		elemType := TypeFromReflectType(t.ReflectType().Elem())
		sfs := t.ReflectStructFields()
		sfs[0] = reflect.StructField{
			Name: "Elem",
			Type: elemType.ReflectType(),
		}
		t.fieldABITypes()[0] = *elemType.abiType()
		t.ptrToFieldABITypes()[0] = *elemType.abiPtrType()
		t.fieldUSRTypes()[0] = elemType
		t.ptrToFieldUSRTypes()[0] = TypeFromReflectType(reflect.PointerTo(elemType.ReflectType()))
	}
	kind := rt.Kind()
	switch kind {
	case reflect.Array:
		numFields++
		initFuncs = append(initFuncs, func(t *Type) {
			t.uintData[0] = (uint(t.ReflectType().Len()) << uintDataBits) | udIsArrayMask
		})
		initFuncs = append(initFuncs, handleArraySliceAndPointer)
	case reflect.Slice, reflect.Pointer:
		numFields++
		initFuncs = append(initFuncs, func(t *Type) {
			t.uintData[0] |= udIsArrayMask | udHasLenMask
		})
		initFuncs = append(initFuncs, handleArraySliceAndPointer)
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
			numTypes := t.numTypes()
			reflectStructFields := t.ReflectStructFields()
			for i := 0; i < numTypes; i++ {
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
		id := InterfaceDataOf(&v)
		*t.abiPtrType() = id.Type
		v = rv.Elem().Interface()
		*t.abiType() = id.Type
	}
	// add the type before initializing in case of recursive types
	if v, loaded := types.LoadOrStore(key, t); loaded {
		return v.(*Type)
	}
	for _, initFunc := range initFuncs {
		initFunc(t)
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
	tReflectStructFields := t.ReflectStructFields()
	tvReflectStructFields := tv.Elem().Field(2).Slice(0, t.numTypes()).Interface().([]reflect.StructField)
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
func (t *Type) Len() int { return int(t.uintData[0] >> uintDataBits) }

// ReflectStructFields returns a borrowed slice of reflect.StructField
// of all of the type's struct fields.  Do not mutate the elements of
// the slice.  If the type is an array or slice, then this returns a
// single-element slice whose Type is the type of the element of the
// array or slice.
func (t *Type) ReflectStructFields() []reflect.StructField {
	ups := t.unsafePointers()
	numTypes := t.numTypes()
	return unsafe.Slice(
		(*reflect.StructField)(unsafe.Add(unsafe.Pointer(ups), (2+numTypes*typeUnsafePointers)*int(unsafe.Sizeof(unsafe.Pointer(nil))))),
		numTypes,
	)
}

// ReflecType gets the Go reflect.Type that this unsafereflect.Type
// wraps
func (t *Type) ReflectType() reflect.Type {
	return t.reflectType
}

// Size of the type
func (t *Type) Size() int { return t.size }

// UnsafeDeref returns the same underlying memory as was passed into
// ptr but its type is changed from *T to T.
func (t *Type) UnsafeDeref(ptr any) any {
	id := InterfaceDataOf(&ptr)
	if id.Type == *t.abiPtrType() {
		atomic.StorePointer(&id.Type, *t.abiType())
	}
	return ptr
}

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
	strucID := InterfaceDataOf(&struc)
	if strucID.Type != *t.abiType() && strucID.Type != *t.abiPtrType() {
		panic(fmt.Sprintf(
			"cannot get field of %T with %v",
			struc, t.reflectType.Name(),
		))
	}
	base := strucID.Data
	if t.ReflectType().Kind() == reflect.Slice {
		base = SliceDataOf((*[]any)(strucID.Data)).Data
	}
	fID := InterfaceDataOf(&field)
	atomic.StorePointer(&fID.Type, fieldTypesSelector(t)[fieldIndex&^t.notFieldLengthMask()])
	atomic.StorePointer(
		&fID.Data,
		unsafe.Add(base, t.fieldOffset(fieldIndex)),
	)
	return
}

// ABIType gets the Go ABI type pointer
func (t *Type) ABIType() unsafe.Pointer { return *t.abiType() }

func (t *Type) abiType() *unsafe.Pointer { return t.unsafePointers() }

func (t *Type) abiPtrType() *unsafe.Pointer {
	return (*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(t.unsafePointers()), unsafe.Sizeof(unsafe.Pointer(nil))))
}

func (t *Type) fieldABITypes() []unsafe.Pointer {
	numTypes := t.numTypes()
	return unsafe.Slice(t.unsafePointers(), 2+numTypes)[2:]
}

// notFieldLengthMask is:
//
//	-1 for array and slice types or
//	0 for struct types.
//
// For arrays and slices, there is only one field
// defined in the Type, so when you access the field information for
// index 100, it accesses fieldInfo[100&^notFieldLengthMask] = fieldInfo[0]
func (t *Type) notFieldLengthMask() int {
	return int((t.uintData[0]&udHasLenMask)>>udHasLenBit) - 1
}
func (t *Type) notArrayLengthMask() int {
	return int((t.uintData[0]&udIsArrayMask)>>udIsArrayBit) - 1
}

func (t *Type) fieldOffset(i int) int {
	fieldLengthMask := ^t.notFieldLengthMask()
	arrayLengthMask := ^t.notArrayLengthMask()
	sliceLengthMask := fieldLengthMask & arrayLengthMask
	return int(t.ReflectStructFields()[i&(fieldLengthMask^sliceLengthMask)].Offset) +
		t.fieldUSRTypes()[0].size*(i&(arrayLengthMask|sliceLengthMask))
}

func (t *Type) fieldUSRTypes() []*Type {
	ptrs := (**Type)(unsafe.Pointer(t.unsafePointers()))
	numTypes := t.numTypes()
	return unsafe.Slice(ptrs, 2+(usrPtrType*numTypes))[2+(usrType*numTypes):]
}

// numTypes returns 1 for arrays and slices (the element type) or
// the number of fields.
func (t *Type) numTypes() int {
	length := t.Len()
	fieldLengthMask := ^t.notFieldLengthMask()
	arrayLengthMask := ^t.notArrayLengthMask()
	pointerIndexMask := fieldLengthMask & arrayLengthMask
	return (length & (fieldLengthMask ^ pointerIndexMask)) +
		(1 & (arrayLengthMask | pointerIndexMask))
}

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
	id := InterfaceDataOf(&v)
	for _, st := range structPointers {
		structType := TypeOf(st)
		structKind := structType.ReflectType().Kind()
		if structKind == reflect.Pointer {
			structType = structType.fieldUSRTypes()[0]
			structKind = structType.ReflectType().Kind()
			InterfaceDataOf(&st).Type = *structType.abiType()
		}
		types := fieldsSelector(structType)
		base := InterfaceDataOf(&st).Data
		if structKind == reflect.Slice {
			base = SliceDataOf((*[]any)(base)).Data
		}
		m := (^structType.notFieldLengthMask()) & structType.notArrayLengthMask()
		for i, length := 0, Len(st); i < length; i++ {
			id.Data = unsafe.Add(base, structType.fieldOffset(i))
			id.Type = types[i&m]
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
//
// Note that this also doesn't put memory barriers around writing
// pointers. Use at your own risk.
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
func InterfaceDataOf[T any](v *T) *InterfaceData {
	if Checked {
		t := reflect.TypeFor[T]()
		if t.Kind() != reflect.Interface {
			panic(fmt.Errorf(
				// TODO: Is there a better way to get the type
				// representation in case of non-defined types?
				"InterfaceDataOf can only get InterfaceData of interfaces, not %T",
				reflect.New(t).Elem(),
			))
		}
	}
	return (*InterfaceData)(unsafe.Pointer(v))
}

// Len gets the length of a slice or array or the number of fields
// of a struct.
func Len(v interface{}) int {
	t := TypeOf(v)
	ud := t.uintData[0]
	a := (ud & udIsArrayMask) >> udIsArrayBit
	b := (ud & udHasLenMask) >> udHasLenBit
	if (a ^ b) != 0 {
		return t.Len()
	}
	if t.ReflectType().Kind() == reflect.Slice {
		return SliceDataOf((*[]any)(InterfaceDataOf(&v).Data)).Len
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
)

// MakeAt is like reflect.NewAt, but it returns the value, not a pointer
// to the value.
func MakeAt(t *Type, p unsafe.Pointer) (v any) {
	id := InterfaceDataOf(&v)
	atomic.StorePointer(&id.Type, *t.abiType())
	atomic.StorePointer(&id.Data, p)
	return
}

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
	id := InterfaceDataOf(&v)
	// if t.ReflectType().Kind() == reflect.Pointer {
	// 	data := id.Data
	// 	atomic.StorePointer(&id.Data, zeroData)
	// 	atomic.StorePointer(&id.Type, t.fieldABITypes()[0])
	// 	atomic.StorePointer(&id.Data, *(*unsafe.Pointer)(data))
	// 	t = t.fieldUSRTypes()[0]
	// }
	sd := SliceDataOf(&memory)
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

// SliceDataOf exposes the internal fields of a slice.  Manipulating
// those fields manipulates the slice itself.
func SliceDataOf[TSlice []T, T any](v *TSlice) *SliceData {
	return (*SliceData)(unsafe.Pointer(v))
}

type StringData struct {
	Data unsafe.Pointer
	Len  int
}

// StringDataOf exposes the internal fields of a string.  Manipulating
// those fields manipulates the passed-in string value.
func StringDataOf(s *string) *StringData {
	return (*StringData)(unsafe.Pointer(s))
}

var isNilFuncs = struct {
	pointer func(p unsafe.Pointer) bool
	slice   func(d SliceData) bool
	iface   func(d InterfaceData) bool
}{
	pointer: func(p unsafe.Pointer) bool {
		return p == nil
	},
	slice: func(d SliceData) bool {
		return d == SliceData{}
	},
	iface: func(d InterfaceData) bool {
		return d == InterfaceData{}
	},
}

// IsNilFunc returns a function that can check if its value is nil.
//
// Ideally, this function implementation would just be:
//
//	func IsNilFunc[T any]() func(v T) bool {
//		return func(v T) bool {
//			return v == nil
//		}
//	}
//
// But that implementation produces this compiler error:
//
//	invalid operation: v == nil (mismatched types T and untyped nil)
//
// So IsNilFunc creates an IsNil function with the unsafe & reflect
// packages.
func IsNilFunc[T any]() func(v T) bool {
	switch reflect.TypeFor[T]().Kind() {
	case
		reflect.Chan,
		reflect.Func,
		reflect.Map,
		reflect.Pointer,
		reflect.UnsafePointer:
		// Compile-time check that these types are all the
		// size of pointers:
		_ = func() (x [1]struct{}) {
			const ptrSize = unsafe.Sizeof(unsafe.Pointer(nil))
			_ = x[int(ptrSize-unsafe.Sizeof((chan int)(nil)))]
			_ = x[int(ptrSize-unsafe.Sizeof((func())(nil)))]
			_ = x[int(ptrSize-unsafe.Sizeof((map[int]struct{})(nil)))]
			//_ = x[int(ptrSize-unsafe.Sizeof(byte(0)))]
			return
		}
		return *((*func(T) bool)(unsafe.Pointer(&isNilFuncs.pointer)))
	case reflect.Slice:
		return *((*func(T) bool)(unsafe.Pointer(&isNilFuncs.slice)))
	case reflect.Interface:
		return *((*func(T) bool)(unsafe.Pointer(&isNilFuncs.iface)))
	}
	return func(v T) bool { return false }
}
