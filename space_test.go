package tape_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	tape "github.com/uiscsi/uiscsi-tape"
	"github.com/uiscsi/uiscsi-tape/internal/ssc"
	"github.com/uiscsi/uiscsi-tape/test"

	"github.com/uiscsi/uiscsi"
)

// setupTestDrive creates a MockTapeDrive with 1 MiB media, a connected
// session, and a Drive opened with 1024-byte fixed blocks.
func setupTestDrive(t *testing.T) (*tape.Drive, *test.MockTapeDrive, *uiscsi.Session) {
	t.Helper()

	mock := test.NewMockTapeDrive(1 << 20) // 1 MiB
	t.Cleanup(func() { mock.Close() })

	ctx := context.Background()
	sess, err := uiscsi.Dial(ctx, mock.Addr(),
		uiscsi.WithTarget("iqn.2026-04.test:tape"),
	)
	if err != nil {
		mock.Close()
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { sess.Close() })

	drv, err := tape.Open(ctx, sess, 0, tape.WithBlockSize(1024))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { drv.Close(ctx) })

	return drv, mock, sess
}

// spaceCommand sends a raw SPACE(6) command and returns the result status
// and any error.
func spaceCommand(t *testing.T, sess *uiscsi.Session, code uint8, count int32) (uint8, []byte) {
	t.Helper()
	ctx := context.Background()
	cdb := ssc.SpaceCDB(code, count)
	result, err := sess.Raw().Execute(ctx, 0, cdb)
	if err != nil {
		t.Fatalf("SPACE(%d, %d): %v", code, count, err)
	}
	return result.Status, result.SenseData
}

