package ssc

import (
	"bytes"
	"testing"
)

func TestSpaceCDB_FilemarksForward(t *testing.T) {
	got := SpaceCDB(0x01, 2)
	want := []byte{0x11, 0x01, 0x00, 0x00, 0x02, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("SpaceCDB(0x01, 2) = %X, want %X", got, want)
	}
}

func TestSpaceCDB_BlocksBackward(t *testing.T) {
	got := SpaceCDB(0x00, -1)
	// -1 in 24-bit two's complement: 0xFF, 0xFF, 0xFF
	want := []byte{0x11, 0x00, 0xFF, 0xFF, 0xFF, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("SpaceCDB(0x00, -1) = %X, want %X", got, want)
	}
}

func TestSpaceCDB_EndOfData(t *testing.T) {
	got := SpaceCDB(0x03, 0)
	want := []byte{0x11, 0x03, 0x00, 0x00, 0x00, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("SpaceCDB(0x03, 0) = %X, want %X", got, want)
	}
}

func TestSpaceCDB_BlocksForward(t *testing.T) {
	got := SpaceCDB(0x00, 5)
	want := []byte{0x11, 0x00, 0x00, 0x00, 0x05, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("SpaceCDB(0x00, 5) = %X, want %X", got, want)
	}
}
