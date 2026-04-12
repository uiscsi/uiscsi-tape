//go:build e2e

// Package e2e_test contains end-to-end tests for uiscsi-tape.
package e2e_test

import (
	"context"
	"errors"
	"testing"

	"github.com/uiscsi/tapesim"
	tape "github.com/uiscsi/uiscsi-tape"
)

// TestEOMEarlyWarning verifies that writing to a small media with a low EOM
// threshold eventually returns ErrEOM. Uses 64 KiB of total media capacity
// with an EOM threshold at 48 KiB; writes 4 KiB blocks until EOM fires.
func TestEOMEarlyWarning(t *testing.T) {
	// 64 KiB capacity, EOM early warning at 48 KiB.
	media := tapesim.NewMedia(64*1024, tapesim.WithEOMThreshold(48*1024))
	tgt, cleanup := SetupTCMUTapeTarget(t, media)
	defer cleanup()

	ctx := context.Background()

	block := make([]byte, 4096) // 4 KiB per write
	var gotEOM bool
	// 20 iterations * 4 KiB = 80 KiB, exceeds the 64 KiB capacity.
	// EOM early warning should fire around the 12th write (48 KiB).
	for i := 0; i < 20; i++ {
		err := tgt.Drive.Write(ctx, block)
		if errors.Is(err, tape.ErrEOM) {
			gotEOM = true
			break
		}
		if err != nil {
			t.Fatalf("Write #%d: %v", i, err)
		}
	}

	if !gotEOM {
		t.Fatal("expected EOM early warning but wrote all blocks without error")
	}
}