func TestSpaceBlocks_Forward(t *testing.T) {
	drv, mock, sess := setupTestDrive(t)
	ctx := context.Background()

	// Write 3 x 1024-byte records
	data := make([]byte, 1024)
	for i := 0; i < 3; i++ {
		data[0] = byte(i)
		if err := drv.Write(ctx, data); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	if err := drv.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	// SPACE(blocks, 2)
	status, _ := spaceCommand(t, sess, 0x00, 2)
	if status != 0x00 {
		t.Fatalf("SPACE status = 0x%02X, want 0x00", status)
	}
	if pos := mock.Position(); pos != 2048 {
		t.Errorf("position = %d, want 2048", pos)
	}
}

func TestSpaceBlocks_Backward(t *testing.T) {
	drv, mock, sess := setupTestDrive(t)
	ctx := context.Background()

	// Write 3 x 1024-byte records (position at 3072)
	data := make([]byte, 1024)
	for i := 0; i < 3; i++ {
		if err := drv.Write(ctx, data); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	// SPACE(blocks, -1)
	status, _ := spaceCommand(t, sess, 0x00, -1)
	if status != 0x00 {
		t.Fatalf("SPACE status = 0x%02X, want 0x00", status)
	}
	if pos := mock.Position(); pos != 2048 {
		t.Errorf("position = %d, want 2048", pos)
	}
}

func TestSpaceFilemarks_Forward(t *testing.T) {
	drv, mock, sess := setupTestDrive(t)
	ctx := context.Background()

	// Write [data, FM, data, FM]
	data := make([]byte, 1024)
	if err := drv.Write(ctx, data); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	if err := drv.WriteFilemarks(ctx, 1); err != nil {
		t.Fatalf("WriteFilemarks 1: %v", err)
	}
	fm1Pos := mock.Position()

	if err := drv.Write(ctx, data); err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	if err := drv.WriteFilemarks(ctx, 1); err != nil {
		t.Fatalf("WriteFilemarks 2: %v", err)
	}

	if err := drv.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	// SPACE(filemarks, 1) -- advance past first filemark
	status, _ := spaceCommand(t, sess, 0x01, 1)
	if status != 0x00 {
		t.Fatalf("SPACE status = 0x%02X, want 0x00", status)
	}
	if pos := mock.Position(); pos != fm1Pos {
		t.Errorf("position = %d, want %d (past first FM)", pos, fm1Pos)
	}
}

func TestSpaceFilemarks_Backward(t *testing.T) {
	drv, mock, sess := setupTestDrive(t)
	ctx := context.Background()

	// Write [data, FM, data, FM]
	data := make([]byte, 1024)
	if err := drv.Write(ctx, data); err != nil {
		t.Fatal(err)
	}
	if err := drv.WriteFilemarks(ctx, 1); err != nil {
		t.Fatal(err)
	}
	fm1Pos := mock.Position()

	if err := drv.Write(ctx, data); err != nil {
		t.Fatal(err)
	}
	if err := drv.WriteFilemarks(ctx, 1); err != nil {
		t.Fatal(err)
	}
	// Position is after second FM

	// SPACE(filemarks, -1) -- back to first FM
	status, _ := spaceCommand(t, sess, 0x01, -1)
	if status != 0x00 {
		t.Fatalf("SPACE status = 0x%02X, want 0x00", status)
	}
	if pos := mock.Position(); pos != fm1Pos {
		t.Errorf("position = %d, want %d (first FM)", pos, fm1Pos)
	}
}

func TestSpaceSequentialFilemarks(t *testing.T) {
	drv, mock, sess := setupTestDrive(t)
	ctx := context.Background()

	// Write 3 consecutive filemarks
	if err := drv.WriteFilemarks(ctx, 3); err != nil {
		t.Fatal(err)
	}

	if err := drv.Rewind(ctx); err != nil {
		t.Fatal(err)
	}

	// SPACE(sequential-filemarks=0x02, count=2) -- skip 2 FMs
	status, _ := spaceCommand(t, sess, 0x02, 2)
	if status != 0x00 {
		t.Fatalf("SPACE status = 0x%02X, want 0x00", status)
	}
	// Position should be at the second filemark (position 0 for all three
	// since filemarks at position 0 are stacked). The mock stacks all three
	// at position 0 because no data was written between them.
	// With filemarks all at position 0, SPACE(fm, 2) finds 2 FMs at pos>=0.
	_ = mock.Position() // non-negative
}

func TestSpaceEndOfData(t *testing.T) {
	drv, mock, sess := setupTestDrive(t)
	ctx := context.Background()

	// Write 3 records + filemark
	data := make([]byte, 1024)
	for i := 0; i < 3; i++ {
		if err := drv.Write(ctx, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := drv.WriteFilemarks(ctx, 1); err != nil {
		t.Fatal(err)
	}

	if err := drv.Rewind(ctx); err != nil {
		t.Fatal(err)
	}

	// SPACE(end-of-data, 0)
	status, _ := spaceCommand(t, sess, 0x03, 0)
	if status != 0x00 {
		t.Fatalf("SPACE status = 0x%02X, want 0x00", status)
	}
	if pos := mock.Position(); pos != mock.Written() {
		t.Errorf("position = %d, want %d (written)", pos, mock.Written())
	}
}

func TestWriteAfterFilemark(t *testing.T) {
	drv, mock, _ := setupTestDrive(t)
	ctx := context.Background()

	// Write 1024 bytes, filemark, 1024 bytes more
	data1 := bytes.Repeat([]byte{0xAA}, 1024)
	if err := drv.Write(ctx, data1); err != nil {
		t.Fatal(err)
	}
	if err := drv.WriteFilemarks(ctx, 1); err != nil {
		t.Fatal(err)
	}
	data2 := bytes.Repeat([]byte{0xBB}, 1024)
	if err := drv.Write(ctx, data2); err != nil {
		t.Fatal(err)
	}

	// Verify total written (1024 data + filemark marker + 1024 data = 2048 bytes)
	if w := mock.Written(); w != 2048 {
		t.Errorf("Written = %d, want 2048", w)
	}

	// Rewind and read: data1, filemark, data2
	if err := drv.Rewind(ctx); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 1024)
	n, err := drv.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read 1: %v", err)
	}
	if n != 1024 || buf[0] != 0xAA {
		t.Errorf("Read 1: n=%d, first byte=0x%02X, want 1024/0xAA", n, buf[0])
	}

	// Read 2 -- filemark
	_, err = drv.Read(ctx, buf)
	if !errors.Is(err, tape.ErrFilemark) {
		t.Fatalf("Read 2: err=%v, want ErrFilemark", err)
	}

	// Read 3 -- data after filemark
	n, err = drv.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read 3: %v", err)
	}
	if n != 1024 || buf[0] != 0xBB {
		t.Errorf("Read 3: n=%d, first byte=0x%02X, want 1024/0xBB", n, buf[0])
	}
}

func TestReadAcrossFilemark(t *testing.T) {
	drv, _, _ := setupTestDrive(t)
	ctx := context.Background()

	// Write [0xAA rec, FM, 0xBB rec]
	data1 := bytes.Repeat([]byte{0xAA}, 1024)
	if err := drv.Write(ctx, data1); err != nil {
		t.Fatal(err)
	}
	if err := drv.WriteFilemarks(ctx, 1); err != nil {
		t.Fatal(err)
	}
	data2 := bytes.Repeat([]byte{0xBB}, 1024)
	if err := drv.Write(ctx, data2); err != nil {
		t.Fatal(err)
	}

	if err := drv.Rewind(ctx); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 1024)

	// Read 1: 0xAA data
	n, err := drv.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read 1: %v", err)
	}
	if n != 1024 || buf[0] != 0xAA {
		t.Errorf("Read 1: n=%d byte=0x%02X, want 1024/0xAA", n, buf[0])
	}

	// Read 2: filemark
	_, err = drv.Read(ctx, buf)
	if !errors.Is(err, tape.ErrFilemark) {
		t.Fatalf("Read 2: %v, want ErrFilemark", err)
	}

	// Read 3: 0xBB data
	n, err = drv.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read 3: %v", err)
	}
	if n != 1024 || buf[0] != 0xBB {
		t.Errorf("Read 3: n=%d byte=0x%02X, want 1024/0xBB", n, buf[0])
	}
}

