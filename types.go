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
