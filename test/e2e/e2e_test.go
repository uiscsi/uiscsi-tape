//go:build e2e

// Package e2e_test contains end-to-end tests that exercise the uiscsi-tape
// public API against a real TCMU-backed LIO iSCSI target configured via
// configfs. These tests require root privileges and loaded kernel modules:
// target_core_user, target_core_mod, and iscsi_target_mod.
//
// Run with:
//
//	sudo go test -tags e2e -v -count=1 ./test/e2e/
package e2e_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tcmu "github.com/uiscsi/go-tcmu"
	"github.com/uiscsi/tapesim"
	tcmutarget "github.com/uiscsi/tapesim-tcmu"
	"github.com/uiscsi/uiscsi"
	"github.com/uiscsi/uiscsi/test/lio"
	tape "github.com/uiscsi/uiscsi-tape"
)

const initiatorIQN = "iqn.2026-04.com.uiscsi.e2e:tape-initiator"

// hbaCounter provides unique HBA IDs for TCMU devices across parallel test runs.
// Starts at 100 to avoid collisions with go-tcmu's default HBA 30.
var hbaCounter atomic.Int32

func init() { hbaCounter.Store(100) }

// allocateHBA returns a unique HBA ID for a new TCMU device.
func allocateHBA() int { return int(hbaCounter.Add(1)) }

// TCMUTapeTarget holds all the layers of the E2E test stack:
// tapesim.Media -> TapeHandler -> TCMU Device -> LIO Target -> uiscsi Session -> tape.Drive.
type TCMUTapeTarget struct {
	Drive   *tape.Drive
	Media   *tapesim.Media
	Device  *tcmu.Device
	Session *uiscsi.Session
	Target  *lio.Target
}

// RequireTCMUModules skips the test if any of the three required kernel modules
// for TCMU + LIO iSCSI are not loaded. Checks target_core_user, target_core_mod,
// and iscsi_target_mod.
func RequireTCMUModules(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile("/proc/modules")
	if err != nil {
		t.Skipf("cannot read /proc/modules: %v", err)
	}
	content := string(data)
	for _, mod := range []string{"target_core_user", "target_core_mod", "iscsi_target_mod"} {
		if !strings.Contains(content, mod) {
			t.Skipf("kernel module %s not loaded — E2E tape tests require TCMU support", mod)
		}
	}
}

// SetupTCMUTapeTarget assembles a full E2E test stack from a pre-built
// tapesim.Media. It:
//  1. Requires root and TCMU kernel modules (skips if not met)
//  2. Creates a TCMU device backed by media via tcmu.OpenTCMUDevice
//  3. Creates an LIO iSCSI target via lio.Setup, linking the TCMU backstore
//  4. Dials a uiscsi Session to the LIO portal
//  5. Opens a tape.Drive over the session
//
// The returned cleanup function tears down all resources in reverse order:
// Drive.Close -> Session.Close -> LIO teardown -> Device.Close.
// On any setup error, already-created resources are cleaned up before t.Fatalf.
//
// The caller controls timing of the cleanup call; it is NOT registered with
// t.Cleanup automatically.
func SetupTCMUTapeTarget(t *testing.T, media *tapesim.Media) (*TCMUTapeTarget, func()) {
	t.Helper()
	lio.RequireRoot(t)
	RequireTCMUModules(t)

	hba := allocateHBA()
	volName := fmt.Sprintf("tape-e2e-%04d", hba)

	ctx := context.Background()

	// Create TCMU device — must happen BEFORE lio.Setup because the backstore
	// configfs entry must exist before LIO enables the TPG and resolves the LUN.
	handler := &tcmu.SCSIHandler{
		VolumeName: volName,
		HBA:        hba,
		LUN:        0,
		WWN: tcmu.NaaWWN{
			OUI:      "000000",
			VendorID: tcmu.GenerateSerial(volName),
		},
		DataSizes: tcmu.DataSizes{
			VolumeSize: 1 << 30, // 1 GiB sentinel (variable-block tape ignores this)
			BlockSize:  1,       // hw_block_size=1 required for variable-block tape
		},
		DevReady:       tcmutarget.NewTapeDevReady(media),
		ExternalFabric: true,
	}

	dev, err := tcmu.OpenTCMUDevice(ctx, "/dev", handler)
	if err != nil {
		t.Fatalf("OpenTCMUDevice %s: %v", volName, err)
	}

	// Wire TCMU backstore into LIO fabric. The backstore path is the configfs
	// directory created by OpenTCMUDevice, e.g.
	// /sys/kernel/config/target/core/user_101/tape-e2e-0101.
	tgt, lioCleanup := lio.Setup(t, lio.Config{
		TargetSuffix:      volName,
		InitiatorIQN:      initiatorIQN,
		TCMUBackstorePath: dev.BackstorePath(),
	})

	// Dial iSCSI session to the LIO portal.
	sess, err := uiscsi.Dial(ctx, tgt.Addr,
		uiscsi.WithTarget(tgt.IQN),
		uiscsi.WithInitiatorName(initiatorIQN),
		uiscsi.WithMaxRecvDataSegmentLength(262144),
	)
	if err != nil {
		lioCleanup()
		dev.Close()
		t.Fatalf("Dial %s: %v", tgt.Addr, err)
	}

	// Open tape drive over the iSCSI session.
	drv, err := tape.Open(ctx, sess, 0)
	if err != nil {
		sess.Close()
		lioCleanup()
		dev.Close()
		t.Fatalf("tape.Open: %v", err)
	}

	cleanup := func() {
		drv.Close(ctx)
		sess.Close()
		lioCleanup()
		dev.Close()
	}

	return &TCMUTapeTarget{
		Drive:   drv,
		Media:   media,
		Device:  dev,
		Session: sess,
		Target:  tgt,
	}, cleanup
}

// sweepTCMUBackstores removes any stale TCMU configfs backstores that match the
// tape-e2e- prefix. These can be left behind if a previous test run crashed
// before cleanup. Errors are logged but not fatal — cleanup is best-effort.
//
// Scans /sys/kernel/config/target/core/user_* for tape-e2e-* entries.
func sweepTCMUBackstores() {
	coreBase := "/sys/kernel/config/target/core"
	dirs, err := os.ReadDir(coreBase)
	if err != nil {
		// configfs not available — nothing to sweep.
		return
	}
	for _, d := range dirs {
		if !strings.HasPrefix(d.Name(), "user_") {
			continue
		}
		hbaDir := filepath.Join(coreBase, d.Name())
		entries, err := os.ReadDir(hbaDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "tape-e2e-") {
				continue
			}
			bsDir := filepath.Join(hbaDir, e.Name())
			// Disable before removal — some kernels resist removing enabled backstores.
			if err := os.WriteFile(filepath.Join(bsDir, "enable"), []byte("0"), 0o644); err != nil {
				// Ignore EBUSY/ENOENT — may already be disabled or being torn down.
			}
			if err := os.Remove(bsDir); err != nil {
				fmt.Fprintf(os.Stderr, "sweepTCMUBackstores: remove %s: %v\n", bsDir, err)
			}
		}
		// Attempt to remove empty user_* HBA dir (ignore error — may still have entries).
		_ = os.Remove(hbaDir)
	}
}

// TestMain cleans up any stale LIO targets and TCMU backstores from previous
// crashed runs before executing the test suite.
func TestMain(m *testing.M) {
	lio.SweepOrphans()
	sweepTCMUBackstores()
	os.Exit(m.Run())
}
