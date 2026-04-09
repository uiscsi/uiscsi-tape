package tape

import (
	"context"
	"fmt"

	"github.com/uiscsi/uiscsi"
	"github.com/uiscsi/uiscsi-tape/internal/ssc"
)

// DensityInfo holds a density descriptor reported by the drive.
type DensityInfo struct {
	PrimaryCode   uint8  // primary density code (e.g., 0x46 for LTO-4)
	SecondaryCode uint8  // secondary code (0 if not applicable)
	Writable      bool   // drive can write at this density
	Default       bool   // default density when no media loaded
	BitsPerMM     uint32 // recording density (bits per mm)
	MediaWidth    uint16 // media width in tenths of mm
	Tracks        uint16 // number of tracks
	CapacityMB    uint32 // capacity in megabytes
	Organization  string // assigning organization (e.g., "LTO CVE")
	Description   string // human-readable description (e.g., "LTO-4")
	Name          string // short density name
}

// DensityCode returns the density code of the currently loaded medium
// from MODE SENSE. Returns 0x00 if the drive reports default/unknown.
func (d *Drive) DensityCode(ctx context.Context) (uint8, error) {
	log := d.log()
	log.Debug("tape: mode sense (density code query)")

	data, err := d.session.SCSI().ModeSense6(ctx, d.lun, 0x00, 0x00)
	if err != nil {
		return 0, fmt.Errorf("tape: mode sense: %w", err)
	}

	bd, err := ssc.ParseModeParameterHeader6(data)
	if err != nil {
		return 0, fmt.Errorf("tape: %w", err)
	}

	log.Debug("tape: density code", "code", fmt.Sprintf("0x%02X", bd.DensityCode))
	return bd.DensityCode, nil
}

// ReportDensitySupport queries the drive for all supported density
// codes. If media is false, returns densities the drive supports
// regardless of loaded media. If media is true, returns densities
// applicable to the currently loaded medium.
//
// Not all drives implement this command. Returns an error if the
// drive rejects the command (CHECK CONDITION with INVALID OPCODE).
func (d *Drive) ReportDensitySupport(ctx context.Context, media bool) ([]DensityInfo, error) {
	log := d.log()
	log.Debug("tape: report density support", "media", media)

	cdb := ssc.ReportDensitySupportCDB(media, 8192)
	result, err := d.session.Raw().Execute(ctx, d.lun, cdb, uiscsi.WithDataIn(8192))
	if err != nil {
		return nil, fmt.Errorf("tape: report density support: %w", err)
	}

	if senseErr := interpretSense(result.Status, result.SenseData); senseErr != nil {
		return nil, fmt.Errorf("tape: report density support: %w", senseErr)
	}

	descs, err := ssc.ParseReportDensitySupport(result.Data)
	if err != nil {
		return nil, fmt.Errorf("tape: %w", err)
	}

	infos := make([]DensityInfo, len(descs))
	for i, desc := range descs {
		infos[i] = DensityInfo{
			PrimaryCode:   desc.PrimaryCode,
			SecondaryCode: desc.SecondaryCode,
			Writable:      desc.WRTok,
			Default:       desc.DefLT,
			BitsPerMM:     desc.BitsPerMM,
			MediaWidth:    desc.MediaWidth,
			Tracks:        desc.Tracks,
			CapacityMB:    desc.Capacity,
			Organization:  desc.Assigning,
			Description:   desc.Description,
			Name:          desc.Name,
		}
	}

	log.Debug("tape: density support", "count", len(infos))
	return infos, nil
}

// DensityName returns a human-readable name for a tape density code.
// Known codes are mapped per T10 SSC density code assignments.
// Returns "Unknown (0xNN)" for unrecognized codes.
func DensityName(code uint8) string {
	if name, ok := densityNames[code]; ok {
		return name
	}
	return fmt.Sprintf("Unknown (0x%02X)", code)
}

// densityNames maps density codes to human-readable names.
// Sources: T10 SSC density code assignments (https://www.t10.org/lists/adc-num.htm),
// SSC-3/SSC-4 Annex A.
var densityNames = map[uint8]string{
	0x00: "Default",

	// QIC formats
	0x01: "QIC-11",
	0x02: "QIC-24",
	0x03: "QIC-120",
	0x05: "QIC-150",
	0x06: "QIC-525",
	0x07: "QIC-1350",
	0x09: "QIC-3095",
	0x0B: "QIC-3220",
	0x0D: "QIC-2GB",
	0x0F: "QIC-4GB",

	// DDS / DAT (4mm)
	0x10: "QIC-2GB (wide)",
	0x13: "DDS-2",
	0x14: "DDS-2 (media recognition)",
	0x24: "DDS-3",
	0x25: "DDS-3",
	0x26: "DDS-4",
	0x47: "DAT-72",
	0x48: "DAT-160",
	0x49: "DAT-320",

	// 8mm / Mammoth / AIT
	0x15: "8mm-15",
	0x27: "8mm-AME",
	0x28: "AIT-1",
	0x30: "AIT-1 Turbo",
	0x31: "AIT-2",
	0x32: "AIT-3",
	0x33: "AIT-3 Ex",
	0x34: "AIT-4",
	0x8C: "Mammoth-2",

	// DLT / SDLT
	0x17: "DLT-10GB",
	0x18: "DLT-15GB",
	0x19: "DLT-20GB",
	0x1A: "DLT-35GB",
	0x1B: "DLT-40GB",
	0x1C: "DLT1 40/56GB",
	0x40: "SDLT-1",
	0x41: "DLT-S4",
	0x4A: "SDLT-2",
	0x4B: "VS-1",
	0x4C: "VS-160",

	// LTO Ultrium
	0x42: "LTO-2",
	0x44: "LTO-3",
	0x46: "LTO-4",
	0x58: "LTO-5",
	0x5A: "LTO-6",
	0x5C: "LTO-7",
	0x5D: "LTO-7 Type M (M8)",
	0x5E: "LTO-8",
	0x60: "LTO-9",

	// IBM 3592
	0x51: "3592-J1A",
	0x52: "3592-E05",
	0x53: "3592-E06",
	0x54: "3592-E07",
	0x55: "3592-E08",

	// 3480/3490
	0x29: "3480",
	0x2A: "3490E",
}
