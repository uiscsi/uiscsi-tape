package tape

import "fmt"

// interpretSense converts raw SCSI status + sense bytes into a *TapeError.
// Returns nil if status is GOOD (0x00).
//
// This parses fixed-format sense data directly from raw bytes -- it does NOT
// use internal/scsi.ParseSense (cannot import internal packages across modules).
// The tape module receives sense bytes via Session.Execute() which returns
// RawResult.SenseData as []byte.
func interpretSense(status uint8, senseData []byte) error {
	if status == 0x00 {
		return nil
	}

	if len(senseData) < 2 {
		return &TapeError{
			Cause: fmt.Errorf("tape: CHECK CONDITION with no sense data"),
		}
	}

	responseCode := senseData[0] & 0x7F

	switch {
	case responseCode == 0x70 || responseCode == 0x71:
		return parseFixedSense(senseData)
	case responseCode == 0x72 || responseCode == 0x73:
		return parseDescriptorSense(senseData)
	default:
		return &TapeError{
			Cause: fmt.Errorf("tape: unknown sense response code 0x%02X", responseCode),
		}
	}
}

// parseFixedSense parses fixed-format (0x70/0x71) sense data.
// Fixed format: byte 2 has Filemark|EOM|ILI|SenseKey, ASC at 12, ASCQ at 13.
func parseFixedSense(data []byte) error {
	if len(data) < 18 {
		// Extract what we can from short data.
		senseKey := data[2] & 0x0F
		return &TapeError{
			SenseKey: senseKey,
			Cause:    fmt.Errorf("tape: fixed-format sense data too short (%d bytes, need 18)", len(data)),
		}
	}

	senseKey := data[2] & 0x0F

	return &TapeError{
		Filemark:   data[2]&0x80 != 0,
		EOM:        data[2]&0x40 != 0,
		ILI:        data[2]&0x20 != 0,
		BlankCheck: senseKey == 0x08,
		SenseKey:   senseKey,
		ASC:        data[12],
		ASCQ:       data[13],
	}
}

// parseDescriptorSense parses descriptor-format (0x72/0x73) sense data.
// Descriptor format: byte 1 has SenseKey, ASC at 2, ASCQ at 3.
// Filemark/EOM/ILI bits are not present in the fixed locations of descriptor format.
func parseDescriptorSense(data []byte) error {
	if len(data) < 8 {
		return &TapeError{
			Cause: fmt.Errorf("tape: descriptor-format sense data too short (%d bytes, need 8)", len(data)),
		}
	}

	senseKey := data[1] & 0x0F

	return &TapeError{
		BlankCheck: senseKey == 0x08,
		SenseKey:   senseKey,
		ASC:        data[2],
		ASCQ:       data[3],
	}
}
