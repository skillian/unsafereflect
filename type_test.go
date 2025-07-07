package unsafereflect_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/skillian/unsafereflect"
)

func TestType(t *testing.T) {
	type T struct {
		Bool    bool
		Float32 float32
		Float64 float64
		Int     int
		Int8    int8
		Int16   int16
		Int32   int32
		Int64   int64
		Uint    uint
		Uint8   uint8
		Uint16  uint16
		Uint32  uint32
		Uint64  uint64
		String  string
		Pointer *T
	}
	for i := 0; i < 2; i++ {
		v := &T{
			Bool:    false,
			Float32: 1,
			Float64: 2,
			Int:     3,
			Int8:    4,
			Int16:   5,
			Int32:   6,
			Int64:   7,
			Uint:    8,
			Uint8:   9,
			Uint16:  10,
			Uint32:  11,
			Uint64:  12,
			String:  "Hello",
		}
		v.Pointer = v
		vType := reflect.TypeOf(v).Elem()
		numFields := vType.NumField()
		vs := unsafereflect.AppendUnsafeFieldValues(make([]interface{}, 0, numFields), v)
		if len(vs) != numFields {
			t.Fatalf(
				"len(vs) = %d; expected %d",
				len(vs), numFields,
			)
		}
		v2 := T{}
		typeAssert(t, &v2.Bool, vs[0], "v.Bool")
		typeAssert(t, &v2.Float32, vs[1], "v.Float32")
		typeAssert(t, &v2.Float64, vs[2], "v.Float64")
		typeAssert(t, &v2.Int, vs[3], "v.Int")
		typeAssert(t, &v2.Int8, vs[4], "v.Int8")
		typeAssert(t, &v2.Int16, vs[5], "v.Int16")
		typeAssert(t, &v2.Int32, vs[6], "v.Int32")
		typeAssert(t, &v2.Int64, vs[7], "v.Int64")
		typeAssert(t, &v2.Uint, vs[8], "v.Uint")
		typeAssert(t, &v2.Uint8, vs[9], "v.Uint8")
		typeAssert(t, &v2.Uint16, vs[10], "v.Uint16")
		typeAssert(t, &v2.Uint32, vs[11], "v.Uint32")
		typeAssert(t, &v2.Uint64, vs[12], "v.Uint64")
		typeAssert(t, &v2.String, vs[13], "v.String")
		typeAssert(t, &v2.Pointer, vs[14], "v.Pointer")
		vs = unsafereflect.AppendFieldPointers(vs[:0], v)
		dynamicType := func() reflect.Type {
			fs := make([]reflect.StructField, len(vs))
			for i := 0; i < numFields; i++ {
				fs[i] = vType.Field(i)
				fs[i].Type = reflect.TypeOf(vs[i])
			}
			return reflect.StructOf(fs)
		}()
		pv := reflect.New(dynamicType)
		pev := pv.Elem()
		for i, p := range vs {
			typeAssert(t, pev.Field(i).Addr().Interface(), p, "p.%s", dynamicType.Field(i).Name)
		}
		if reflect.ValueOf(vs[0]).Elem().UnsafeAddr() != reflect.ValueOf(v).Elem().UnsafeAddr() {
			t.Fatalf("expected address of first field == address of v")
		}
	}
}

func typeAssert(t *testing.T, dest, src interface{}, srcDesc string, srcDescArgs ...interface{}) {
	t.Helper()
	destValue := reflect.ValueOf(dest)
	destType := destValue.Type().Elem()
	sourceValue := reflect.ValueOf(src)
	sourceType := sourceValue.Type()
	if sourceType != destType {
		sb := strings.Builder{}
		if _, err := fmt.Fprintf(&sb, srcDesc, srcDescArgs...); err != nil {
			panic(err)
		}
		if _, err := fmt.Fprintf(
			&sb, " is %v, not %v",
			sourceType.Name(), destType.Name(),
		); err != nil {
			panic(err)
		}
		t.Fatal(sb.String())
	}
	destValue.Elem().Set(sourceValue)
}

func TestDemonstrateUnsafety(t *testing.T) {
	type S struct {
		I int
	}
	v := &S{I: 123}
	vs := unsafereflect.AppendUnsafeFieldValues(make([]interface{}, 0, 1), v)
	key := vs[0]
	// key's underlying data is &v.I, but its type is int (not *int)
	// so we can change its value, i.e. "spooky action at a distance"
	m := map[interface{}]string{
		key: "Hello, World!",
	}
	// now we're going to corrupt the map:
	v.I = 456
	// Normally, you'd expect that this would retrieve what we
	// inserted before, but we rewrote key's underlying data so
	// now the key in the map is no longer 123.
	t.Log(m[123])
	// what's interesting is this probably doesn't work, either:
	t.Log(m[456])
	// because the key was hashed for its original value (123)
	// when it was inserted, now that it has been rewritten to
	// 456, you can't find it.
}

func TestExampleUnsafeUsage(t *testing.T) {
	type S struct {
		Int int
		Str string
	}
	s := S{Int: 123, Str: ""}
	var v interface{} = s
	if v.(S).Int != 123 {
		t.Fatal("expected v.(S).Int to be", 123, "not", v.(S).Int)
	}
	s.Int = 456
	if v.(S).Int != 123 {
		t.Fatal("assignment to s should not have affected v")
	}
	//
	// Everything above should make sense: `s` is of type `S` and
	// `v` is a _copy_ of `s` as it was after `s` was first
	// instantiated.
	//
	// Subsequently changing `s.Int`` to `456` should not affect
	// `v`.
	//
	p := unsafereflect.TypeOf(v).FieldPointer(v, 0)
	if pi, ok := p.(*int); !ok {
		t.Fatalf("expected p to be %T, not %T", pi, p)
	} else {
		*pi = 456
	}
	if v.(S).Int != 456 {
		t.Fatal("Something changed in Go's internals!")
	}
	if s.Int == 123 {
		t.Log("original s was affected")
	}
	//
	// Assuming the previous two `if`s above passed their tests,
	// `v.(S).Int == 456` demonstrates the `unsafereflect` package:
	// The `(*unsafereflect.Type).FieldPointer` method accesses
	// the `Int` field of the `S` wrapped by `v` and allows you to
	// write into it.
	//
	// You should know what you're doing if you intend on using
	// this functionality.  If you know what you're doing, then
	// this package is intended to act as a helper.
	//
}

func TestUnsafeSlice(t *testing.T) {
	sl := make([]int, 10)
	for i := range sl {
		sl[i] = i
	}
	psl := unsafereflect.AppendFieldPointers(make([]interface{}, 0, len(sl)), sl)
	for i := range sl {
		if &sl[i] != psl[i] {
			t.Fatalf(
				"Slice element ptr %[1]d: %[2]v (%[2]p) != %[3]v (%[3]p)",
				i, &sl[i], psl[i],
			)
		}
	}
}
