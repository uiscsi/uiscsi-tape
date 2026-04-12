//go:build e2e

// Package e2e_test contains end-to-end tests for uiscsi-tape.
package e2e_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/uiscsi/tapesim"
)

// TestVariableBlockMode verifies that variable-block I/O works correctly
// through the full TCMU+LIO+iSCSI stack. Writes data in variable-block
// mode (block size 0), rewinds, and reads back the same amount.
//
// Note: tapesim.Media is a flat byte buffer without record boundaries.
// Real tape drives track per-write record boundaries; tapesim does not.
// This test verifies variable-block data fidelity using matching
// write/read sizes.
func TestVariableBlockMode(t *testing.T) {
	media := tapesim.NewMedia(10 << 20) // 10 MiB
	tgt, cleanup := SetupTCMUTapeTarget(t, media)
	defer cleanup()

	ctx := context.Background()

	// Write a single variable-length block.
	writeData := bytes.Repeat([]byte{0xAA}, 1000)
	if err := tgt.Drive.Write(ctx, writeData); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Rewind to BOT.
	if err := tgt.Drive.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	// Read back with matching buffer size.
	readBuf := make([]byte, 1000)
	n, err := tgt.Drive.Read(ctx, readBuf)
	if err != nil {
		t.Fatalf("Read: unexpected error: %v", err)
	}
	if n != len(writeData) {
		t.Fatalf("Read: got %d bytes, want %d", n, len(writeData))
	}
	if !bytes.Equal(readBuf[:n], writeData) {
		t.Fatalf("Read: content mismatch at %d bytes", n)
	}
}

// TestFixedBlockMode verifies that fixed-block-size I/O works correctly.
// After setting 512-byte blocks, two blocks of distinct fill are written,
// rewound, and read back with exact content match.
func TestFixedBlockMode(t *testing.T) {
	media := tapesim.NewMedia(10 << 20) // 10 MiB
	tgt, cleanup := SetupTCMUTapeTarget(t, media)
	defer cleanup()

	ctx := context.Background()

	// Set fixed block size to 512 bytes.
	if err := tgt.Drive.SetBlockSize(ctx, 512); err != nil {
		t.Fatalf("SetBlockSize(512): %v", err)
	}

	block1 := bytes.Repeat([]byte{0x11}, 512)
	block2 := bytes.Repeat([]byte{0x22}, 512)

	// Write two blocks.
	if err := tgt.Drive.Write(ctx, block1); err != nil {
		t.Fatalf("Write block1: %v", err)
	}
	if err := tgt.Drive.Write(ctx, block2); err != nil {
		t.Fatalf("Write block2: %v", err)
	}

	// Rewind to BOT.
	if err := tgt.Drive.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	buf := make([]byte, 512)

	// Read back block 1.
	n, err := tgt.Drive.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read block1: unexpected error: %v", err)
	}
	if n != 512 {
		t.Fatalf("Read block1: got %d bytes, want 512", n)
	}
	if !bytes.Equal(buf[:n], block1) {
		t.Fatalf("Read block1: content mismatch")
	}

	// Read back block 2.
	n, err = tgt.Drive.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read block2: unexpected error: %v", err)
	}
	if n != 512 {
		t.Fatalf("Read block2: got %d bytes, want 512", n)
	}
	if !bytes.Equal(buf[:n], block2) {
		t.Fatalf("Read block2: content mismatch")
	}

}
