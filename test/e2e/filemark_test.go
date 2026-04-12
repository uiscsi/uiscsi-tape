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

	buf := make([]byte, 1024)

	// Read first record — expect "record-one".
	n, err := tgt.Drive.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read record1: unexpected error: %v", err)
	}
	if !bytes.Equal(buf[:n], record1) {
		t.Fatalf("Read record1: got %q, want %q", buf[:n], record1)
	}

	// Read past filemark — expect ErrFilemark.
	_, err = tgt.Drive.Read(ctx, buf)
	if !errors.Is(err, tape.ErrFilemark) {
		t.Fatalf("Read filemark: got %v, want ErrFilemark", err)
	}

	// Read second record — expect "record-two".
	n, err = tgt.Drive.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read record2: unexpected error: %v", err)
	}
	if !bytes.Equal(buf[:n], record2) {
		t.Fatalf("Read record2: got %q, want %q", buf[:n], record2)
	}
}
