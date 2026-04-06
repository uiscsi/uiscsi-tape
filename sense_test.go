package tape

import (
	"errors"
	"testing"
)

func TestInterpretSense(t *testing.T) {
	tests := []struct {
		name       string
		status     uint8
		senseData  []byte
		wantNil    bool
		wantFM     bool
		wantEOM    bool
		wantILI    bool
		wantBlank  bool
		wantKey    uint8
		wantASC    uint8
		wantASCQ   uint8
		wantIs     []error // sentinel errors that should match via errors.Is
		wantNotIs  []error // sentinel errors that should NOT match
	}{
		{
			name:    "GOOD status returns nil",
			status:  0x00,
			wantNil: true,
		},
		{
			name:      "CHECK CONDITION with filemark",
			status:    0x02,
			senseData: makeSense(0x70, 0x80|0x00, 0, 0), // filemark + NO_SENSE
			wantFM:    true,
			wantKey:   0x00,
			wantIs:    []error{ErrFilemark},
			wantNotIs: []error{ErrEOM, ErrILI, ErrBlankCheck},
		},
		{
			name:      "CHECK CONDITION with EOM",
			status:    0x02,
			senseData: makeSense(0x70, 0x40|0x00, 0, 0), // EOM + NO_SENSE
			wantEOM:   true,
			wantKey:   0x00,
			wantIs:    []error{ErrEOM},
			wantNotIs: []error{ErrFilemark, ErrILI, ErrBlankCheck},
		},
		{
			name:      "CHECK CONDITION with ILI",
			status:    0x02,
			senseData: makeSense(0x70, 0x20|0x05, 0, 0), // ILI + ILLEGAL_REQUEST
			wantILI:   true,
			wantKey:   0x05,
			wantIs:    []error{ErrILI},
			wantNotIs: []error{ErrFilemark, ErrEOM, ErrBlankCheck},
		},
		{
			name:      "CHECK CONDITION with BLANK CHECK",
			status:    0x02,
			senseData: makeSense(0x70, 0x08, 0, 0), // BLANK_CHECK sense key
			wantBlank: true,
			wantKey:   0x08,
			wantIs:    []error{ErrBlankCheck},
			wantNotIs: []error{ErrFilemark, ErrEOM, ErrILI},
		},
		{
			name:      "CHECK CONDITION with all bits set",
			status:    0x02,
			senseData: makeSense(0x70, 0xE0|0x00, 0, 0), // FM+EOM+ILI + NO_SENSE
			wantFM:    true,
			wantEOM:   true,
			wantILI:   true,
			wantKey:   0x00,
			wantIs:    []error{ErrFilemark, ErrEOM, ErrILI},
			wantNotIs: []error{ErrBlankCheck},
		},
		{
			name:    "CHECK CONDITION with nil sense data",
			status:  0x02,
			wantNil: false, // returns a TapeError, not nil
		},
		{
			name:      "CHECK CONDITION with short sense data",
			status:    0x02,
			senseData: []byte{0x70, 0x00, 0x00}, // only 3 bytes, need 18 for fixed
			wantNil:   false,
		},
		{
			name:      "CHECK CONDITION with ASC/ASCQ populated",
			status:    0x02,
			senseData: makeSense(0x70, 0x05, 0x24, 0x00), // ILLEGAL_REQUEST, invalid field in CDB
			wantKey:   0x05,
			wantASC:   0x24,
			wantASCQ:  0x00,
		},
		{
			name:      "descriptor format sense data",
			status:    0x02,
			senseData: makeDescriptorSense(0x72, 0x05, 0x24, 0x00), // ILLEGAL_REQUEST
			wantKey:   0x05,
			wantASC:   0x24,
			wantASCQ:  0x00,
		},
		{
			name:      "descriptor format blank check",
			status:    0x02,
			senseData: makeDescriptorSense(0x72, 0x08, 0x05, 0x00),
			wantBlank: true,
			wantKey:   0x08,
			wantASC:   0x05,
			wantIs:    []error{ErrBlankCheck},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := interpretSense(tt.status, tt.senseData)

			if tt.wantNil {
				if err != nil {
					t.Fatalf("want nil error, got %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("want non-nil error, got nil")
			}

			var te *TapeError
			if !errors.As(err, &te) {
				// For short sense or nil sense, we just check it's an error
				if tt.senseData == nil || len(tt.senseData) < 18 {
					return // ok, it's an error but not necessarily a full TapeError
				}
				t.Fatalf("error is not *TapeError: %v", err)
			}

			if te.Filemark != tt.wantFM {
				t.Errorf("Filemark = %v, want %v", te.Filemark, tt.wantFM)
			}
			if te.EOM != tt.wantEOM {
				t.Errorf("EOM = %v, want %v", te.EOM, tt.wantEOM)
			}
			if te.ILI != tt.wantILI {
				t.Errorf("ILI = %v, want %v", te.ILI, tt.wantILI)
			}
			if te.BlankCheck != tt.wantBlank {
				t.Errorf("BlankCheck = %v, want %v", te.BlankCheck, tt.wantBlank)
			}
			if te.SenseKey != tt.wantKey {
				t.Errorf("SenseKey = 0x%02X, want 0x%02X", te.SenseKey, tt.wantKey)
			}
			if te.ASC != tt.wantASC {
				t.Errorf("ASC = 0x%02X, want 0x%02X", te.ASC, tt.wantASC)
			}
			if te.ASCQ != tt.wantASCQ {
				t.Errorf("ASCQ = 0x%02X, want 0x%02X", te.ASCQ, tt.wantASCQ)
			}

			for _, sentinel := range tt.wantIs {
				if !errors.Is(err, sentinel) {
					t.Errorf("errors.Is(err, %v) = false, want true", sentinel)
				}
			}
			for _, sentinel := range tt.wantNotIs {
				if errors.Is(err, sentinel) {
					t.Errorf("errors.Is(err, %v) = true, want false", sentinel)
				}
			}
		})
	}
}

// makeSense builds an 18-byte fixed-format sense data blob.
func makeSense(responseCode, byte2, asc, ascq byte) []byte {
	sense := make([]byte, 18)
	sense[0] = responseCode // Response code (0x70 = current, 0x71 = deferred)
	sense[2] = byte2        // Filemark|EOM|ILI|SenseKey
	sense[12] = asc         // Additional Sense Code
	sense[13] = ascq        // Additional Sense Code Qualifier
	return sense
}

// makeDescriptorSense builds an 8-byte descriptor-format sense data blob.
func makeDescriptorSense(responseCode, senseKey, asc, ascq byte) []byte {
	sense := make([]byte, 8)
	sense[0] = responseCode    // Response code (0x72 = current, 0x73 = deferred)
	sense[1] = senseKey & 0x0F // Sense key in lower nibble
	sense[2] = asc             // Additional Sense Code
	sense[3] = ascq            // Additional Sense Code Qualifier
	return sense
}
