package tape

import (
	"fmt"

	"github.com/uiscsi/uiscsi"
)

// interpretSense converts raw SCSI status + sense bytes into a *TapeError.
// Returns nil if status is GOOD (0x00). Uses [uiscsi.ParseSenseData] for
// the common sense parsing, then adds tape-specific condition flags
// (BlankCheck) and wraps the result into [TapeError].
func interpretSense(status uint8, senseData []byte) error {
	if status == 0x00 {
		return nil
	}

	si, err := uiscsi.ParseSenseData(senseData)
	if err != nil {
		return &TapeError{
			Cause: fmt.Errorf("tape: sense parse failed: %w", err),
		}
	}
	if si == nil {
		return &TapeError{
			Cause: fmt.Errorf("tape: CHECK CONDITION with no sense data"),
		}
	}

	return &TapeError{
		Filemark:   si.Filemark,
		EOM:        si.EOM,
		ILI:        si.ILI,
		BlankCheck: si.Key == 0x08, // BLANK CHECK sense key
		SenseKey:   si.Key,
		ASC:        si.ASC,
		ASCQ:       si.ASCQ,
	}
}
