package ssc

import "testing"

func TestReadBlockLimitsCDB(t *testing.T) {
	cdb := ReadBlockLimitsCDB()
	want := []byte{0x05, 0x00, 0x00, 0x00, 0x00, 0x00}
	if len(cdb) != len(want) {
		t.Fatalf("ReadBlockLimitsCDB() length = %d, want %d", len(cdb), len(want))
	}
	for i, b := range want {
		if cdb[i] != b {
			t.Errorf("ReadBlockLimitsCDB()[%d] = 0x%02X, want 0x%02X", i, cdb[i], b)
		}
	}
}

func TestWriteCDB(t *testing.T) {
	tests := []struct {
		name     string
		fixed    bool
		xferLen  uint32
		want     []byte
	}{
		{"variable 4096 bytes", false, 4096, []byte{0x0A, 0x00, 0x00, 0x10, 0x00, 0x00}},
		{"fixed 8 blocks", true, 8, []byte{0x0A, 0x01, 0x00, 0x00, 0x08, 0x00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cdb := WriteCDB(tt.fixed, tt.xferLen)
			if len(cdb) != 6 {
				t.Fatalf("WriteCDB() length = %d, want 6", len(cdb))
			}
			for i, b := range tt.want {
				if cdb[i] != b {
					t.Errorf("WriteCDB()[%d] = 0x%02X, want 0x%02X", i, cdb[i], b)
				}
			}
		})
	}
}

func TestReadCDB(t *testing.T) {
	tests := []struct {
		name     string
		fixed    bool
		sili     bool
		xferLen  uint32
		want     []byte
	}{
		{"variable no SILI 65536", false, false, 65536, []byte{0x08, 0x00, 0x01, 0x00, 0x00, 0x00}},
		{"fixed+SILI 1 block", true, true, 1, []byte{0x08, 0x03, 0x00, 0x00, 0x01, 0x00}},
		{"fixed no SILI 1 block", true, false, 1, []byte{0x08, 0x01, 0x00, 0x00, 0x01, 0x00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cdb := ReadCDB(tt.fixed, tt.sili, tt.xferLen)
			if len(cdb) != 6 {
				t.Fatalf("ReadCDB() length = %d, want 6", len(cdb))
			}
			for i, b := range tt.want {
				if cdb[i] != b {
					t.Errorf("ReadCDB()[%d] = 0x%02X, want 0x%02X", i, cdb[i], b)
				}
			}
		})
	}
}

func TestWriteFilemarksCDB(t *testing.T) {
	tests := []struct {
		name  string
		count uint32
		want  []byte
	}{
		{"1 filemark", 1, []byte{0x10, 0x00, 0x00, 0x00, 0x01, 0x00}},
		{"3 filemarks", 3, []byte{0x10, 0x00, 0x00, 0x00, 0x03, 0x00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cdb := WriteFilemarksCDB(tt.count)
			if len(cdb) != 6 {
				t.Fatalf("WriteFilemarksCDB() length = %d, want 6", len(cdb))
			}
			for i, b := range tt.want {
				if cdb[i] != b {
					t.Errorf("WriteFilemarksCDB()[%d] = 0x%02X, want 0x%02X", i, cdb[i], b)
				}
			}
		})
	}
}

func TestRewindCDB(t *testing.T) {
	tests := []struct {
		name   string
		immed  bool
		want   []byte
	}{
		{"no immed", false, []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00}},
		{"immed", true, []byte{0x01, 0x01, 0x00, 0x00, 0x00, 0x00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cdb := RewindCDB(tt.immed)
			if len(cdb) != 6 {
				t.Fatalf("RewindCDB() length = %d, want 6", len(cdb))
			}
			for i, b := range tt.want {
				if cdb[i] != b {
					t.Errorf("RewindCDB()[%d] = 0x%02X, want 0x%02X", i, cdb[i], b)
				}
			}
		})
	}
}

func TestModeSense6CDB(t *testing.T) {
	cdb := ModeSense6CDB(255)
	if len(cdb) != 6 {
		t.Fatalf("length = %d, want 6", len(cdb))
	}
	if cdb[0] != 0x1A {
		t.Errorf("opcode = 0x%02X, want 0x1A", cdb[0])
	}
	if cdb[1] != 0x00 {
		t.Errorf("DBD byte = 0x%02X, want 0x00 (DBD=0)", cdb[1])
	}
	if cdb[4] != 255 {
		t.Errorf("allocLen = %d, want 255", cdb[4])
	}
}

func TestModeSelect6CDB(t *testing.T) {
	cdb := ModeSelect6CDB(12)
	if len(cdb) != 6 {
		t.Fatalf("length = %d, want 6", len(cdb))
	}
	if cdb[0] != 0x15 {
		t.Errorf("opcode = 0x%02X, want 0x15", cdb[0])
	}
	if cdb[1]&0x10 == 0 {
		t.Error("PF bit (byte 1 bit 4) should be set")
	}
	if cdb[4] != 12 {
		t.Errorf("paramLen = %d, want 12", cdb[4])
	}
}

func TestReadPositionCDB(t *testing.T) {
	cdb := ReadPositionCDB()
	if len(cdb) != 10 {
		t.Fatalf("ReadPositionCDB() length = %d, want 10", len(cdb))
	}
	if cdb[0] != 0x34 {
		t.Errorf("opcode = 0x%02X, want 0x34", cdb[0])
	}
	// Service action 0x00 (short form) — byte 1 bits 4-0 should be 0.
	if cdb[1]&0x1F != 0 {
		t.Errorf("service action = 0x%02X, want 0x00", cdb[1]&0x1F)
	}
}
