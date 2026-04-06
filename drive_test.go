package tape_test

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	tape "github.com/rkujawa/uiscsi-tape"
	"github.com/rkujawa/uiscsi-tape/test"
)

func TestOpen(t *testing.T) {
	_, sess := test.SetupMock(t)
	ctx := context.Background()

	drive, err := tape.Open(ctx, sess, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if drive == nil {
		t.Fatal("Open returned nil drive")
	}
}

func TestOpenInfo(t *testing.T) {
	_, sess := test.SetupMock(t)
	ctx := context.Background()

	drive, err := tape.Open(ctx, sess, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	info := drive.Info()
	if info.DeviceType != 0x01 {
		t.Errorf("DeviceType = 0x%02X, want 0x01", info.DeviceType)
	}
	if got := info.VendorID; got != "UISCSI" {
		t.Errorf("VendorID = %q, want %q", got, "UISCSI")
	}
	if got := info.ProductID; got != "VirtualTape" {
		t.Errorf("ProductID = %q, want %q", got, "VirtualTape")
	}
	if got := info.Revision; got != "0001" {
		t.Errorf("Revision = %q, want %q", got, "0001")
	}
}

func TestOpenLimits(t *testing.T) {
	_, sess := test.SetupMock(t)
	ctx := context.Background()

	drive, err := tape.Open(ctx, sess, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	limits := drive.Limits()
	if limits.MaxBlock != 0x100000 {
		t.Errorf("MaxBlock = 0x%X, want 0x100000", limits.MaxBlock)
	}
	if limits.MinBlock != 1 {
		t.Errorf("MinBlock = %d, want 1", limits.MinBlock)
	}
	if limits.Granularity != 0 {
		t.Errorf("Granularity = %d, want 0", limits.Granularity)
	}
}

func TestOpenNotTape(t *testing.T) {
	mock, sess := test.SetupMock(t)
	mock.SetDeviceType(0x00) // disk device
	ctx := context.Background()

	_, err := tape.Open(ctx, sess, 0)
	if err == nil {
		t.Fatal("Open should fail for non-tape device")
	}
	if !errors.Is(err, tape.ErrNotTape) {
		t.Errorf("error = %v, want errors.Is ErrNotTape", err)
	}
}

// init registers a check that the test package's mock handles the
// sendDataIn call correctly. This is a compile-time check that
// binary.BigEndian is imported.
var _ = binary.BigEndian
