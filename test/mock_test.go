package test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/uiscsi/uiscsi"
	"github.com/uiscsi/uiscsi-tape/internal/ssc"
)

func TestMockTUR(t *testing.T) {
	_, sess := SetupMock(t)

	ctx := context.Background()
	if err := sess.TestUnitReady(ctx, 0); err != nil {
		t.Fatalf("TestUnitReady: %v", err)
	}
}

func TestMockInquiry(t *testing.T) {
	_, sess := SetupMock(t)

	ctx := context.Background()
	inq, err := sess.Inquiry(ctx, 0)
	if err != nil {
		t.Fatalf("Inquiry: %v", err)
	}

	if inq.DeviceType != 0x01 {
		t.Errorf("DeviceType = 0x%02X, want 0x01 (tape)", inq.DeviceType)
	}
	if !strings.Contains(inq.VendorID, "UISCSI") {
		t.Errorf("VendorID = %q, want to contain \"UISCSI\"", inq.VendorID)
	}
	if !strings.Contains(inq.ProductID, "VirtualTape") {
		t.Errorf("ProductID = %q, want to contain \"VirtualTape\"", inq.ProductID)
	}
}

func TestMockReadBlockLimits(t *testing.T) {
	_, sess := SetupMock(t)

	ctx := context.Background()
	result, err := sess.Execute(ctx, 0, ssc.ReadBlockLimitsCDB(), uiscsi.WithDataIn(6))
	if err != nil {
		t.Fatalf("Execute READ BLOCK LIMITS: %v", err)
	}

	if result.Status != 0 {
		t.Fatalf("Status = 0x%02X, want 0x00 (GOOD)", result.Status)
	}

	if len(result.Data) < 6 {
		t.Fatalf("Data length = %d, want >= 6", len(result.Data))
	}

	bl, err := ssc.ParseReadBlockLimits(result.Data)
	if err != nil {
		t.Fatalf("ParseReadBlockLimits: %v", err)
	}

	if bl.MaxBlock != 0x100000 {
		t.Errorf("MaxBlock = 0x%X, want 0x100000", bl.MaxBlock)
	}
	if bl.MinBlock != 1 {
		t.Errorf("MinBlock = %d, want 1", bl.MinBlock)
	}
}

func TestMockPosition(t *testing.T) {
	mock, _ := SetupMock(t)

	if pos := mock.Position(); pos != 0 {
		t.Errorf("initial position = %d, want 0", pos)
	}
}

func TestMockWrite(t *testing.T) {
	mock, sess := SetupMock(t)

	ctx := context.Background()
	data := []byte("hello tape world!")
	cdb := ssc.WriteCDB(false, uint32(len(data)))

	result, err := sess.Execute(ctx, 0, cdb, uiscsi.WithDataOut(bytes.NewReader(data), uint32(len(data))))
	if err != nil {
		t.Fatalf("Execute WRITE: %v", err)
	}
	if result.Status != 0 {
		t.Fatalf("Status = 0x%02X, want 0x00 (GOOD)", result.Status)
	}
	if pos := mock.Position(); pos != len(data) {
		t.Errorf("Position = %d, want %d", pos, len(data))
	}
}

func TestMockRead(t *testing.T) {
	mock, sess := SetupMock(t)

	ctx := context.Background()
	data := []byte("read me back")
	writeCDB := ssc.WriteCDB(false, uint32(len(data)))

	// Write data
	_, err := sess.Execute(ctx, 0, writeCDB, uiscsi.WithDataOut(bytes.NewReader(data), uint32(len(data))))
	if err != nil {
		t.Fatalf("Execute WRITE: %v", err)
	}

	// Rewind
	rewindCDB := ssc.RewindCDB(false)
	_, err = sess.Execute(ctx, 0, rewindCDB)
	if err != nil {
		t.Fatalf("Execute REWIND: %v", err)
	}
	if pos := mock.Position(); pos != 0 {
		t.Fatalf("Position after rewind = %d, want 0", pos)
	}

	// Read back
	readCDB := ssc.ReadCDB(false, false, uint32(len(data)))
	result, err := sess.Execute(ctx, 0, readCDB, uiscsi.WithDataIn(uint32(len(data))))
	if err != nil {
		t.Fatalf("Execute READ: %v", err)
	}
	if result.Status != 0 {
		t.Fatalf("Status = 0x%02X, want 0x00 (GOOD)", result.Status)
	}
	if !bytes.Equal(result.Data, data) {
		t.Errorf("Read data = %q, want %q", result.Data, data)
	}
}

