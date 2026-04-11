package test

import (
	"bytes"
	"context"
	"sort"
	"testing"

	"github.com/uiscsi/uiscsi"
	"github.com/uiscsi/uiscsi-tape/internal/ssc"
)

// TestMockInjectError_ConsumedOnce verifies that an injected error is
// consumed by the first matching command and subsequent commands succeed.
func TestMockInjectError_ConsumedOnce(t *testing.T) {
	mock, sess := SetupMock(t)
	ctx := context.Background()

	// Inject a NOT READY error on TUR (opcode 0x00)
	mock.InjectError(0x00, 0x02, 0x04, 0x01) // NOT READY / LOGICAL UNIT IS IN PROCESS OF BECOMING READY

	// First TUR should fail with CHECK CONDITION
	cdb := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00} // TUR
	result, err := sess.Raw().Execute(ctx, 0, cdb)
	if err != nil {
		t.Fatalf("Execute TUR: %v", err)
	}
	if result.Status != 0x02 {
		t.Fatalf("first TUR status = 0x%02X, want 0x02 (CHECK CONDITION)", result.Status)
	}
	// Verify sense key
	if len(result.SenseData) < 3 {
		t.Fatal("no sense data returned")
	}
	if result.SenseData[2]&0x0F != 0x02 {
		t.Errorf("sense key = 0x%02X, want 0x02 (NOT READY)", result.SenseData[2]&0x0F)
	}

	// Second TUR should succeed (injection consumed)
	result, err = sess.Raw().Execute(ctx, 0, cdb)
	if err != nil {
		t.Fatalf("Execute TUR (second): %v", err)
	}
	if result.Status != 0x00 {
		t.Errorf("second TUR status = 0x%02X, want 0x00 (GOOD)", result.Status)
	}
}

// TestMockInjectError_FIFO verifies that multiple injected errors for the
// same opcode are consumed in FIFO order.
func TestMockInjectError_FIFO(t *testing.T) {
	mock, sess := SetupMock(t)
	ctx := context.Background()

	// Queue two different errors on TUR
	mock.InjectError(0x00, 0x02, 0x04, 0x01) // NOT READY
	mock.InjectError(0x00, 0x06, 0x29, 0x00) // UNIT ATTENTION

	cdb := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	// First: NOT READY
	result, err := sess.Raw().Execute(ctx, 0, cdb)
	if err != nil {
		t.Fatalf("Execute TUR (1): %v", err)
	}
	if result.SenseData[2]&0x0F != 0x02 {
		t.Errorf("first sense key = 0x%02X, want 0x02", result.SenseData[2]&0x0F)
	}

	// Second: UNIT ATTENTION
	result, err = sess.Raw().Execute(ctx, 0, cdb)
	if err != nil {
		t.Fatalf("Execute TUR (2): %v", err)
	}
	if result.SenseData[2]&0x0F != 0x06 {
		t.Errorf("second sense key = 0x%02X, want 0x06", result.SenseData[2]&0x0F)
	}

	// Third: GOOD
	result, err = sess.Raw().Execute(ctx, 0, cdb)
	if err != nil {
		t.Fatalf("Execute TUR (3): %v", err)
	}
	if result.Status != 0x00 {
		t.Errorf("third TUR status = 0x%02X, want 0x00", result.Status)
	}
}

