package tape

// DriveInfo holds identification data from INQUIRY.
type DriveInfo struct {
	DeviceType uint8
	VendorID   string
	ProductID  string
	Revision   string
}

// BlockLimits holds parsed READ BLOCK LIMITS response.
type BlockLimits struct {
	Granularity uint8
	MaxBlock    uint32
	MinBlock    uint16
}

// Position holds the current tape position from READ POSITION (short form).
type Position struct {
	BOP         bool   // At beginning of partition
	EOP         bool   // At end of partition
	BlockNumber uint32 // Current logical block number
}