func TestMockWriteFilemarks(t *testing.T) {
	mock, sess := SetupMock(t)

	ctx := context.Background()

	// Write some data
	data := []byte("before filemark")
	writeCDB := ssc.WriteCDB(false, uint32(len(data)))
	_, err := sess.Execute(ctx, 0, writeCDB, uiscsi.WithDataOut(bytes.NewReader(data), uint32(len(data))))
	if err != nil {
		t.Fatalf("Execute WRITE: %v", err)
	}

	fmPos := mock.Position()

	// Write a filemark
	fmCDB := ssc.WriteFilemarksCDB(1)
	result, err := sess.Execute(ctx, 0, fmCDB)
	if err != nil {
		t.Fatalf("Execute WRITE FILEMARKS: %v", err)
	}
	if result.Status != 0 {
		t.Fatalf("WRITE FILEMARKS Status = 0x%02X, want 0x00", result.Status)
	}

	// Rewind and seek to filemark position
	rewindCDB := ssc.RewindCDB(false)
	_, err = sess.Execute(ctx, 0, rewindCDB)
	if err != nil {
		t.Fatalf("Execute REWIND: %v", err)
	}

	// Read past the data to reach the filemark position
	skipCDB := ssc.ReadCDB(false, false, uint32(fmPos))
	_, err = sess.Execute(ctx, 0, skipCDB, uiscsi.WithDataIn(uint32(fmPos)))
	if err != nil {
		t.Fatalf("Execute READ (skip): %v", err)
	}

	// Now read at filemark position -- should get CHECK CONDITION with filemark sense
	readCDB := ssc.ReadCDB(false, false, 1)
	result, err = sess.Execute(ctx, 0, readCDB, uiscsi.WithDataIn(1))
	if err != nil {
		t.Fatalf("Execute READ at filemark: %v", err)
	}
	if result.Status != 0x02 {
		t.Errorf("Status at filemark = 0x%02X, want 0x02 (CHECK CONDITION)", result.Status)
	}
	// Verify filemark bit in sense data (byte 2 bit 7)
	if len(result.SenseData) >= 3 {
		if result.SenseData[2]&0x80 == 0 {
			t.Errorf("Filemark bit not set in sense byte 2: 0x%02X", result.SenseData[2])
		}
	} else {
		t.Errorf("No sense data returned at filemark")
	}
}

func TestMockRewind(t *testing.T) {
	mock, sess := SetupMock(t)

	ctx := context.Background()
	data := []byte("some data")
	writeCDB := ssc.WriteCDB(false, uint32(len(data)))

	_, err := sess.Execute(ctx, 0, writeCDB, uiscsi.WithDataOut(bytes.NewReader(data), uint32(len(data))))
	if err != nil {
		t.Fatalf("Execute WRITE: %v", err)
	}

	if pos := mock.Position(); pos == 0 {
		t.Fatal("Position should be non-zero after write")
	}

	rewindCDB := ssc.RewindCDB(false)
	result, err := sess.Execute(ctx, 0, rewindCDB)
	if err != nil {
		t.Fatalf("Execute REWIND: %v", err)
	}
	if result.Status != 0 {
		t.Fatalf("REWIND Status = 0x%02X, want 0x00", result.Status)
	}
	if pos := mock.Position(); pos != 0 {
		t.Errorf("Position after rewind = %d, want 0", pos)
	}
}