// TestMockInjectFilemark verifies that InjectFilemark adds a filemark at
// the specified position, and a subsequent READ at that position returns
// filemark sense.
func TestMockInjectFilemark(t *testing.T) {
	mock, sess := SetupMock(t)
	ctx := context.Background()

	// Write 1024 bytes
	data := make([]byte, 1024)
	for i := range data {
		data[i] = 0xAA
	}
	writeCDB := ssc.WriteCDB(false, uint32(len(data)))
	_, err := sess.Raw().Execute(ctx, 0, writeCDB, uiscsi.WithDataOut(bytes.NewReader(data), uint32(len(data))))
	if err != nil {
		t.Fatalf("WRITE: %v", err)
	}

	// Inject a filemark at position 512
	mock.InjectFilemark(512)

	// Rewind
	_, err = sess.Raw().Execute(ctx, 0, ssc.RewindCDB(false))
	if err != nil {
		t.Fatalf("REWIND: %v", err)
	}

	// Read 512 bytes (to reach filemark position)
	readCDB := ssc.ReadCDB(false, false, 512)
	result, err := sess.Raw().Execute(ctx, 0, readCDB, uiscsi.WithDataIn(512))
	if err != nil {
		t.Fatalf("READ (advance): %v", err)
	}
	if result.Status != 0x00 {
		t.Fatalf("READ (advance) status = 0x%02X, want 0x00", result.Status)
	}

	// Now at position 512 -- should hit the filemark
	readCDB = ssc.ReadCDB(false, false, 512)
	result, err = sess.Raw().Execute(ctx, 0, readCDB, uiscsi.WithDataIn(512))
	if err != nil {
		t.Fatalf("READ (at filemark): %v", err)
	}
	if result.Status != 0x02 {
		t.Fatalf("READ at filemark status = 0x%02X, want 0x02", result.Status)
	}
	if len(result.SenseData) < 3 || result.SenseData[2]&0x80 == 0 {
		t.Error("filemark bit not set in sense data")
	}
}

// TestMockInjectShortRead verifies that InjectShortRead causes the next
// READ to return CHECK CONDITION with ILI sense and correct residue in
// the INFORMATION field.
func TestMockInjectShortRead(t *testing.T) {
	mock, sess := SetupMock(t)
	ctx := context.Background()

	// Write 1024 bytes
	data := make([]byte, 1024)
	for i := range data {
		data[i] = 0xBB
	}
	writeCDB := ssc.WriteCDB(false, uint32(len(data)))
	_, err := sess.Raw().Execute(ctx, 0, writeCDB, uiscsi.WithDataOut(bytes.NewReader(data), uint32(len(data))))
	if err != nil {
		t.Fatalf("WRITE: %v", err)
	}

	// Rewind
	_, err = sess.Raw().Execute(ctx, 0, ssc.RewindCDB(false))
	if err != nil {
		t.Fatalf("REWIND: %v", err)
	}

	// Inject short read: only 100 bytes instead of requested
	mock.InjectShortRead(0x08, 100)

	// Read 1024 bytes -- should get CHECK CONDITION with ILI
	readCDB := ssc.ReadCDB(false, false, 1024)
	result, err := sess.Raw().Execute(ctx, 0, readCDB, uiscsi.WithDataIn(1024))
	if err != nil {
		t.Fatalf("READ (short): %v", err)
	}
	if result.Status != 0x02 {
		t.Fatalf("READ status = 0x%02X, want 0x02 (CHECK CONDITION)", result.Status)
	}

	// Verify ILI bit in sense (byte 2 bit 5)
	if len(result.SenseData) < 3 {
		t.Fatal("no sense data")
	}
	if result.SenseData[2]&0x20 == 0 {
		t.Error("ILI bit not set in sense data")
	}

	// Verify INFORMATION field contains residue (1024-100=924)
	if result.SenseData[0]&0x80 == 0 {
		t.Error("VALID bit not set in sense data")
	}
	if len(result.SenseData) >= 7 {
		info := uint32(result.SenseData[3])<<24 | uint32(result.SenseData[4])<<16 |
			uint32(result.SenseData[5])<<8 | uint32(result.SenseData[6])
		if info != 924 {
			t.Errorf("INFORMATION = %d, want 924 (residue)", info)
		}
	}
}

