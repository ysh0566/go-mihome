package miot

import "fmt"

// Error is a structured MIoT error with an operation, code, and wrapped cause.
type Error struct {
	Code ErrorCode
	Op   string
	Err  error
	Msg  string
}

// Error returns a human-readable description of the MIoT error.
func (e *Error) Error() string {
	switch {
	case e == nil:
		return "<nil>"
	case e.Msg != "" && e.Op != "":
		return fmt.Sprintf("%s: %s: %s", e.Op, e.Code, e.Msg)
	case e.Msg != "":
		return fmt.Sprintf("%s: %s", e.Code, e.Msg)
	case e.Op != "":
		return fmt.Sprintf("%s: %s", e.Op, e.Code)
	default:
		return string(e.Code)
	}
}

// Unwrap returns the underlying cause.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Wrap returns a structured MIoT error that preserves the wrapped cause.
func Wrap(code ErrorCode, op string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Op: op, Err: err, Msg: err.Error()}
}
