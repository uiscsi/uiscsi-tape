package tape

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/rkujawa/uiscsi"
	"github.com/rkujawa/uiscsi-tape/internal/ssc"
)

// Drive represents an opened SSC tape drive over an iSCSI session.
type Drive struct {
	session *uiscsi.Session
	lun     uint64
	info    DriveInfo
	limits  BlockLimits
	cfg     driveConfig
}

// log returns the configured logger, falling back to slog.Default().
func (d *Drive) log() *slog.Logger {
	if d.cfg.logger != nil {
		return d.cfg.logger
	}
	return slog.Default()
}

// Info returns drive identification from INQUIRY probe.
func (d *Drive) Info() DriveInfo { return d.info }

// Limits returns block size limits from READ BLOCK LIMITS probe.
func (d *Drive) Limits() BlockLimits { return d.limits }

// Open probes an iSCSI LUN and returns a Drive if it is a tape device.
// The probe sequence is: TEST UNIT READY, INQUIRY (device type 0x01 check),
// READ BLOCK LIMITS. All three must succeed for Open to return a Drive.
// Returns ErrNotTape if the device is not a sequential access device (type 0x01).
func Open(ctx context.Context, session *uiscsi.Session, lun uint64, opts ...Option) (*Drive, error) {
	var cfg driveConfig
	for _, o := range opts {
		o(&cfg)
	}

	log := cfg.logger
	if log == nil {
		log = slog.Default()
	}

	// Step 1: TEST UNIT READY
	log.Debug("tape: probe step 1 -- TEST UNIT READY", "lun", lun)
	if err := session.TestUnitReady(ctx, lun); err != nil {
		return nil, fmt.Errorf("tape: drive not ready: %w", err)
	}

	// Step 2: INQUIRY
	log.Debug("tape: probe step 2 -- INQUIRY", "lun", lun)
	inq, err := session.Inquiry(ctx, lun)
	if err != nil {
		return nil, fmt.Errorf("tape: inquiry failed: %w", err)
	}
	if inq.DeviceType != 0x01 {
		return nil, fmt.Errorf("tape: device type 0x%02X is not sequential access: %w", inq.DeviceType, ErrNotTape)
	}
	info := DriveInfo{
		DeviceType: inq.DeviceType,
		VendorID:   inq.VendorID,
		ProductID:  inq.ProductID,
		Revision:   inq.Revision,
	}
	log.Debug("tape: probe INQUIRY result",
		"vendor", info.VendorID,
		"product", info.ProductID,
		"revision", info.Revision,
	)

	// Step 3: READ BLOCK LIMITS
	log.Debug("tape: probe step 3 -- READ BLOCK LIMITS", "lun", lun)
	result, err := session.Execute(ctx, lun, ssc.ReadBlockLimitsCDB(), uiscsi.WithDataIn(6))
	if err != nil {
		return nil, fmt.Errorf("tape: READ BLOCK LIMITS failed: %w", err)
	}
	// Execute returns raw status -- interpret via tape-specific sense parsing.
	if result.Status != 0 {
		senseErr := interpretSense(result.Status, result.SenseData)
		if senseErr != nil {
			return nil, fmt.Errorf("tape: READ BLOCK LIMITS: %w", senseErr)
		}
		return nil, fmt.Errorf("tape: READ BLOCK LIMITS: status 0x%02X", result.Status)
	}
	bl, err := ssc.ParseReadBlockLimits(result.Data)
	if err != nil {
		return nil, fmt.Errorf("tape: %w", err)
	}
	limits := BlockLimits{
		Granularity: bl.Granularity,
		MaxBlock:    bl.MaxBlock,
		MinBlock:    bl.MinBlock,
	}
	log.Debug("tape: probe READ BLOCK LIMITS result",
		"maxBlock", limits.MaxBlock,
		"minBlock", limits.MinBlock,
		"granularity", limits.Granularity,
	)

	return &Drive{
		session: session,
		lun:     lun,
		info:    info,
		limits:  limits,
		cfg:     cfg,
	}, nil
}
