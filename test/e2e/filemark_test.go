//go:build e2e

// Package e2e_test contains end-to-end tests for uiscsi-tape.
package e2e_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	tape "github.com/uiscsi/uiscsi-tape"
	"github.com/uiscsi/tapesim"
)

// TestFilemarkDetection verifies the write → filemark → write → rewind → read
// sequence: the first read returns the first record, the second read returns
// ErrFilemark, and the third read returns the second record.
func TestFilemarkDetection(t *testing.T) {
	media := tapesim.NewMedia(10 << 20) // 10 MiB
	tgt, cleanup := SetupTCMUTapeTarget(t, media)
	defer cleanup()

	ctx := context.Background()

	record1 := []byte("record-one")
	record2 := []byte("record-two")

	// Write first record.
	if err := tgt.Drive.Write(ctx, record1); err != nil {
		t.Fatalf("Write record1: %v", err)
	}

	// Write filemark.
	if err := tgt.Drive.WriteFilemarks(ctx, 1); err != nil {
		t.Fatalf("WriteFilemarks: %v", err)
	}

	// Write second record.
	if err := tgt.Drive.Write(ctx, record2); err != nil {
		t.Fatalf("Write record2: %v", err)
	}

	// Rewind to BOT.
	if err := tgt.Drive.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	// Read first record with matching buffer size — tapesim is a flat
	// media without record boundaries, so the buffer must match the
	// expected record size for variable-block reads.
	buf1 := make([]byte, len(record1))
	n, err := tgt.Drive.Read(ctx, buf1)
	if err != nil {
		t.Fatalf("Read record1: unexpected error: %v", err)
	}
	if !bytes.Equal(buf1[:n], record1) {
		t.Fatalf("Read record1: got %q, want %q", buf1[:n], record1)
	}

	// Read past filemark — expect ErrFilemark. Buffer size doesn't matter
	// for filemark detection (zero bytes returned).
	_, err = tgt.Drive.Read(ctx, make([]byte, 1024))
	if !errors.Is(err, tape.ErrFilemark) {
		t.Fatalf("Read filemark: got %v, want ErrFilemark", err)
	}

	// Read second record with matching buffer size.
	buf2 := make([]byte, len(record2))
	n, err = tgt.Drive.Read(ctx, buf2)
	if err != nil {
		t.Fatalf("Read record2: unexpected error: %v", err)
	}
	if !bytes.Equal(buf2[:n], record2) {
		t.Fatalf("Read record2: got %q, want %q", buf2[:n], record2)
	}
}
