package tape

import (
	"errors"
	"strings"
)

// Sentinel errors for tape-specific conditions.
var (
	// ErrFilemark indicates a filemark was encountered during read.
	ErrFilemark = errors.New("tape: filemark detected")

	// ErrEOM indicates end-of-medium or end-of-partition was reached.
	ErrEOM = errors.New("tape: end of medium")

	// ErrBlankCheck indicates a blank/unwritten area was encountered.
	ErrBlankCheck = errors.New("tape: blank check")

	// ErrILI indicates an incorrect length was detected (block size mismatch).
	ErrILI = errors.New("tape: incorrect length indicator")

	// ErrNotTape indicates the device is not a sequential-access (tape) device.
	ErrNotTape = errors.New("tape: device is not a tape drive")
)

// TapeError represents a tape-specific error with condition flags parsed from
// sense data. It supports errors.Is matching against sentinel errors based on
// which condition flags are set.
type TapeError struct {
	Filemark   bool
	EOM        bool
	ILI        bool
	BlankCheck bool
	SenseKey   uint8
	ASC        uint8
	ASCQ       uint8
	Cause      error
}

// Error returns a human-readable description of the tape error.
func (e *TapeError) Error() string {
	var parts []string
	if e.Filemark {
		parts = append(parts, "filemark")
	}
	if e.EOM {
		parts = append(parts, "eom")
	}
	if e.ILI {
		parts = append(parts, "ili")
	}
	if e.BlankCheck {
		parts = append(parts, "blank check")
	}
	if len(parts) == 0 {
		if e.Cause != nil {
			return "tape: " + e.Cause.Error()
		}
		return "tape: error"
	}
	return "tape: " + strings.Join(parts, ", ")
}

// Unwrap returns the underlying cause error.
func (e *TapeError) Unwrap() error {
	return e.Cause
}

// Is reports whether the error matches a sentinel tape error based on
// the condition flags set in this TapeError.
func (e *TapeError) Is(target error) bool {
	switch {
	case target == ErrFilemark:
		return e.Filemark
	case target == ErrEOM:
		return e.EOM
	case target == ErrBlankCheck:
		return e.BlankCheck
	case target == ErrILI:
		return e.ILI
	default:
		return false
	}
}

// IsFilemark reports whether the filemark condition is set.
func (e *TapeError) IsFilemark() bool { return e.Filemark }

// IsEOM reports whether the end-of-medium condition is set.
func (e *TapeError) IsEOM() bool { return e.EOM }

// IsILI reports whether the incorrect length indicator condition is set.
func (e *TapeError) IsILI() bool { return e.ILI }

// IsBlankCheck reports whether the blank check condition is set.
func (e *TapeError) IsBlankCheck() bool { return e.BlankCheck }
