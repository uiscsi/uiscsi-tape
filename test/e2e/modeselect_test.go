//go:build e2e

// Package e2e_test contains end-to-end tests for uiscsi-tape.
package e2e_test

import (
	"context"
	"testing"

	"github.com/uiscsi/tapesim"
)

// TestModeSelectSenseRoundtrip verifies that block size and compression
// settings written via MODE SELECT(6) persist and are correctly read back
// via MODE SENSE(6). This exercises the full SCSI mode page path through
// the TCMU tape handler.
func TestModeSelectSenseRoundtrip(t *testing.T) {
	media := tapesim.NewMedia(10 << 20) // 10 MiB
	tgt, cleanup := SetupTCMUTapeTarget(t, media)
	defer cleanup()

	ctx := context.Background()

	// --- Block size roundtrip ---

	// Read initial block size — expect variable-block mode (0).
	bs, err := tgt.Drive.BlockSize(ctx)
	if err != nil {
		t.Fatalf("BlockSize (initial): %v", err)
	}
	t.Logf("initial block size: %d", bs)

	// Set block size to 1024 bytes.
	if err := tgt.Drive.SetBlockSize(ctx, 1024); err != nil {
		t.Fatalf("SetBlockSize(1024): %v", err)
	}

	// Read back and verify.
	bs, err = tgt.Drive.BlockSize(ctx)
	if err != nil {
		t.Fatalf("BlockSize after SetBlockSize(1024): %v", err)
	}
	if bs != 1024 {
		t.Fatalf("SetBlockSize(1024) -> BlockSize() = %d, want 1024", bs)
	}

	// Restore variable-block mode (0).
	if err := tgt.Drive.SetBlockSize(ctx, 0); err != nil {
		t.Fatalf("SetBlockSize(0): %v", err)
	}

	// Read back and verify variable-block mode.
	bs, err = tgt.Drive.BlockSize(ctx)
	if err != nil {
		t.Fatalf("BlockSize after SetBlockSize(0): %v", err)
	}
	if bs != 0 {
		t.Fatalf("SetBlockSize(0) -> BlockSize() = %d, want 0", bs)
	}

	// --- Compression roundtrip ---

	// Read initial compression state.
	dce, dde, err := tgt.Drive.Compression(ctx)
	if err != nil {
		t.Fatalf("Compression (initial): %v", err)
	}
	t.Logf("initial compression: dce=%v dde=%v", dce, dde)

	// Enable both DCE and DDE.
	if err := tgt.Drive.SetCompression(ctx, true, true); err != nil {
		t.Fatalf("SetCompression(true, true): %v", err)
	}

	// Read back and verify both enabled.
	dce, dde, err = tgt.Drive.Compression(ctx)
	if err != nil {
		t.Fatalf("Compression after SetCompression(true, true): %v", err)
	}
	if !dce || !dde {
		t.Fatalf("SetCompression(true, true) -> Compression() = dce=%v dde=%v, want both true", dce, dde)
	}

	// Disable both DCE and DDE.
	if err := tgt.Drive.SetCompression(ctx, false, false); err != nil {
		t.Fatalf("SetCompression(false, false): %v", err)
	}

	// Read back and verify both disabled.
	dce, dde, err = tgt.Drive.Compression(ctx)
	if err != nil {
		t.Fatalf("Compression after SetCompression(false, false): %v", err)
	}
	if dce || dde {
		t.Fatalf("SetCompression(false, false) -> Compression() = dce=%v dde=%v, want both false", dce, dde)
	}
}
