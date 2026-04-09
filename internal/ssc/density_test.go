package ssc

import (
	"encoding/binary"
	"testing"
)

func TestReportDensitySupportCDB(t *testing.T) {
	// Without MEDIA bit.
	cdb := ReportDensitySupportCDB(false, 4096)
	if cdb[0] != 0x44 {
		t.Errorf("opcode = 0x%02X, want 0x44", cdb[0])
	}
	if cdb[1]&0x01 != 0 {
		t.Error("MEDIA bit set, want clear")
	}
	allocLen := binary.BigEndian.Uint16(cdb[7:9])
	if allocLen != 4096 {
		t.Errorf("allocLen = %d, want 4096", allocLen)
	}

	// With MEDIA bit.
	cdb = ReportDensitySupportCDB(true, 8192)
	if cdb[1]&0x01 == 0 {
		t.Error("MEDIA bit clear, want set")
	}
}

func TestParseReportDensitySupport(t *testing.T) {
	t.Run("empty response", func(t *testing.T) {
		// 4-byte header with zero list length.
		data := make([]byte, 4)
		descs, err := ParseReportDensitySupport(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(descs) != 0 {
			t.Errorf("got %d descriptors, want 0", len(descs))
		}
	})

	t.Run("too short", func(t *testing.T) {
		_, err := ParseReportDensitySupport([]byte{0, 0})
		if err == nil {
			t.Fatal("expected error for short response")
		}
	})

	t.Run("single descriptor", func(t *testing.T) {
		// 4-byte header + 52-byte descriptor.
		data := make([]byte, 4+52)
		binary.BigEndian.PutUint16(data[0:2], 52) // list length

		desc := data[4:]
		desc[0] = 0x46       // primary = LTO-4
		desc[1] = 0x00       // secondary
		desc[2] = 0x80       // WRTok=1
		desc[5] = 0x00       // bits per mm (24-bit)
		desc[6] = 0x27
		desc[7] = 0x10       // = 10000
		binary.BigEndian.PutUint16(desc[8:10], 127)    // media width
		binary.BigEndian.PutUint16(desc[10:12], 896)    // tracks
		binary.BigEndian.PutUint32(desc[12:16], 819200) // capacity MB
		copy(desc[16:24], []byte("LTO CVE "))           // organization
		copy(desc[24:44], []byte("LTO-4               ")) // description
		copy(desc[44:52], []byte("U-416   "))           // name

		descs, err := ParseReportDensitySupport(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(descs) != 1 {
			t.Fatalf("got %d descriptors, want 1", len(descs))
		}

		d := descs[0]
		if d.PrimaryCode != 0x46 {
			t.Errorf("PrimaryCode = 0x%02X, want 0x46", d.PrimaryCode)
		}
		if !d.WRTok {
			t.Error("WRTok = false, want true")
		}
		if d.BitsPerMM != 10000 {
			t.Errorf("BitsPerMM = %d, want 10000", d.BitsPerMM)
		}
		if d.MediaWidth != 127 {
			t.Errorf("MediaWidth = %d, want 127", d.MediaWidth)
		}
		if d.Tracks != 896 {
			t.Errorf("Tracks = %d, want 896", d.Tracks)
		}
		if d.Capacity != 819200 {
			t.Errorf("Capacity = %d, want 819200", d.Capacity)
		}
		if d.Assigning != "LTO CVE" {
			t.Errorf("Assigning = %q, want %q", d.Assigning, "LTO CVE")
		}
		if d.Description != "LTO-4" {
			t.Errorf("Description = %q, want %q", d.Description, "LTO-4")
		}
		if d.Name != "U-416" {
			t.Errorf("Name = %q, want %q", d.Name, "U-416")
		}
	})

	t.Run("two descriptors", func(t *testing.T) {
		data := make([]byte, 4+52*2)
		binary.BigEndian.PutUint16(data[0:2], 52*2)

		// First descriptor: LTO-3.
		data[4] = 0x44
		copy(data[4+24:4+44], []byte("LTO-3               "))

		// Second descriptor: LTO-4.
		data[4+52] = 0x46
		copy(data[4+52+24:4+52+44], []byte("LTO-4               "))

		descs, err := ParseReportDensitySupport(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(descs) != 2 {
			t.Fatalf("got %d descriptors, want 2", len(descs))
		}
		if descs[0].PrimaryCode != 0x44 {
			t.Errorf("first descriptor PrimaryCode = 0x%02X, want 0x44", descs[0].PrimaryCode)
		}
		if descs[1].PrimaryCode != 0x46 {
			t.Errorf("second descriptor PrimaryCode = 0x%02X, want 0x46", descs[1].PrimaryCode)
		}
	})

	t.Run("truncated descriptor", func(t *testing.T) {
		// Header says 52 bytes but only 30 present.
		data := make([]byte, 4+30)
		binary.BigEndian.PutUint16(data[0:2], 52)
		descs, err := ParseReportDensitySupport(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Truncated descriptor should be skipped.
		if len(descs) != 0 {
			t.Errorf("got %d descriptors, want 0 (truncated)", len(descs))
		}
	})
}

func TestParseReportDensitySupport_Flags(t *testing.T) {
	data := make([]byte, 4+52)
	binary.BigEndian.PutUint16(data[0:2], 52)
	data[4+2] = 0xF0 // WRTok=1, DUP=1, DefLT=1, DLVALID=1

	descs, err := ParseReportDensitySupport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	d := descs[0]
	if !d.WRTok {
		t.Error("WRTok = false")
	}
	if !d.DUP {
		t.Error("DUP = false")
	}
	if !d.DefLT {
		t.Error("DefLT = false")
	}
	if !d.DLVALID {
		t.Error("DLVALID = false")
	}
}
