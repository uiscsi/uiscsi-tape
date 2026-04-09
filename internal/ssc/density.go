package ssc

import (
	"encoding/binary"
	"fmt"
)

// DensityDescriptor holds one entry from REPORT DENSITY SUPPORT.
// SSC-3 Section 7.6.
type DensityDescriptor struct {
	PrimaryCode   uint8
	SecondaryCode uint8
	WRTok         bool   // drive can write this density
	DUP           bool   // appears in more than one descriptor
	DefLT         bool   // default density when no media loaded
	DLVALID       bool   // description length field is valid
	BitsPerMM     uint32 // recording bits per mm (24-bit)
	MediaWidth    uint16 // media width in mm (tenths)
	Tracks        uint16 // number of tracks
	Capacity      uint32 // capacity in megabytes
	Assigning     string // 8-byte organization name (trimmed)
	Description   string // 20-byte density description (trimmed)
	Name          string // 8-byte density name (trimmed)
}

// ReportDensitySupportCDB returns a REPORT DENSITY SUPPORT CDB
// (opcode 0x44, 10 bytes). SSC-3 Section 7.6.
// media=false reports drive-supported densities; media=true reports
// densities of the currently loaded medium.
func ReportDensitySupportCDB(media bool, allocLen uint16) []byte {
	cdb := make([]byte, 10)
	cdb[0] = 0x44
	if media {
		cdb[1] = 0x01 // MEDIA bit
	}
	binary.BigEndian.PutUint16(cdb[7:9], allocLen)
	return cdb
}

// ParseReportDensitySupport parses the response from REPORT DENSITY
// SUPPORT. The response has a 4-byte header (allocation length list)
// followed by 52-byte density descriptors.
// SSC-3 Section 7.6.
func ParseReportDensitySupport(data []byte) ([]DensityDescriptor, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("ssc: REPORT DENSITY SUPPORT response too short (%d bytes, need >= 4)", len(data))
	}
	listLen := int(binary.BigEndian.Uint16(data[0:2]))
	end := 4 + listLen
	if end > len(data) {
		end = len(data)
	}

	const descLen = 52
	var descs []DensityDescriptor
	for off := 4; off+descLen <= end; off += descLen {
		d := data[off : off+descLen]
		descs = append(descs, DensityDescriptor{
			PrimaryCode:   d[0],
			SecondaryCode: d[1],
			WRTok:         d[2]&0x80 != 0,
			DUP:           d[2]&0x40 != 0,
			DefLT:         d[2]&0x20 != 0,
			DLVALID:       d[2]&0x10 != 0,
			BitsPerMM:     uint32(d[5])<<16 | uint32(d[6])<<8 | uint32(d[7]),
			MediaWidth:    binary.BigEndian.Uint16(d[8:10]),
			Tracks:        binary.BigEndian.Uint16(d[10:12]),
			Capacity:      binary.BigEndian.Uint32(d[12:16]),
			Assigning:     trimRight(string(d[16:24])),
			Description:   trimRight(string(d[24:44])),
			Name:          trimRight(string(d[44:52])),
		})
	}
	return descs, nil
}

func trimRight(s string) string {
	// Trim trailing spaces and NULs.
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == 0) {
		s = s[:len(s)-1]
	}
	return s
}
