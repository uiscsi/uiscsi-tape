//go:build e2e

// Package e2e_test contains end-to-end tests for uiscsi-tape.
package e2e_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/uiscsi/tapesim"
	tape "github.com/uiscsi/uiscsi-tape"
)

// spaceCDB constructs a SPACE(6) CDB (opcode 0x11) per SSC-3 §7.10.
// code selects the space type: 0x00=blocks, 0x01=filemarks, 0x03=end-of-data.
// count is a signed 24-bit value (two's complement) indicating how far to space.
func spaceCDB(code uint8, count int32) []byte {
	cdb := make([]byte, 6)
	cdb[0] = 0x11 // SPACE(6) opcode
	cdb[1] = code & 0x07
	// 24-bit two's complement count in bytes 2-4 (big-endian)
	u := uint32(count) & 0x00FFFFFF
	cdb[2] = byte(u >> 16)
	cdb[3] = byte(u >> 8)
	cdb[4] = byte(u)
	return cdb
}

// doSpace issues a SPACE(6) command via raw CDB and fatals on any error
// or non-GOOD SCSI status.
func doSpace(t *testing.T, tgt *TCMUTapeTarget, code uint8, count int32) {
	t.Helper()
	ctx := context.Background()
	result, err := tgt.Session.Raw().Execute(ctx, 0, spaceCDB(code, count))
	if err != nil {
		t.Fatalf("SPACE(0x%02x, %d): %v", code, count, err)
	}
	if result.Status != 0 {
		t.Fatalf("SPACE(0x%02x, %d): status=0x%02X sense=%x", code, count, result.Status, result.SenseData)
	}
}

// TestSpaceByBlocks verifies SPACE forward and backward by block count.
// It writes 5 distinct 512-byte blocks, rewinds, reads 2 blocks to reach
// block 2, spaces forward 2 blocks to reach block 4, verifies the content,
// then spaces backward 3 blocks to reach block 2 and verifies again.
func TestSpaceByBlocks(t *testing.T) {
	media := tapesim.NewMedia(10 << 20) // 10 MiB
	tgt, cleanup := SetupTCMUTapeTarget(t, media)
	defer cleanup()

	ctx := context.Background()

	// Use fixed 512-byte block size for deterministic block numbering.
	if err := tgt.Drive.SetBlockSize(ctx, 512); err != nil {
		t.Fatalf("SetBlockSize(512): %v", err)
	}

	// Write 5 blocks with distinct fill patterns (0x01 through 0x05).
	const blockSize = 512
	for i := 1; i <= 5; i++ {
		block := bytes.Repeat([]byte{byte(i)}, blockSize)
		if err := tgt.Drive.Write(ctx, block); err != nil {
			t.Fatalf("Write block %d: %v", i, err)
		}
	}

	// Rewind to beginning of tape.
	if err := tgt.Drive.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	// Read 2 blocks to advance to block 2.
	buf := make([]byte, blockSize)
	for i := 0; i < 2; i++ {
		if _, err := tgt.Drive.Read(ctx, buf); err != nil {
			t.Fatalf("Read block %d: %v", i, err)
		}
	}

	// SPACE forward 2 blocks — now at block 4 (zero-indexed).
	doSpace(t, tgt, 0x00, 2)

	// Read block 5 (fill pattern 0x05) and verify.
	n, err := tgt.Drive.Read(ctx, buf)
	if err != nil && !errors.Is(err, tape.ErrILI) {
		t.Fatalf("Read block 5: %v", err)
	}
	if n != blockSize {
		t.Fatalf("Read block 5: got %d bytes, want %d", n, blockSize)
	}
	if buf[0] != 0x05 {
		t.Errorf("Read block 5: first byte=0x%02X, want 0x05", buf[0])
	}

	// SPACE backward 3 blocks — now at block 2 (zero-indexed), which
	// contains fill pattern 0x03.
	doSpace(t, tgt, 0x00, -3)

	// Read block 3 (fill pattern 0x03) and verify.
	n, err = tgt.Drive.Read(ctx, buf)
	if err != nil && !errors.Is(err, tape.ErrILI) {
		t.Fatalf("Read block 3: %v", err)
	}
	if n != blockSize {
		t.Fatalf("Read block 3: got %d bytes, want %d", n, blockSize)
	}
	if buf[0] != 0x03 {
		t.Errorf("Read block 3: first byte=0x%02X, want 0x03", buf[0])
	}
}

// TestSpaceByFilemarks verifies that SPACE forward by one filemark
// positions the tape past the filemark so that the next read returns
// data from the following file.
func TestSpaceByFilemarks(t *testing.T) {
	media := tapesim.NewMedia(10 << 20) // 10 MiB
	tgt, cleanup := SetupTCMUTapeTarget(t, media)
	defer cleanup()

	ctx := context.Background()

	// Write: data1 (0xAA-filled), filemark, data2 (0xBB-filled), filemark, data3 (0xCC-filled).
	const recSize = 4096
	data1 := bytes.Repeat([]byte{0xAA}, recSize)
	data2 := bytes.Repeat([]byte{0xBB}, recSize)
	data3 := bytes.Repeat([]byte{0xCC}, recSize)

	if err := tgt.Drive.Write(ctx, data1); err != nil {
		t.Fatalf("Write data1: %v", err)
	}
	if err := tgt.Drive.WriteFilemarks(ctx, 1); err != nil {
		t.Fatalf("WriteFilemarks 1: %v", err)
	}
	if err := tgt.Drive.Write(ctx, data2); err != nil {
		t.Fatalf("Write data2: %v", err)
	}
	if err := tgt.Drive.WriteFilemarks(ctx, 1); err != nil {
		t.Fatalf("WriteFilemarks 2: %v", err)
	}
	if err := tgt.Drive.Write(ctx, data3); err != nil {
		t.Fatalf("Write data3: %v", err)
	}

	// Rewind to beginning of tape.
	if err := tgt.Drive.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	// SPACE forward 1 filemark — positions tape past the first filemark.
	doSpace(t, tgt, 0x01, 1)

	// Next read should return data2 (0xBB-filled).
	buf := make([]byte, recSize)
	n, err := tgt.Drive.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read after SPACE filemark: %v", err)
	}
	if n != recSize {
		t.Fatalf("Read after SPACE filemark: got %d bytes, want %d", n, recSize)
	}
	if buf[0] != 0xBB {
		t.Errorf("Read after SPACE filemark: first byte=0x%02X, want 0xBB (data2)", buf[0])
	}
}
