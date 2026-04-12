//go:build e2e

// Package e2e_test contains end-to-end tests for uiscsi-tape.
package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"testing"

	"github.com/uiscsi/tapesim"
)

// TestWriteReadRoundtrip verifies that data written to a TCMU-backed tape drive
// can be rewound and read back byte-for-byte. Uses 32 KiB of cryptographically
// random data to ensure no accidental passes from zero-filled buffers.
func TestWriteReadRoundtrip(t *testing.T) {
	media := tapesim.NewMedia(10 << 20) // 10 MiB
	tgt, cleanup := SetupTCMUTapeTarget(t, media)
	defer cleanup()

	ctx := context.Background()

	// Generate 32 KiB of random data.
	data := make([]byte, 32*1024)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	// Write record.
	if err := tgt.Drive.Write(ctx, data); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Rewind to BOT.
	if err := tgt.Drive.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	// Read back.
	buf := make([]byte, len(data))
	n, err := tgt.Drive.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if n != len(data) {
		t.Fatalf("Read returned %d bytes, want %d", n, len(data))
	}

	if !bytes.Equal(data, buf[:n]) {
		t.Fatalf("data mismatch: wrote %d bytes, read %d bytes; contents differ", len(data), n)
	}
}