// TestMockSPACE_Blocks verifies SPACE(blocks) advances position correctly.
func TestMockSPACE_Blocks(t *testing.T) {
	mock, sess := SetupMock(t)
	ctx := context.Background()

	// Set block size to 1024 via MODE SELECT
	modeData := make([]byte, 12)
	modeData[3] = 8 // block descriptor length
	modeData[9] = 0
	modeData[10] = 0x04
	modeData[11] = 0x00 // block length = 1024
	msCDB := ssc.ModeSelect6CDB(12)
	_, err := sess.Raw().Execute(ctx, 0, msCDB, uiscsi.WithDataOut(bytes.NewReader(modeData), 12))
	if err != nil {
		t.Fatalf("MODE SELECT: %v", err)
	}

	// Write 3 records of 1024 bytes each
	for i := 0; i < 3; i++ {
		data := make([]byte, 1024)
		data[0] = byte(i)
		writeCDB := ssc.WriteCDB(false, 1024)
		_, err := sess.Raw().Execute(ctx, 0, writeCDB, uiscsi.WithDataOut(bytes.NewReader(data), 1024))
		if err != nil {
			t.Fatalf("WRITE %d: %v", i, err)
		}
	}

	// Rewind
	_, err = sess.Raw().Execute(ctx, 0, ssc.RewindCDB(false))
	if err != nil {
		t.Fatalf("REWIND: %v", err)
	}

	// SPACE(blocks, 2)
	spaceCDB := ssc.SpaceCDB(0x00, 2)
	result, err := sess.Raw().Execute(ctx, 0, spaceCDB)
	if err != nil {
		t.Fatalf("SPACE: %v", err)
	}
	if result.Status != 0x00 {
		t.Fatalf("SPACE status = 0x%02X, want 0x00", result.Status)
	}

	if pos := mock.Position(); pos != 2048 {
		t.Errorf("position after SPACE(blocks, 2) = %d, want 2048", pos)
	}
}

// TestMockSPACE_Filemarks verifies SPACE(filemarks) advances to past the filemark.
func TestMockSPACE_Filemarks(t *testing.T) {
	mock, sess := SetupMock(t)
	ctx := context.Background()

	// Write data, filemark, data, filemark
	data := make([]byte, 1024)
	writeCDB := ssc.WriteCDB(false, 1024)
	_, err := sess.Raw().Execute(ctx, 0, writeCDB, uiscsi.WithDataOut(bytes.NewReader(data), 1024))
	if err != nil {
		t.Fatalf("WRITE 1: %v", err)
	}

	fmCDB := ssc.WriteFilemarksCDB(1)
	_, err = sess.Raw().Execute(ctx, 0, fmCDB)
	if err != nil {
		t.Fatalf("WRITE FM 1: %v", err)
	}
	fm1Pos := mock.Position()

	_, err = sess.Raw().Execute(ctx, 0, writeCDB, uiscsi.WithDataOut(bytes.NewReader(data), 1024))
	if err != nil {
		t.Fatalf("WRITE 2: %v", err)
	}

	_, err = sess.Raw().Execute(ctx, 0, fmCDB)
	if err != nil {
		t.Fatalf("WRITE FM 2: %v", err)
	}

	// Rewind
	_, err = sess.Raw().Execute(ctx, 0, ssc.RewindCDB(false))
	if err != nil {
		t.Fatalf("REWIND: %v", err)
	}

	// SPACE(filemarks, 1) -- advance past first filemark
	spaceCDB := ssc.SpaceCDB(0x01, 1)
	result, err := sess.Raw().Execute(ctx, 0, spaceCDB)
	if err != nil {
		t.Fatalf("SPACE(filemarks): %v", err)
	}
	if result.Status != 0x00 {
		t.Fatalf("SPACE status = 0x%02X, want 0x00", result.Status)
	}

	// Position should be at the first filemark position (SPACE consumes the
	// filemark from the list so the next READ sees data at that position).
	got := mock.Position()
	if got != fm1Pos {
		t.Errorf("position after SPACE(filemarks, 1) = %d, want %d", got, fm1Pos)
	}
}

