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
