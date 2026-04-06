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
