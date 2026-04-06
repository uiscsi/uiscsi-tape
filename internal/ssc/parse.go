package ssc

import "fmt"

// BlockLimits holds parsed READ BLOCK LIMITS response.
type BlockLimits struct {
	Granularity uint8
	MaxBlock    uint32
	MinBlock    uint16
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
