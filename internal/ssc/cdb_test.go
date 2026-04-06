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
