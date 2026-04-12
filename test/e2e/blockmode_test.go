//go:build e2e

// Package e2e_test contains end-to-end tests for uiscsi-tape.
package e2e_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/uiscsi/tapesim"
)

// TestVariableBlockMode verifies that three records of different sizes can be
// written and read back in variable-block mode with exact length and content
// fidelity.
func TestVariableBlockMode(t *testing.T) {
	media := tapesim.NewMedia(10 << 20) // 10 MiB
	tgt, cleanup := SetupTCMUTapeTarget(t, media)
	defer cleanup()

	ctx := context.Background()

	// Three records of distinct sizes and fill patterns.
	rec1 := bytes.Repeat([]byte{0xAA}, 100)
	rec2 := bytes.Repeat([]byte{0xBB}, 500)
	rec3 := bytes.Repeat([]byte{0xCC}, 2000)

	// Write all three records.
	for i, rec := range [][]byte{rec1, rec2, rec3} {
		if err := tgt.Drive.Write(ctx, rec); err != nil {
			t.Fatalf("Write record%d: %v", i+1, err)
		}
	}

	// Rewind to BOT.
	if err := tgt.Drive.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	readBuf := make([]byte, 4096)

	// Read back and verify each record.
	type expectation struct {
		data []byte
	}
	expectations := []expectation{{rec1}, {rec2}, {rec3}}

	for i, ex := range expectations {
		n, err := tgt.Drive.Read(ctx, readBuf)
		if err != nil {
			t.Fatalf("Read record%d: unexpected error: %v", i+1, err)
		}
		if n != len(ex.data) {
			t.Fatalf("Read record%d: got %d bytes, want %d", i+1, n, len(ex.data))
		}
		if !bytes.Equal(readBuf[:n], ex.data) {
			t.Fatalf("Read record%d: content mismatch at %d bytes", i+1, n)
		}
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
