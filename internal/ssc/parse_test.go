package ssc

import "testing"

func TestParseReadBlockLimits(t *testing.T) {
	tests := []struct {
		name            string
		data            []byte
		wantGranularity uint8
		wantMaxBlock    uint32
		wantMinBlock    uint16
		wantErr         bool
	}{
		{
			name:            "max values",
			data:            []byte{0x00, 0xFF, 0xFF, 0xFF, 0x00, 0x01},
			wantGranularity: 0,
			wantMaxBlock:    16777215,
			wantMinBlock:    1,
		},
		{
			name:            "typical values",
			data:            []byte{0x01, 0x00, 0x10, 0x00, 0x00, 0x04},
			wantGranularity: 1,
			wantMaxBlock:    4096,
			wantMinBlock:    4,
		},
		{
			name:    "too short",
			data:    []byte{0x00, 0x01, 0x02, 0x03, 0x04},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bl, err := ParseReadBlockLimits(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("ParseReadBlockLimits() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseReadBlockLimits() unexpected error: %v", err)
			}
			if bl.Granularity != tt.wantGranularity {
				t.Errorf("Granularity = %d, want %d", bl.Granularity, tt.wantGranularity)
			}
			if bl.MaxBlock != tt.wantMaxBlock {
				t.Errorf("MaxBlock = %d, want %d", bl.MaxBlock, tt.wantMaxBlock)
			}
			if bl.MinBlock != tt.wantMinBlock {
				t.Errorf("MinBlock = %d, want %d", bl.MinBlock, tt.wantMinBlock)
			}
		})
	}
}

func TestParseModeParameterHeader6(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		wantBlock uint32
		wantErr   bool
	}{
		{
			name: "variable block (0)",
			data: []byte{
				11,   // mode data length
				0x00, // medium type
				0x00, // device-specific
				8,    // block descriptor length
				0x00, // density code
				0, 0, 0, // number of blocks
				0,       // reserved
				0, 0, 0, // block length = 0 (variable)
			},
			wantBlock: 0,
		},
		{
			name: "fixed 65536",
			data: []byte{
				11, 0x00, 0x00, 8,
				0x00, 0, 0, 0, 0,
				0x01, 0x00, 0x00, // block length = 65536
			},
			wantBlock: 65536,
		},
		{
			name: "fixed 512",
			data: []byte{
				11, 0x00, 0x00, 8,
				0x00, 0, 0, 0, 0,
				0x00, 0x02, 0x00, // block length = 512
			},
			wantBlock: 512,
		},
		{
			name: "no block descriptor (bdLen=0)",
			data: []byte{3, 0x00, 0x00, 0},
			wantBlock: 0,
		},
		{
			name:    "too short",
			data:    []byte{0, 0},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bd, err := ParseModeParameterHeader6(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bd.BlockLength != tt.wantBlock {
				t.Errorf("BlockLength = %d, want %d", bd.BlockLength, tt.wantBlock)
			}
		})
	}
}

func TestBuildModeSelectData6(t *testing.T) {
	data := BuildModeSelectData6(65536)
	if len(data) != 12 {
		t.Fatalf("length = %d, want 12", len(data))
	}
	if data[3] != 8 {
		t.Errorf("block descriptor length = %d, want 8", data[3])
	}
	// Block length at bytes 9-11
	bl := uint32(data[9])<<16 | uint32(data[10])<<8 | uint32(data[11])
	if bl != 65536 {
		t.Errorf("block length = %d, want 65536", bl)
	}

	// Variable mode
	data0 := BuildModeSelectData6(0)
	bl0 := uint32(data0[9])<<16 | uint32(data0[10])<<8 | uint32(data0[11])
	if bl0 != 0 {
		t.Errorf("variable block length = %d, want 0", bl0)
	}
}

func TestParseReadPosition(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		wantBOP   bool
		wantEOP   bool
		wantBlock uint32
		wantErr   bool
	}{
		{
			name: "at BOT",
			data: func() []byte {
				d := make([]byte, 20)
				d[0] = 0x80 // BOP=1
				return d
			}(),
			wantBOP:   true,
			wantBlock: 0,
		},
		{
			name: "at position 1000",
			data: func() []byte {
				d := make([]byte, 20)
				d[4] = 0x00
				d[5] = 0x00
				d[6] = 0x03
				d[7] = 0xE8 // 1000
				return d
			}(),
			wantBlock: 1000,
		},
		{
			name: "EOP set",
			data: func() []byte {
				d := make([]byte, 20)
				d[0] = 0x40 // EOP=1
				return d
			}(),
			wantEOP: true,
		},
		{
			name: "BOP and EOP both set",
			data: func() []byte {
				d := make([]byte, 20)
				d[0] = 0xC0
				return d
			}(),
			wantBOP: true,
			wantEOP: true,
		},
		{
			name:    "too short",
			data:    make([]byte, 19),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos, err := ParseReadPosition(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if pos.BOP != tt.wantBOP {
				t.Errorf("BOP = %v, want %v", pos.BOP, tt.wantBOP)
			}
			if pos.EOP != tt.wantEOP {
				t.Errorf("EOP = %v, want %v", pos.EOP, tt.wantEOP)
			}
			if pos.BlockNumber != tt.wantBlock {
				t.Errorf("BlockNumber = %d, want %d", pos.BlockNumber, tt.wantBlock)
			}
		})
	}
}