func TestMockEOM(t *testing.T) {
	mock, sess := SetupMock(t)

	ctx := context.Background()

	// Set a very small EOM threshold to trigger early warning quickly
	mock.SetEOMThreshold(100)

	// Write data that crosses EOM threshold but fits in media
	data := make([]byte, 200)
	for i := range data {
		data[i] = byte(i & 0xFF)
	}
	writeCDB := ssc.WriteCDB(false, uint32(len(data)))
	result, err := sess.Execute(ctx, 0, writeCDB, uiscsi.WithDataOut(bytes.NewReader(data), uint32(len(data))))
	if err != nil {
		t.Fatalf("Execute WRITE (EOM): %v", err)
	}

	// Write should succeed but return CHECK CONDITION with EOM sense
	if result.Status != 0x02 {
		t.Fatalf("EOM Status = 0x%02X, want 0x02 (CHECK CONDITION)", result.Status)
	}

	// Verify EOM bit in sense data (byte 2 bit 6)
	if len(result.SenseData) >= 3 {
		if result.SenseData[2]&0x40 == 0 {
			t.Errorf("EOM bit not set in sense byte 2: 0x%02X", result.SenseData[2])
		}
	} else {
		t.Errorf("No sense data returned for EOM")
	}

	// Verify data was actually written
	if mock.Written() != len(data) {
		t.Errorf("Written = %d, want %d", mock.Written(), len(data))
	}
}

func TestMockReadPosition(t *testing.T) {
	_, sess := SetupMock(t)
	ctx := context.Background()

	// Position at BOT should be 0 with BOP=true.
	result, err := sess.Execute(ctx, 0, []byte{0x34, 0, 0, 0, 0, 0, 0, 0, 0, 0}, uiscsi.WithDataIn(20))
	if err != nil {
		t.Fatalf("Execute READ POSITION: %v", err)
	}
	if result.Status != 0 {
		t.Fatalf("Status = 0x%02X, want 0x00", result.Status)
	}
	if len(result.Data) < 20 {
		t.Fatalf("response length = %d, want >= 20", len(result.Data))
	}
	if result.Data[0]&0x80 == 0 {
		t.Error("BOP should be set at position 0")
	}

	// Write some data to advance position.
	data := make([]byte, 4096)
	cdb := []byte{0x0A, 0x00, 0x00, 0x10, 0x00, 0x00} // WRITE(6), 4096 bytes
	_, err = sess.Execute(ctx, 0, cdb, uiscsi.WithDataOut(bytes.NewReader(data), 4096))
	if err != nil {
		t.Fatalf("Execute WRITE: %v", err)
	}

	// Position should be non-zero now.
	result, err = sess.Execute(ctx, 0, []byte{0x34, 0, 0, 0, 0, 0, 0, 0, 0, 0}, uiscsi.WithDataIn(20))
	if err != nil {
		t.Fatalf("Execute READ POSITION after write: %v", err)
	}
	pos := uint32(result.Data[4])<<24 | uint32(result.Data[5])<<16 | uint32(result.Data[6])<<8 | uint32(result.Data[7])
	if pos == 0 {
		t.Error("position should be non-zero after write")
	}
	if result.Data[0]&0x80 != 0 {
		t.Error("BOP should not be set after write")
	}

	// Rewind and check position again.
	_, err = sess.Execute(ctx, 0, []byte{0x01, 0, 0, 0, 0, 0})
	if err != nil {
		t.Fatalf("Execute REWIND: %v", err)
	}

	result, err = sess.Execute(ctx, 0, []byte{0x34, 0, 0, 0, 0, 0, 0, 0, 0, 0}, uiscsi.WithDataIn(20))
	if err != nil {
		t.Fatalf("Execute READ POSITION after rewind: %v", err)
	}
	pos = uint32(result.Data[4])<<24 | uint32(result.Data[5])<<16 | uint32(result.Data[6])<<8 | uint32(result.Data[7])
	if pos != 0 {
		t.Errorf("position after rewind = %d, want 0", pos)
	}
	if result.Data[0]&0x80 == 0 {
		t.Error("BOP should be set after rewind")
	}
}
