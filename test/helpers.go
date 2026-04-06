package test

import (
	"context"
	"testing"

	"github.com/rkujawa/uiscsi"
)

// SetupMock creates a MockTapeDrive with 1 MiB media and returns it along
// with a connected uiscsi.Session. Both must be closed after the test;
// t.Cleanup handles this automatically.
func SetupMock(t *testing.T) (*MockTapeDrive, *uiscsi.Session) {
	t.Helper()

	mock := NewMockTapeDrive(1 << 20) // 1 MiB
	t.Cleanup(func() { mock.Close() })

	ctx := context.Background()
	sess, err := uiscsi.Dial(ctx, mock.Addr(),
		uiscsi.WithTarget("iqn.2026-04.test:tape"),
	)
	if err != nil {
		mock.Close()
		t.Fatalf("SetupMock: dial failed: %v", err)
	}
	t.Cleanup(func() { sess.Close() })

	return mock, sess
}
