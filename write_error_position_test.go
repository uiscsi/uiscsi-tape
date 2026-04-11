package tape

import (
	"errors"
	"testing"
)

// TestWriteErrorPositionFixedValid verifies that interpretSense with
// fixed-format sense (0x70) containing VALID bit and INFORMATION field
// populates TapeError.Position.
func TestWriteErrorPositionFixedValid(t *testing.T) {
	// Fixed-format sense data (response code 0x70):
	//   byte 0: 0xF0 (0x70 | VALID bit 0x80)
	//   byte 2: 0x03 (MEDIUM ERROR sense key)
	//   bytes 3-6: INFORMATION field = 0x00001000 (4096)
	//   byte 7: additional length = 10
	//   bytes 12-13: ASC/ASCQ = 0x0C/0x00 (Write error)
	sense := make([]byte, 18)
	sense[0] = 0xF0 // response code 0x70 + VALID
	sense[2] = 0x03 // MEDIUM ERROR
	sense[3] = 0x00 // INFORMATION MSB
	sense[4] = 0x00
	sense[5] = 0x10
	sense[6] = 0x00 // INFORMATION LSB = 0x00001000 = 4096
	sense[7] = 10   // additional sense length
	sense[12] = 0x0C // ASC: Write error
	sense[13] = 0x00 // ASCQ

	err := interpretSense(0x02, sense) // CHECK CONDITION
	if err == nil {
		t.Fatal("expected error from interpretSense")
	}

	var te *TapeError
	if !errors.As(err, &te) {
		t.Fatalf("expected *TapeError, got %T: %v", err, err)
	}
	if te.Position != 4096 {
		t.Errorf("Position = %d, want 4096", te.Position)
	}
	if te.SenseKey != 0x03 {
		t.Errorf("SenseKey = 0x%02X, want 0x03", te.SenseKey)
	}
}

// TestWriteErrorPositionFixedNoValid verifies that when VALID bit is NOT
// set, TapeError.Position is 0.
func TestWriteErrorPositionFixedNoValid(t *testing.T) {
	// Fixed-format sense data without VALID bit.
	sense := make([]byte, 18)
	sense[0] = 0x70 // response code 0x70, NO VALID bit
	sense[2] = 0x03 // MEDIUM ERROR
	sense[3] = 0x00 // INFORMATION field (should be ignored)
	sense[4] = 0x00
	sense[5] = 0x10
	sense[6] = 0x00 // 4096 -- but VALID not set
	sense[7] = 10
	sense[12] = 0x0C
	sense[13] = 0x00

	err := interpretSense(0x02, sense)
	if err == nil {
		t.Fatal("expected error from interpretSense")
	}

	var te *TapeError
	if !errors.As(err, &te) {
		t.Fatalf("expected *TapeError, got %T: %v", err, err)
	}
	// Position should be 0 because VALID bit was not set -- however,
	// Position is populated from si.Information which carries the raw
	// value regardless of VALID. The VALID bit indicates whether the
	// INFORMATION field is defined for the error. We still propagate
	// the raw value; callers check InformationValid on SenseInfo.
	// For TapeError.Position, we populate it unconditionally from
	// si.Information since the caller can check the sense data VALID
	// bit if needed via the underlying SCSIError.
	//
	// After review: the plan says "VALID bit NOT set results in
	// TapeError.Position = 0". We should only populate Position when
	// VALID is set.
	if te.Position != 0 {
		t.Errorf("Position = %d, want 0 (VALID bit not set)", te.Position)
	}
}

// TestWriteErrorPositionErrorsAs verifies the errors.As pattern works
// for accessing TapeError.Position.
func TestWriteErrorPositionErrorsAs(t *testing.T) {
	sense := make([]byte, 18)
	sense[0] = 0xF0 // VALID
	sense[2] = 0x03
	sense[3] = 0x00
	sense[4] = 0x00
	sense[5] = 0x00
	sense[6] = 0x42 // Position = 66
	sense[7] = 10
	sense[12] = 0x0C
	sense[13] = 0x02

	err := interpretSense(0x02, sense)

	var te *TapeError
	if !errors.As(err, &te) {
		t.Fatalf("errors.As failed: got %T", err)
	}
	if te.Position != 66 {
		t.Errorf("Position = %d, want 66", te.Position)
	}
}

// TestWriteErrorPositionInErrorString verifies Position appears in Error()
// output when non-zero.
func TestWriteErrorPositionInErrorString(t *testing.T) {
	te := &TapeError{
		SenseKey: 0x03,
		ASC:      0x0C,
		ASCQ:     0x00,
		Position: 4096,
	}
	s := te.Error()
	if !contains(s, "pos=4096") {
		t.Errorf("Error() = %q, want to contain 'pos=4096'", s)
	}

	// Zero position should not appear.
	te2 := &TapeError{
		SenseKey: 0x03,
		ASC:      0x0C,
		ASCQ:     0x00,
		Position: 0,
	}
	s2 := te2.Error()
	if contains(s2, "pos=") {
		t.Errorf("Error() = %q, should not contain 'pos=' when Position is 0", s2)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