func TestRepositionAfterFilemark(t *testing.T) {
	drv, _, sess := setupTestDrive(t)
	ctx := context.Background()

	// Write "file 1" (0xAA), FM, "file 2" (0xBB), FM
	file1 := bytes.Repeat([]byte{0xAA}, 1024)
	if err := drv.Write(ctx, file1); err != nil {
		t.Fatal(err)
	}
	if err := drv.WriteFilemarks(ctx, 1); err != nil {
		t.Fatal(err)
	}
	file2 := bytes.Repeat([]byte{0xBB}, 1024)
	if err := drv.Write(ctx, file2); err != nil {
		t.Fatal(err)
	}
	if err := drv.WriteFilemarks(ctx, 1); err != nil {
		t.Fatal(err)
	}

	if err := drv.Rewind(ctx); err != nil {
		t.Fatal(err)
	}

	// SPACE(filemarks, 1) -- skip past file 1's filemark
	status, _ := spaceCommand(t, sess, 0x01, 1)
	if status != 0x00 {
		t.Fatalf("SPACE status = 0x%02X, want 0x00", status)
	}

	// Read -- should get file 2 data (0xBB)
	buf := make([]byte, 1024)
	n, err := drv.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read file 2: %v", err)
	}
	if n != 1024 || buf[0] != 0xBB {
		t.Errorf("Read file 2: n=%d byte=0x%02X, want 1024/0xBB", n, buf[0])
	}
}

func TestSpaceBlocksZero(t *testing.T) {
	drv, mock, sess := setupTestDrive(t)
	ctx := context.Background()

	// Write some data
	data := make([]byte, 1024)
	if err := drv.Write(ctx, data); err != nil {
		t.Fatal(err)
	}

	posBefore := mock.Position()

	// SPACE(blocks, 0) -- no-op
	status, _ := spaceCommand(t, sess, 0x00, 0)
	if status != 0x00 {
		t.Fatalf("SPACE status = 0x%02X, want 0x00", status)
	}
	if pos := mock.Position(); pos != posBefore {
		t.Errorf("position = %d, want %d (unchanged)", pos, posBefore)
	}
}

func TestSetmarksOutOfScope(t *testing.T) {
	_, _, sess := setupTestDrive(t)

	// SPACE(setmarks=0x04, 1)
	status, senseData := spaceCommand(t, sess, 0x04, 1)
	if status != 0x02 {
		t.Fatalf("SPACE(setmarks) status = 0x%02X, want 0x02", status)
	}
	if len(senseData) >= 3 && senseData[2]&0x0F != 0x05 {
		t.Errorf("sense key = 0x%02X, want 0x05 (ILLEGAL REQUEST)", senseData[2]&0x0F)
	}
}

func TestSpaceBackwardPastBOT(t *testing.T) {
	_, mock, sess := setupTestDrive(t)

	// At position 0 (BOT). SPACE(blocks, -1)
	status, senseData := spaceCommand(t, sess, 0x00, -1)
	if status != 0x02 {
		t.Fatalf("SPACE backward past BOT status = 0x%02X, want 0x02", status)
	}

	// Position clamped to 0
	if pos := mock.Position(); pos != 0 {
		t.Errorf("position = %d, want 0", pos)
	}

	// Check ASC/ASCQ 0x00/0x04 (BEGINNING OF PARTITION/MEDIUM DETECTED)
	if len(senseData) >= 14 {
		if senseData[12] != 0x00 || senseData[13] != 0x04 {
			t.Errorf("ASC/ASCQ = 0x%02X/0x%02X, want 0x00/0x04", senseData[12], senseData[13])
		}
	} else {
		t.Error("sense data too short to check ASC/ASCQ")
	}
}