// TestMockSPACE_EndOfData verifies SPACE(end-of-data) moves to written position.
func TestMockSPACE_EndOfData(t *testing.T) {
	mock, sess := SetupMock(t)
	ctx := context.Background()

	// Write some data
	data := make([]byte, 2048)
	writeCDB := ssc.WriteCDB(false, 2048)
	_, err := sess.Raw().Execute(ctx, 0, writeCDB, uiscsi.WithDataOut(bytes.NewReader(data), 2048))
	if err != nil {
		t.Fatalf("WRITE: %v", err)
	}

	// Rewind
	_, err = sess.Raw().Execute(ctx, 0, ssc.RewindCDB(false))
	if err != nil {
		t.Fatalf("REWIND: %v", err)
	}

	// SPACE(end-of-data, 0)
	spaceCDB := ssc.SpaceCDB(0x03, 0)
	result, err := sess.Raw().Execute(ctx, 0, spaceCDB)
	if err != nil {
		t.Fatalf("SPACE(EOD): %v", err)
	}
	if result.Status != 0x00 {
		t.Fatalf("SPACE(EOD) status = 0x%02X, want 0x00", result.Status)
	}

	if pos := mock.Position(); pos != mock.Written() {
		t.Errorf("position = %d, want %d (written)", pos, mock.Written())
	}
}

// TestMockSPACE_Setmarks verifies SPACE(setmarks) returns ILLEGAL REQUEST.
func TestMockSPACE_Setmarks(t *testing.T) {
	_, sess := SetupMock(t)
	ctx := context.Background()

	spaceCDB := ssc.SpaceCDB(0x04, 1)
	result, err := sess.Raw().Execute(ctx, 0, spaceCDB)
	if err != nil {
		t.Fatalf("SPACE(setmarks): %v", err)
	}
	if result.Status != 0x02 {
		t.Fatalf("SPACE(setmarks) status = 0x%02X, want 0x02", result.Status)
	}
	if len(result.SenseData) >= 3 {
		if result.SenseData[2]&0x0F != 0x05 {
			t.Errorf("sense key = 0x%02X, want 0x05 (ILLEGAL REQUEST)", result.SenseData[2]&0x0F)
		}
	}
}

// TestMockSPACE_BackwardPastBOT verifies SPACE backward past beginning of
// tape returns sense with BEGINNING OF PARTITION/MEDIUM DETECTED.
func TestMockSPACE_BackwardPastBOT(t *testing.T) {
	mock, sess := SetupMock(t)
	ctx := context.Background()

	// Set block size so SPACE has a step
	modeData := make([]byte, 12)
	modeData[3] = 8
	modeData[10] = 0x04 // block length = 1024
	msCDB := ssc.ModeSelect6CDB(12)
	_, err := sess.Raw().Execute(ctx, 0, msCDB, uiscsi.WithDataOut(bytes.NewReader(modeData), 12))
	if err != nil {
		t.Fatalf("MODE SELECT: %v", err)
	}

	// Position is 0 (BOT). SPACE(blocks, -1)
	spaceCDB := ssc.SpaceCDB(0x00, -1)
	result, err := sess.Raw().Execute(ctx, 0, spaceCDB)
	if err != nil {
		t.Fatalf("SPACE: %v", err)
	}
	if result.Status != 0x02 {
		t.Fatalf("SPACE backward past BOT status = 0x%02X, want 0x02", result.Status)
	}

	// Position should be clamped to 0
	if pos := mock.Position(); pos != 0 {
		t.Errorf("position = %d, want 0", pos)
	}

	// Check ASC/ASCQ 0x00/0x04
	if len(result.SenseData) >= 14 {
		if result.SenseData[12] != 0x00 || result.SenseData[13] != 0x04 {
			t.Errorf("ASC/ASCQ = 0x%02X/0x%02X, want 0x00/0x04", result.SenseData[12], result.SenseData[13])
		}
	}
}

// Ensure sort is used (for the actual mock implementation - used in import).
var _ = sort.Ints
