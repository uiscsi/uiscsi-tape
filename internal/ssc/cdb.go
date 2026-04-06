package ssc

// ReadBlockLimitsCDB returns a READ BLOCK LIMITS CDB (opcode 0x05, 6 bytes).
// SSC-3 Section 7.4.
func ReadBlockLimitsCDB() []byte {
	cdb := make([]byte, 6)
	cdb[0] = 0x05
	return cdb
}

// WriteCDB returns a WRITE(6) CDB (opcode 0x0A, 6 bytes).
// If fixed is true, the FIXED bit (byte 1 bit 0) is set and transferLen
// is a block count; otherwise transferLen is a byte count.
// Transfer length occupies bytes 2-4 as a 24-bit big-endian value.
// SSC-3 Section 7.1.
func WriteCDB(fixed bool, transferLen uint32) []byte {
	cdb := make([]byte, 6)
	cdb[0] = 0x0A
	if fixed {
		cdb[1] = 0x01
	}
	cdb[2] = byte(transferLen >> 16)
	cdb[3] = byte(transferLen >> 8)
	cdb[4] = byte(transferLen)
	return cdb
}

// ReadCDB returns a READ(6) CDB (opcode 0x08, 6 bytes).
// FIXED bit is byte 1 bit 0; SILI bit is byte 1 bit 1.
// Transfer length occupies bytes 2-4 as a 24-bit big-endian value.
// SSC-3 Section 7.2.
func ReadCDB(fixed, sili bool, transferLen uint32) []byte {
	cdb := make([]byte, 6)
	cdb[0] = 0x08
	if fixed {
		cdb[1] |= 0x01
	}
	if sili {
		cdb[1] |= 0x02
	}
	cdb[2] = byte(transferLen >> 16)
	cdb[3] = byte(transferLen >> 8)
	cdb[4] = byte(transferLen)
	return cdb
}

// WriteFilemarksCDB returns a WRITE FILEMARKS(6) CDB (opcode 0x10, 6 bytes).
// Count occupies bytes 2-4 as a 24-bit big-endian value.
// SSC-3 Section 7.3.
func WriteFilemarksCDB(count uint32) []byte {
	cdb := make([]byte, 6)
	cdb[0] = 0x10
	cdb[2] = byte(count >> 16)
	cdb[3] = byte(count >> 8)
	cdb[4] = byte(count)
	return cdb
}

// ReadPositionCDB returns a READ POSITION CDB (opcode 0x34, 10 bytes).
// Uses service action 0x00 (short form), which returns a 20-byte response
// containing the current logical block position. SSC-3 Section 7.7.
func ReadPositionCDB() []byte {
	cdb := make([]byte, 10)
	cdb[0] = 0x34
	return cdb
}

// RewindCDB returns a REWIND CDB (opcode 0x01, 6 bytes).
// If immed is true, the IMMED bit (byte 1 bit 0) is set for immediate return.
// SSC-3 Section 7.5.
func RewindCDB(immed bool) []byte {
	cdb := make([]byte, 6)
	cdb[0] = 0x01
	if immed {
		cdb[1] = 0x01
	}
	return cdb
}
