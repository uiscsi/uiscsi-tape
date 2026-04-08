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

func pipelineCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestReadAheadBasic(t *testing.T) {
	mock, sess := test.SetupMock(t)
	_ = mock
	ctx := pipelineCtx(t)

	drive, err := tape.Open(ctx, sess, 0, tape.WithReadAhead(4))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer drive.Close(ctx)

	// Write 10 records + filemark. Records must be ≤ MRDSL (8192 default)
	// because the mock sends entire records in a single Data-In PDU.
	const recSize = 4096
	for i := range 10 {
		data := bytes.Repeat([]byte{byte(i)}, recSize)
		if err := drive.Write(ctx, data); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if err := drive.WriteFilemarks(ctx, 1); err != nil {
		t.Fatalf("WriteFilemarks: %v", err)
	}
	if err := drive.Rewind(ctx); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	// Read all 10 back with pipeline.
	for i := range 10 {
		buf := make([]byte, recSize)
		n, err := drive.Read(ctx, buf)
		if err != nil {
			t.Fatalf("Read %d: %v", i, err)
		}
		if n != recSize {
			t.Fatalf("Read %d: n=%d, want %d", i, n, recSize)
		}
		want := bytes.Repeat([]byte{byte(i)}, recSize)
		if !bytes.Equal(buf[:n], want) {
			t.Fatalf("Read %d: data mismatch", i)
		}
	}

	// Next read should hit filemark.
	buf := make([]byte, recSize)
	_, err = drive.Read(ctx, buf)
	if !errors.Is(err, tape.ErrFilemark) {
		t.Fatalf("expected ErrFilemark, got: %v", err)
	}
}

func TestReadAheadFilemark(t *testing.T) {
	mock, sess := test.SetupMock(t)
	_ = mock
	ctx := pipelineCtx(t)

	drive, err := tape.Open(ctx, sess, 0, tape.WithReadAhead(4))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer drive.Close(ctx)

	// Write: 3 records, filemark, 2 records, filemark.
	for i := range 3 {
		drive.Write(ctx, bytes.Repeat([]byte{byte(i)}, 256))
	}
	drive.WriteFilemarks(ctx, 1)
	for i := range 2 {
		drive.Write(ctx, bytes.Repeat([]byte{byte(10 + i)}, 256))
	}
	drive.WriteFilemarks(ctx, 1)
	drive.Rewind(ctx)

	// Read first 3 records.
	for i := range 3 {
		buf := make([]byte, 256)
		n, err := drive.Read(ctx, buf)
		if err != nil {
			t.Fatalf("Read file1 record %d: %v", i, err)
		}
		if n != 256 {
			t.Fatalf("Read file1 record %d: n=%d", i, n)
		}
	}

	// Hit filemark.
	buf := make([]byte, 256)
	_, err = drive.Read(ctx, buf)
	if !errors.Is(err, tape.ErrFilemark) {
		t.Fatalf("expected ErrFilemark after file1, got: %v", err)
	}

	// Read second batch (pipeline should restart).
	for i := range 2 {
		n, err := drive.Read(ctx, buf)
		if err != nil {
			t.Fatalf("Read file2 record %d: %v", i, err)
		}
		if n != 256 {
			t.Fatalf("Read file2 record %d: n=%d", i, n)
		}
	}

	// Second filemark.
	_, err = drive.Read(ctx, buf)
	if !errors.Is(err, tape.ErrFilemark) {
		t.Fatalf("expected ErrFilemark after file2, got: %v", err)
	}
}

func TestReadAheadBlankCheck(t *testing.T) {
	mock, sess := test.SetupMock(t)
	_ = mock
	ctx := pipelineCtx(t)

	drive, err := tape.Open(ctx, sess, 0, tape.WithReadAhead(4))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer drive.Close(ctx)

	// Empty tape — should get blank check.
	buf := make([]byte, 256)
	_, err = drive.Read(ctx, buf)
	if !errors.Is(err, tape.ErrBlankCheck) {
		t.Fatalf("expected ErrBlankCheck, got: %v", err)
	}
}

func TestReadAheadZeroDepth(t *testing.T) {
	mock, sess := test.SetupMock(t)
	_ = mock
	ctx := pipelineCtx(t)

	// WithReadAhead(0) = disabled, same as no option.
	drive, err := tape.Open(ctx, sess, 0, tape.WithReadAhead(0))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer drive.Close(ctx)

	drive.Write(ctx, []byte("sync test"))
	drive.WriteFilemarks(ctx, 1)
	drive.Rewind(ctx)

	buf := make([]byte, 256)
	n, err := drive.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != "sync test" {
		t.Fatalf("data: got %q", buf[:n])
	}
}

func TestReadAheadWriteInvalidates(t *testing.T) {
	mock, sess := test.SetupMock(t)
	_ = mock
	ctx := pipelineCtx(t)

	drive, err := tape.Open(ctx, sess, 0, tape.WithReadAhead(4))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer drive.Close(ctx)

	// Write data, rewind, read one record to start pipeline.
	drive.Write(ctx, []byte("record1"))
	drive.Write(ctx, []byte("record2"))
	drive.WriteFilemarks(ctx, 1)
	drive.Rewind(ctx)

	buf := make([]byte, 256)
	_, err = drive.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	// Write should stop the pipeline.
	drive.Write(ctx, []byte("new data"))
	// No crash, no hang = pipeline was stopped correctly.
}

func TestReadAheadRewindInvalidates(t *testing.T) {
	mock, sess := test.SetupMock(t)
	_ = mock
	ctx := pipelineCtx(t)

	drive, err := tape.Open(ctx, sess, 0, tape.WithReadAhead(4))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer drive.Close(ctx)

	drive.Write(ctx, bytes.Repeat([]byte{0xAA}, 256))
	drive.WriteFilemarks(ctx, 1)
	drive.Rewind(ctx)

	buf := make([]byte, 256)
	drive.Read(ctx, buf) // start pipeline

	// Rewind should stop and restart pipeline.
	drive.Rewind(ctx)

	n, err := drive.Read(ctx, buf)
	if err != nil {
		t.Fatalf("Read after rewind: %v", err)
	}
	if n != 256 {
		t.Fatalf("n=%d, want 256", n)
	}
}

func TestReadAheadClose(t *testing.T) {
	mock, sess := test.SetupMock(t)
	_ = mock
	ctx := pipelineCtx(t)

	drive, err := tape.Open(ctx, sess, 0, tape.WithReadAhead(4))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	drive.Write(ctx, []byte("data"))
	drive.WriteFilemarks(ctx, 1)
	drive.Rewind(ctx)

	buf := make([]byte, 256)
	drive.Read(ctx, buf) // start pipeline

	// Close should stop pipeline cleanly.
	if err := drive.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
