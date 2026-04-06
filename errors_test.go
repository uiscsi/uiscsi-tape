package tape

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestTapeErrorIs(t *testing.T) {
	tests := []struct {
		name   string
		err    *TapeError
		target error
		want   bool
	}{
		{
			name:   "filemark matches ErrFilemark",
			err:    &TapeError{Filemark: true},
			target: ErrFilemark,
			want:   true,
		},
		{
			name:   "no filemark does not match ErrFilemark",
			err:    &TapeError{Filemark: false},
			target: ErrFilemark,
			want:   false,
		},
		{
			name:   "EOM matches ErrEOM",
			err:    &TapeError{EOM: true},
			target: ErrEOM,
			want:   true,
		},
		{
			name:   "blank check matches ErrBlankCheck",
			err:    &TapeError{BlankCheck: true},
			target: ErrBlankCheck,
			want:   true,
		},
		{
			name:   "ILI matches ErrILI",
			err:    &TapeError{ILI: true},
			target: ErrILI,
			want:   true,
		},
		{
			name:   "unrelated error does not match",
			err:    &TapeError{Filemark: true},
			target: errors.New("other"),
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errors.Is(tt.err, tt.target); got != tt.want {
				t.Errorf("errors.Is(%v, %v) = %v, want %v", tt.err, tt.target, got, tt.want)
			}
		})
	}
}

func TestTapeErrorUnwrap(t *testing.T) {
	cause := errors.New("underlying cause")
	te := &TapeError{Cause: cause}
	if te.Unwrap() != cause {
		t.Errorf("Unwrap() = %v, want %v", te.Unwrap(), cause)
	}

	te2 := &TapeError{}
	if te2.Unwrap() != nil {
		t.Errorf("Unwrap() on nil Cause = %v, want nil", te2.Unwrap())
	}
}

func TestTapeErrorAccessors(t *testing.T) {
	te := &TapeError{
		Filemark:   true,
		EOM:        true,
		ILI:        true,
		BlankCheck: true,
	}
	if !te.IsFilemark() {
		t.Error("IsFilemark() = false, want true")
	}
	if !te.IsEOM() {
		t.Error("IsEOM() = false, want true")
	}
	if !te.IsILI() {
		t.Error("IsILI() = false, want true")
	}
	if !te.IsBlankCheck() {
		t.Error("IsBlankCheck() = false, want true")
	}

	te2 := &TapeError{}
	if te2.IsFilemark() {
		t.Error("IsFilemark() = true, want false")
	}
	if te2.IsEOM() {
		t.Error("IsEOM() = true, want false")
	}
	if te2.IsILI() {
		t.Error("IsILI() = true, want false")
	}
	if te2.IsBlankCheck() {
		t.Error("IsBlankCheck() = true, want false")
	}
}

func TestTapeErrorErrorString(t *testing.T) {
	te := &TapeError{Filemark: true}
	s := te.Error()
	if !strings.HasPrefix(s, "tape:") {
		t.Errorf("Error() = %q, want prefix 'tape:'", s)
	}
	if !strings.Contains(s, "filemark") {
		t.Errorf("Error() = %q, want to contain 'filemark'", s)
	}

	// Multiple conditions
	te2 := &TapeError{Filemark: true, EOM: true, ILI: true, BlankCheck: true}
	s2 := te2.Error()
	if !strings.Contains(s2, "filemark") || !strings.Contains(s2, "eom") || !strings.Contains(s2, "ili") || !strings.Contains(s2, "blank check") {
		t.Errorf("Error() = %q, want all condition names", s2)
	}
}

func TestTapeErrorWrappedInErrorsIs(t *testing.T) {
	te := &TapeError{Filemark: true, Cause: errors.New("io error")}
	wrapped := fmt.Errorf("wrapper: %w", te)
	if !errors.Is(wrapped, ErrFilemark) {
		t.Error("errors.Is on wrapped TapeError should match ErrFilemark")
	}
}
