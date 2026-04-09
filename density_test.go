package tape_test

import (
	"testing"

	tape "github.com/uiscsi/uiscsi-tape"
)

func TestDensityName(t *testing.T) {
	tests := []struct {
		code uint8
		want string
	}{
		{0x00, "Default"},
		{0x26, "DDS-4"},
		{0x46, "LTO-4"},
		{0x58, "LTO-5"},
		{0x5A, "LTO-6"},
		{0x5C, "LTO-7"},
		{0x5E, "LTO-8"},
		{0x60, "LTO-9"},
		{0x42, "LTO-2"},
		{0x44, "LTO-3"},
		{0x13, "DDS-2"},
		{0x47, "DAT-72"},
		{0x48, "DAT-160"},
		{0x51, "3592-J1A"},
		{0x29, "3480"},
	}
	for _, tt := range tests {
		name := tape.DensityName(tt.code)
		if name != tt.want {
			t.Errorf("DensityName(0x%02X) = %q, want %q", tt.code, name, tt.want)
		}
	}
}

func TestDensityNameUnknown(t *testing.T) {
	name := tape.DensityName(0xFE)
	want := "Unknown (0xFE)"
	if name != want {
		t.Errorf("DensityName(0xFE) = %q, want %q", name, want)
	}
}
