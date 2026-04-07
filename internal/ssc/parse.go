package ssc

import "fmt"

// BlockLimits holds parsed READ BLOCK LIMITS response.
type BlockLimits struct {
	Granularity uint8
	MaxBlock    uint32
	MinBlock    uint16
}

// Position holds parsed READ POSITION (short form) response.
type Position struct {
	BOP         bool   // Beginning of partition
	EOP         bool   // End of partition
	BlockNumber uint32 // First/current logical block location
}

// ParseReadPosition parses a 20-byte READ POSITION short form response.
// SSC-3 Section 7.7: byte 0 has BOP (bit 7) and EOP (bit 6), bytes 4-7
// hold the first block location as a big-endian uint32.
func ParseReadPosition(data []byte) (*Position, error) {
	if len(data) < 20 {
		return nil, fmt.Errorf("ssc: READ POSITION response too short (%d bytes, need 20)", len(data))
	}
	return &Position{
		BOP:         data[0]&0x80 != 0,
		EOP:         data[0]&0x40 != 0,
		BlockNumber: uint32(data[4])<<24 | uint32(data[5])<<16 | uint32(data[6])<<8 | uint32(data[7]),
	}, nil
}

// BlockDescriptor holds the tape mode parameter block descriptor.
type BlockDescriptor struct {
	DensityCode uint8
	BlockLength uint32 // 0 = variable-block, >0 = fixed-block size in bytes
}

// ParseModeParameterHeader6 parses a MODE SENSE(6) response and extracts
// the block descriptor. The response layout per SPC-4 Section 7.4.3:
//
//	Byte 0:    Mode Data Length
//	Byte 1:    Medium Type
//	Byte 2:    Device-Specific Parameter
//	Byte 3:    Block Descriptor Length (8 for tape)
//	Bytes 4-11: Block Descriptor
//	  Byte 4:    Density Code
//	  Bytes 5-7: Number of Blocks (3 bytes, usually 0 for tape)
//	  Byte 8:    Reserved
//	  Bytes 9-11: Block Length (3 bytes big-endian)
func ParseModeParameterHeader6(data []byte) (*BlockDescriptor, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("ssc: MODE SENSE(6) response too short (%d bytes, need >= 4)", len(data))
	}
	bdLen := data[3]
	if bdLen == 0 {
		// No block descriptor present (DBD was set, or drive returned none).
		return &BlockDescriptor{}, nil
	}
	if len(data) < 4+int(bdLen) || bdLen < 8 {
		return nil, fmt.Errorf("ssc: MODE SENSE(6) block descriptor too short (bdLen=%d, data=%d)", bdLen, len(data))
	}
	bd := &BlockDescriptor{
		DensityCode: data[4],
		BlockLength: uint32(data[9])<<16 | uint32(data[10])<<8 | uint32(data[11]),
	}
	return bd, nil
}

// BuildModeSelectData6 builds the data-out payload for MODE SELECT(6)
// to set the tape block size. Returns a 12-byte payload: 4-byte mode
// parameter header + 8-byte block descriptor.
func BuildModeSelectData6(blockLength uint32) []byte {
	data := make([]byte, 12)
	// Bytes 0-2: reserved (0)
	data[3] = 8 // Block Descriptor Length
	// Byte 4: Density Code (0 = default)
	// Bytes 5-7: Number of Blocks (0 = all remaining)
	// Byte 8: Reserved
	// Bytes 9-11: Block Length (3 bytes big-endian)
	data[9] = byte(blockLength >> 16)
	data[10] = byte(blockLength >> 8)
	data[11] = byte(blockLength)
	return data
}

// CompressionConfig holds data compression settings from mode page 0x0F.
type CompressionConfig struct {
	DCE bool // Data Compression Enable (read/write)
	DDE bool // Data Decompression Enable (read)
}

// ParseCompressionPage parses a MODE SENSE(6) response containing
// the Data Compression mode page (0x0F). The response has a 4-byte
// mode parameter header, an 8-byte block descriptor (if present),
// then the mode page. SSC-3 Section 8.3.2.
func ParseCompressionPage(data []byte) (*CompressionConfig, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("ssc: compression page response too short (%d bytes)", len(data))
	}
	// Skip header + block descriptor.
	bdLen := int(data[3])
	offset := 4 + bdLen
	if offset+2 > len(data) {
		return nil, fmt.Errorf("ssc: compression page not found (offset %d, data %d)", offset, len(data))
	}
	// Verify page code.
	pageCode := data[offset] & 0x3F
	if pageCode != 0x0F {
		return nil, fmt.Errorf("ssc: expected compression page 0x0F, got 0x%02X", pageCode)
	}
	if offset+16 > len(data) {
		return nil, fmt.Errorf("ssc: compression page truncated")
	}
	return &CompressionConfig{
		DCE: data[offset+2]&0x80 != 0, // byte 2 bit 7
		DDE: data[offset+2]&0x40 != 0, // byte 2 bit 6
	}, nil
}

// BuildCompressionPage builds the data-out payload for MODE SELECT(6)
// to set compression. Returns mode parameter header (4 bytes) +
// block descriptor (8 bytes, zeros) + compression page (16 bytes).
func BuildCompressionPage(dce, dde bool) []byte {
	data := make([]byte, 28)
	// Header.
	data[3] = 8 // Block Descriptor Length
	// Block descriptor: 8 bytes of zeros (don't change block size).
	// Compression page at offset 12.
	data[12] = 0x0F       // Page code
	data[13] = 14         // Page length (14 bytes follow)
	if dce {
		data[14] |= 0x80 // DCE bit
	}
	if dde {
		data[14] |= 0x40 // DDE bit
	}
	// Compression algorithm: 0x00 = default (let drive choose).
	// Decompression algorithm: 0x00 = default.
	return data
}

// ParseReadBlockLimits parses a 6-byte READ BLOCK LIMITS response.
// Max block length is 3 bytes big-endian (bytes 1-3), min block length is 2 bytes big-endian (bytes 4-5).
func ParseReadBlockLimits(data []byte) (*BlockLimits, error) {
	if len(data) < 6 {
		return nil, fmt.Errorf("ssc: READ BLOCK LIMITS response too short (%d bytes, need 6)", len(data))
	}
	return &BlockLimits{
		Granularity: data[0],
		MaxBlock:    uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3]),
		MinBlock:    uint16(data[4])<<8 | uint16(data[5]),
	}, nil
}
