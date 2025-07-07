// Package unsafereflect provides unsafe access to fields within
// `struct`s that have been "boxed" into `interface{}` values.
//
// This package is used by the `github.com/skillian/sqlstream` package
// to deal with this problem:
//
//	type myStruct {
//		MyInt int64,
//		MyString sql.NullString
//	}
//
//	/* ... */
//
//	db := (*sql.DB)(nil)	// initialized somewhere else
//	var myStructs []myStruct = getMyStructsFromSomewhereElse()
//	db.ExecContext(
//		ctx,
//		`INSERT INTO (
//		"MyInt",		"MyString"		) VALUES (
//		?,			?			);`,
//		myStructs[0].MyInt,	myStructs[0].MyString,
//	)
//
// Here, `db.ExecContext`'s `args ...interface{}` parameter values are
// individually captured into `interface{}` values: which involves
// allocating and copying them separately (so `myStructs[0].MyInt`
// and `myStructs[0].MyString` are each captured and allocated
// separately, and in the more likely scenario that this sort of
// function is executed in a loop, then `myStructs[1].MyInt` and
// `myStructs[1].MyString` would also be captured independently, etc.).
//
// The `unsafereflect` package is intended to ***temporarily*** treat
// `struct` field values as independent `interface{}` values for the
// purpose of scanning their values.
//
// So you could instead do this:
//
//	var myStructs []myStruct = getMyStructsFromSomewhereElse()
//	t := unsafereflect.TypeOf(myStructs[0])
//	for i := myStructs {
//		myInt := t.FieldValue(myStructs[i], 0)
//		myString := t.FieldValue(myStructs[i], 1)
//		db.ExecContext(
//			ctx,
//			`INSERT INTO (
//			"MyInt",	"MyString"	) VALUES (
//			?,		?		);`,
//			myInt,		myString,
//		)
//	}
//
// where `myInt` and `myString` reference `myStructs[i]`'s underlying
// memory, instead of allocating separate `interface{}` values.
package unsafereflect
