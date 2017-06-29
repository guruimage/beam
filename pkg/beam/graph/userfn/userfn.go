package userfn

import (
	"fmt"
	"reflect"
	"runtime"

	"github.com/apache/beam/sdks/go/pkg/beam/graph/typex"
	"github.com/apache/beam/sdks/go/pkg/beam/util/reflectx"
)

// TODO(herohde) 4/14/2017: various side input forms + aggregators/counters.
// Note that we can't tell the difference between K, V and V, S before binding.

// FnParamKind represents the kinds of parameters a user function may take.
type FnParamKind int

const (
	// FnIllegal is an illegal function input parameter type.
	FnIllegal FnParamKind = 0x0
	// FnContext marks a function input parameter of type context.Context.
	FnContext FnParamKind = 0x1
	// FnEventTime indicates a function input parameter of type typex.EventTime.
	FnEventTime FnParamKind = 0x2
	// FnValue indicates a function input parameter of an ordinary Go type.
	FnValue FnParamKind = 0x4
	// FnIter indicates a function input parameter that is an iterator.
	// Examples of iterators:
	//       "func (*int) bool"
	//       "func (*string, *T) bool"
	// If there are 2 parameters, a KV input is implied.
	FnIter FnParamKind = 0x08
	// FnReIter indicates a function input parameter that is a reiterable
	// iterator.
	// The function signature is a function returning a function matching
	// the iterator signature.
	//   "func() func (*int) bool"
	//   "func() func (*string, *T) bool"
	// are reiterable versions of the FnIter examples.
	FnReIter FnParamKind = 0x10
	// FnEmit indicates a function input parameter that is an emitter.
	// Examples of emitters:
	//       "func (int)"
	//       "func (string, T)"
	//       "func (EventTime, int)"
	//       "func (EventTime, string, T)"
	// If there are 2 regular parameters, a KV output is implied. An optional
	// EventTime is allowed as well. Emitters cannot fail.
	FnEmit FnParamKind = 0x20
	// FnType indicates a function input parameter that is a type for a coder. It
	// is only valid for coders.
	FnType FnParamKind = 0x40
)

func (k FnParamKind) String() string {
	switch k {
	case FnContext:
		return "Context"
	case FnEventTime:
		return "EventTime"
	case FnValue:
		return "Value"
	case FnIter:
		return "Iter"
	case FnReIter:
		return "ReIter"
	case FnEmit:
		return "Emit"
	case FnType:
		return "Type"
	default:
		return fmt.Sprintf("%v", int(k))
	}
}

// FnParam captures the kind and type of a single user function parameter.
type FnParam struct {
	Kind FnParamKind
	T    reflect.Type
}

// ReturnKind represents the kinds of return values a user function may provide.
type ReturnKind int

// The supported types of ReturnKind.
const (
	RetIllegal   ReturnKind = 0x0
	RetEventTime ReturnKind = 0x1
	RetValue     ReturnKind = 0x2
	RetError     ReturnKind = 0x4
)

func (k ReturnKind) String() string {
	switch k {
	case RetError:
		return "Error"
	case RetEventTime:
		return "EventTime"
	case RetValue:
		return "Value"
	default:
		return fmt.Sprintf("%v", int(k))
	}
}

// ReturnParam captures the kind and type of a single user function return value.
type ReturnParam struct {
	Kind ReturnKind
	T    reflect.Type
}

// UserFn is the reflected user function or method, preprocessed. This wrapper
// is useful both at graph construction time as well as execution time.
type UserFn struct {
	Name string // robust name
	Fn   reflect.Value

	Param []FnParam
	Ret   []ReturnParam
}

// Context returns (index, true) iff the function expects a context.Context.
// The context should be the first parameter by convention.
func (u *UserFn) Context() (pos int, exists bool) {
	for i, p := range u.Param {
		if p.Kind == FnContext {
			return i, true
		}
	}
	return -1, false
}

