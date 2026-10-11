package tjs

import "errors"

// Member flags are kept separate from C++ call flags.  The low bits are
// intentionally available to host code when registering native members.
const (
	MemberReadOnly uint32 = 1 << iota
	MemberHidden
	MemberDontDelete
)

// ReadOnly is an alias used by embedders when declaring immutable data.
const ReadOnly = MemberReadOnly

var (
	ErrMemberNotFound = errors.New("TJS member not found")
	ErrReadOnly       = errors.New("TJS member is read-only")
	ErrCannotDelete   = errors.New("TJS member cannot be deleted")
	ErrBadParamCount  = errors.New("TJS bad parameter count")
	ErrType           = errors.New("TJS type error")
	ErrNotImplemented = errors.New("TJS operation is not implemented")
)

// VMAbortError marks a native or VM error that script try/catch must not swallow.
type VMAbortError struct{ Cause error }

func (e *VMAbortError) Error() string {
	if e == nil || e.Cause == nil {
		return "TJS VM aborted"
	}
	return e.Cause.Error()
}
func (e *VMAbortError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
func vmAbortError(err error) error {
	if err == nil {
		return nil
	}
	var aborted *VMAbortError
	if errors.As(err, &aborted) {
		return err
	}
	return &VMAbortError{Cause: err}
}
func isVMAbortError(err error) bool {
	var aborted *VMAbortError
	return errors.As(err, &aborted)
}

// C++ iTJSDispatch2 flags.  Keep their values identical to tjsInterface.h so
// native registrations can pass flags without a translation table.
const (
	MemberEnsure      uint32 = 0x00000200
	MemberMustExist   uint32 = 0x00000400
	IgnoreProperty    uint32 = 0x00000800
	HiddenMember      uint32 = 0x00001000
	StaticMember      uint32 = 0x00010000
	EnumNoValue       uint32 = 0x00100000
	NativeRegister    uint32 = 0x00000001
	NativeGetInstance uint32 = 0x00000002
)

// C++ spelling aliases are useful when porting native classes.
const (
	TJS_MEMBERENSURE    = MemberEnsure
	TJS_MEMBERMUSTEXIST = MemberMustExist
	TJS_IGNOREPROP      = IgnoreProperty
	TJS_HIDDENMEMBER    = HiddenMember
	TJS_STATICMEMBER    = StaticMember
	TJS_ENUM_NO_VALUE   = EnumNoValue
	TJS_NIS_REGISTER    = NativeRegister
	TJS_NIS_GETINSTANCE = NativeGetInstance
)

// Operation values match TJS_OP_* in tjsInterface.h.
type Operation uint32

const (
	OpBand Operation = 0x0001
	OpBor  Operation = 0x0002
	OpBxor Operation = 0x0003
	OpSub  Operation = 0x0004
	OpAdd  Operation = 0x0005
	OpMod  Operation = 0x0006
	OpDiv  Operation = 0x0007
	OpIDiv Operation = 0x0008
	OpMul  Operation = 0x0009
	OpLor  Operation = 0x000a
	OpLand Operation = 0x000b
	OpSAR  Operation = 0x000c
	OpSAL  Operation = 0x000d
	OpSR   Operation = 0x000e
	OpInc  Operation = 0x000f
	OpDec  Operation = 0x0010
)

const (
	TJS_OP_BAND = OpBand
	TJS_OP_BOR  = OpBor
	TJS_OP_BXOR = OpBxor
	TJS_OP_SUB  = OpSub
	TJS_OP_ADD  = OpAdd
	TJS_OP_MOD  = OpMod
	TJS_OP_DIV  = OpDiv
	TJS_OP_IDIV = OpIDiv
	TJS_OP_MUL  = OpMul
	TJS_OP_LOR  = OpLor
	TJS_OP_LAND = OpLand
	TJS_OP_SAR  = OpSAR
	TJS_OP_SAL  = OpSAL
	TJS_OP_SR   = OpSR
	TJS_OP_INC  = OpInc
	TJS_OP_DEC  = OpDec
	TJS_OP_MASK = Operation(0x001f)
)

func memberReadOnly(flags uint32) bool   { return flags&MemberReadOnly != 0 }
func memberDontDelete(flags uint32) bool { return flags&MemberDontDelete != 0 }

// DefineReadOnly stores an immutable data member and marks it read-only.
func DefineReadOnly(object *Object, name string, value Value) error {
	if object == nil {
		return errInvalidObject
	}
	if err := object.Set(name, value); err != nil {
		return err
	}
	return object.SetFlags(name, MemberReadOnly|MemberDontDelete)
}

// SetReadOnly is a descriptive synonym for DefineReadOnly.
func SetReadOnly(object *Object, name string, value Value) error {
	return DefineReadOnly(object, name, value)
}
