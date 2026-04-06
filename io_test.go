package tape_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	tape "github.com/rkujawa/uiscsi-tape"
	"github.com/rkujawa/uiscsi-tape/test"
)

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestWriteAndReadBack(t *testing.T) {
	mock, sess := test.SetupMock(t)
	_ = mock
	ctx := testCtx(t)

	drive, err := tape.Open(ctx, sess, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Write variable-block record.
	testData := []byte("hello tape world, this is a variable-length record")
	if err := drive.Write(ctx, testData); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := drive.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	// Read back.
	buf := make([]byte, 256)
	n, err := drive.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if !bytes.Equal(buf[:n], testData) {
		t.Fatalf("data mismatch: got %q, want %q", buf[:n], testData)
	}
}

func TestWriteAndReadBackFixed(t *testing.T) {
	mock, sess := test.SetupMock(t)
	_ = mock
	ctx := testCtx(t)

	// Mock uses 512-byte fixed blocks.
	drive, err := tape.Open(ctx, sess, 0, tape.WithBlockSize(512))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Write 2 blocks (1024 bytes).
	testData := make([]byte, 1024)
	for i := range testData {
		testData[i] = byte(i & 0xFF)
	}
	if err := drive.Write(ctx, testData); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := drive.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	buf := make([]byte, 1024)
	n, err := drive.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n != 1024 {
		t.Fatalf("Read: got %d bytes, want 1024", n)
	}
	if !bytes.Equal(buf[:n], testData) {
		t.Fatal("data mismatch in fixed-block read")
	}
}

func TestReadFilemark(t *testing.T) {
	mock, sess := test.SetupMock(t)
	_ = mock
	ctx := testCtx(t)

	drive, err := tape.Open(ctx, sess, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Write data, then a filemark.
	if err := drive.Write(ctx, []byte("before filemark")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := drive.WriteFilemarks(ctx, 1); err != nil {
		t.Fatalf("WriteFilemarks: %v", err)
	}

	if err := drive.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	// First read gets the data.
	buf := make([]byte, 256)
	n, err := drive.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read data: %v", err)
	}
	if string(buf[:n]) != "before filemark" {
		t.Fatalf("data: got %q, want %q", buf[:n], "before filemark")
	}

	// Second read hits the filemark.
	_, err = drive.Read(ctx, buf)
	if !errors.Is(err, tape.ErrFilemark) {
		t.Fatalf("expected ErrFilemark, got: %v", err)
	}
}

func TestReadBlankCheck(t *testing.T) {
	mock, sess := test.SetupMock(t)
	_ = mock
	ctx := testCtx(t)

	drive, err := tape.Open(ctx, sess, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Read from empty tape.
	buf := make([]byte, 256)
	_, err = drive.Read(ctx, buf)
	if !errors.Is(err, tape.ErrBlankCheck) {
		t.Fatalf("expected ErrBlankCheck, got: %v", err)
	}
}

func TestWriteEOM(t *testing.T) {
	mock, sess := test.SetupMock(t)
	ctx := testCtx(t)

	// Set EOM threshold low so writing triggers early warning.
	mock.SetEOMThreshold(100)

	drive, err := tape.Open(ctx, sess, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Write data that crosses the EOM threshold.
	data := make([]byte, 200)
	for i := range data {
		data[i] = 0xAA
	}
	err = drive.Write(ctx, data)
	if !errors.Is(err, tape.ErrEOM) {
		t.Fatalf("expected ErrEOM, got: %v", err)
	}

	// Data should still have been written.
	if mock.Written() == 0 {
		t.Fatal("expected data to be written despite EOM warning")
	}
}

func TestWriteFixedBlockSizeMismatch(t *testing.T) {
	mock, sess := test.SetupMock(t)
	_ = mock
	ctx := testCtx(t)

	drive, err := tape.Open(ctx, sess, 0, tape.WithBlockSize(512))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Write 100 bytes — not a multiple of 512.
	err = drive.Write(ctx, make([]byte, 100))
	if err == nil {
		t.Fatal("expected error for non-multiple of block size")
	}
}

func TestRewind(t *testing.T) {
	mock, sess := test.SetupMock(t)
	_ = mock
	ctx := testCtx(t)

	drive, err := tape.Open(ctx, sess, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	testData := []byte("rewind test data")
	if err := drive.Write(ctx, testData); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := drive.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	// Should be able to read from the beginning.
	buf := make([]byte, 256)
	n, err := drive.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read after rewind: %v", err)
	}
	if !bytes.Equal(buf[:n], testData) {
		t.Fatalf("data mismatch after rewind: got %q, want %q", buf[:n], testData)
	}
}

func TestWriteFilemarks(t *testing.T) {
	mock, sess := test.SetupMock(t)
	_ = mock
	ctx := testCtx(t)

	drive, err := tape.Open(ctx, sess, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Write data, then 2 filemarks.
	if err := drive.Write(ctx, []byte("record one")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := drive.WriteFilemarks(ctx, 2); err != nil {
		t.Fatalf("WriteFilemarks: %v", err)
	}

	if err := drive.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	// Read the data record.
	buf := make([]byte, 256)
	n, err := drive.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != "record one" {
		t.Fatalf("data: got %q, want %q", buf[:n], "record one")
	}

	// Next read hits the first filemark.
	_, err = drive.Read(ctx, buf)
	if !errors.Is(err, tape.ErrFilemark) {
		t.Fatalf("expected ErrFilemark, got: %v", err)
	}
}

func TestReadBufferTooSmall(t *testing.T) {
	mock, sess := test.SetupMock(t)
	_ = mock
	ctx := testCtx(t)

	drive, err := tape.Open(ctx, sess, 0, tape.WithBlockSize(512))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Buffer smaller than one block.
	buf := make([]byte, 100)
	_, err = drive.Read(ctx, buf)
	if err == nil {
		t.Fatal("expected error for buffer too small")
	}
}