// Type returns (index, true) iff the function expects a reflect.FullType.
func (u *UserFn) Type() (pos int, exists bool) {
	for i, p := range u.Param {
		if p.Kind == FnType {
			return i, true
		}
	}
	return -1, false
}

// EventTime returns (index, true) iff the function expects an event timestamp.
func (u *UserFn) EventTime() (pos int, exists bool) {
	for i, p := range u.Param {
		if p.Kind == FnEventTime {
			return i, true
		}
	}
	return -1, false
}

// Error returns (index, true) iff the function returns an error.
func (u *UserFn) Error() (pos int, exists bool) {
	for i, p := range u.Ret {
		if p.Kind == RetError {
			return i, true
		}
	}
	return -1, false
}

// OutEventTime returns (index, true) iff the function returns an event timestamp.
func (u *UserFn) OutEventTime() (pos int, exists bool) {
	for i, p := range u.Ret {
		if p.Kind == RetEventTime {
			return i, true
		}
	}
	return -1, false
}

// Params returns the parameter indices that matches the given mask.
func (u *UserFn) Params(mask FnParamKind) []int {
	var ret []int
	for i, p := range u.Param {
		if (p.Kind & mask) != 0 {
			ret = append(ret, i)
		}
	}
	return ret
}

// Returns returns the return indices that matches the given mask.
func (u *UserFn) Returns(mask ReturnKind) []int {
	var ret []int
	for i, p := range u.Ret {
		if (p.Kind & mask) != 0 {
			ret = append(ret, i)
		}
	}
	return ret
}

func (u *UserFn) String() string {
	return fmt.Sprintf("%+v", *u)
}

// New returns a UserFn from a function, if valid.
func New(dofn interface{}) (*UserFn, error) {
	fn := reflect.ValueOf(dofn)
	if fn.Kind() != reflect.Func {
		return nil, fmt.Errorf("not a function or method: %v", fn.Kind())
	}

	// TODO(herohde) 5/23/2017: reject closures. They can't be serialized.

	name := runtime.FuncForPC(fn.Pointer()).Name()
	fntype := fn.Type()

	var param []FnParam
	for i := 0; i < fntype.NumIn(); i++ {
		t := fntype.In(i)

		kind := FnIllegal
		switch {
		case t == reflectx.Context:
			kind = FnContext
		case t == typex.EventTimeType:
			kind = FnEventTime
		case t == reflectx.Type:
			kind = FnType
		case typex.IsContainer(t), typex.IsConcrete(t), typex.IsUniversal(t):
			kind = FnValue
		case IsEmit(t):
			kind = FnEmit
		case IsIter(t):
			kind = FnIter
		case IsReIter(t):
			kind = FnReIter
		default:
			return nil, fmt.Errorf("bad paramenter type for %s: %v", name, t)
		}

		param = append(param, FnParam{Kind: kind, T: t})
	}

	var ret []ReturnParam
	for i := 0; i < fntype.NumOut(); i++ {
		t := fntype.Out(i)

		kind := RetIllegal
		switch {
		case t == reflectx.Error:
			kind = RetError
		case t == typex.EventTimeType:
			kind = RetEventTime
		case typex.IsContainer(t), typex.IsConcrete(t), typex.IsUniversal(t):
			kind = RetValue
		default:
			return nil, fmt.Errorf("bad return type for %s: %v", name, t)
		}

		ret = append(ret, ReturnParam{Kind: kind, T: t})
	}

	u := &UserFn{Fn: fn, Name: name, Param: param, Ret: ret}

	// TODO(herohde): validate parameter order, restrictions
	return u, nil
}

// SubParams returns the subsequence of the given params with the given
// indices.
func SubParams(list []FnParam, indices ...int) []FnParam {
	var ret []FnParam
	for _, index := range indices {
		ret = append(ret, list[index])
	}
	return ret
}

// SubReturns returns the subsequence of the given return params with
// the given indices.
func SubReturns(list []ReturnParam, indices ...int) []ReturnParam {
	var ret []ReturnParam
	for _, index := range indices {
		ret = append(ret, list[index])
	}
	return ret
}
