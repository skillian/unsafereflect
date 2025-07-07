package unsafereflect

import "runtime"

func CallerName(skip int, optionalPreallocatedUintptrSlice *[]uintptr) string {
	if optionalPreallocatedUintptrSlice == nil {
		optionalPreallocatedUintptrSlice = new([]uintptr)
		*optionalPreallocatedUintptrSlice = make([]uintptr, 8)
	}
	callers := 0
	for {
		callers = runtime.Callers(
			skip+2, /* 1 for the caller of callers */
			*optionalPreallocatedUintptrSlice,
		)
		if callers < len(*optionalPreallocatedUintptrSlice) {
			*optionalPreallocatedUintptrSlice = (*optionalPreallocatedUintptrSlice)[:callers]
			break
		}
		*optionalPreallocatedUintptrSlice = append(
			*optionalPreallocatedUintptrSlice,
			0,
		)
		*optionalPreallocatedUintptrSlice = (*optionalPreallocatedUintptrSlice)[:cap(*optionalPreallocatedUintptrSlice)]
	}
	frames := runtime.CallersFrames(*optionalPreallocatedUintptrSlice)
	for {
		fr, ok := frames.Next()
		if !ok {
			break
		}
		if fr.Function == "" {
			continue
		}
		return fr.Function
	}
	return "<unknown>"
}
