package tape

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/uiscsi/uiscsi"
	"github.com/uiscsi/uiscsi-tape/internal/ssc"
)

const (
	turRetryInterval = 1 * time.Second
	turMaxRetries    = 30 // 30 seconds — tape loads can take a while
)

// Drive represents an opened SSC tape drive over an iSCSI session.
// Drive methods are not safe for concurrent use. A single goroutine
// should own the Drive; coordinate externally if shared access is needed.
type Drive struct {
	session  *uiscsi.Session
	lun      uint64
	info     DriveInfo
	limits   BlockLimits
	cfg      driveConfig
	pipeline *readPipeline // non-nil when readAhead > 0, lazy-started
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

// pollUnitReady sends TEST UNIT READY in a loop, retrying on UNIT ATTENTION
// (sense key 0x06) and NOT READY (sense key 0x02). These are normal after
// media insertion — the drive needs time to load the tape. Returns nil once
// the drive reports GOOD, or the last error after turMaxRetries attempts.
func pollUnitReady(ctx context.Context, session *uiscsi.Session, lun uint64, log *slog.Logger) error {
	for attempt := range turMaxRetries {
		err := session.SCSI().TestUnitReady(ctx, lun)
		if err == nil {
			return nil
		}

		// Check if this is a retriable SCSI error (UNIT ATTENTION or NOT READY).
		var se *uiscsi.SCSIError
		if errors.As(err, &se) && (se.SenseKey == 0x06 || se.SenseKey == 0x02) {
			log.Debug("tape: drive not yet ready, retrying",
				"attempt", attempt+1,
				"sense_key", fmt.Sprintf("0x%02X", se.SenseKey),
				"asc", fmt.Sprintf("0x%02X", se.ASC),
			)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(turRetryInterval):
				continue
			}
		}

		// Non-retriable error.
		return err
	}
	return fmt.Errorf("tape: drive not ready after %d attempts", turMaxRetries)
}

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

	// Step 1: TEST UNIT READY — poll until the drive is ready.
	// After media insertion, tape drives report UNIT ATTENTION ("medium
	// may have changed") and/or NOT READY ("becoming ready") for several
	// seconds while the tape loads. This is normal SSC behavior.
	log.Debug("tape: probe step 1 -- TEST UNIT READY (polling)", "lun", lun)
	if err := pollUnitReady(ctx, session, lun, log); err != nil {
		return nil, fmt.Errorf("tape: drive not ready: %w", err)
	}

	// Step 2: INQUIRY
	log.Debug("tape: probe step 2 -- INQUIRY", "lun", lun)
	inq, err := session.SCSI().Inquiry(ctx, lun)
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
	result, err := session.Raw().Execute(ctx, lun, ssc.ReadBlockLimitsCDB(), uiscsi.WithDataIn(6))
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

	d := &Drive{
		session: session,
		lun:     lun,
		info:    info,
		limits:  limits,
		cfg:     cfg,
	}

	// Step 4: Configure block size if requested.
	if cfg.blockSize > 0 {
		if cfg.blockSize > limits.MaxBlock {
			return nil, fmt.Errorf("tape: requested block size %d exceeds drive maximum %d", cfg.blockSize, limits.MaxBlock)
		}
		if cfg.blockSize < uint32(limits.MinBlock) {
			return nil, fmt.Errorf("tape: requested block size %d below drive minimum %d", cfg.blockSize, limits.MinBlock)
		}
		log.Debug("tape: probe step 4 -- MODE SELECT (set block size)", "blockSize", cfg.blockSize)
		if err := d.SetBlockSize(ctx, cfg.blockSize); err != nil {
			return nil, fmt.Errorf("tape: set block size: %w", err)
		}
	}

	return d, nil
}
