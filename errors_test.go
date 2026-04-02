package miot

import (
	"errors"
	"io"
	"testing"
)

func TestWrapErrorPreservesCode(t *testing.T) {
	err := Wrap(ErrInvalidResponse, "decode gethome", io.EOF)
	if !errors.Is(err, io.EOF) {
		t.Fatal("wrapped cause missing")
	}

	miotErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("Wrap() returned %T, want *Error", err)
	}
	if miotErr.Code != ErrInvalidResponse {
		t.Fatalf("Error.Code = %q", miotErr.Code)
	}
}

func TestWrapNilReturnsNil(t *testing.T) {
	if err := Wrap(ErrInvalidResponse, "decode gethome", nil); err != nil {
		t.Fatalf("Wrap() = %v, want nil", err)
	}
}
