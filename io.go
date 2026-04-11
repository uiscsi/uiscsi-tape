package tape

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/uiscsi/uiscsi"
	"github.com/uiscsi/uiscsi-tape/internal/ssc"
)

// Read reads one record from the current tape position into buf.
//
// In variable-block mode (the default), up to len(buf) bytes are read.
// The actual record on tape may be shorter than the buffer; the returned
// n indicates the actual bytes read.
//
// In fixed-block mode (configured via [WithBlockSize]), Read reads
// floor(len(buf)/blockSize) blocks. The buffer must be at least one
// block in size.
//
// SILI behavior (configured via [WithSILI]):
//   - SILI=false (default): if the record is shorter than requested, the
//     target returns CHECK CONDITION with ILI. Read returns (n, *[TapeError])
//     where n is the actual bytes and the error matches [ErrILI].
//   - SILI=true: short records return GOOD status. Read returns (n, nil).
//
// A filemark at the current position returns (0, err) where err matches
// [ErrFilemark]. A read past the end of written data returns (0, err)
// where err matches [ErrBlankCheck].
func (d *Drive) Read(ctx context.Context, buf []byte) (int, error) {
	if d.cfg.readAhead > 0 {
		return d.readPipelined(ctx, buf)
	}
	return d.readSync(ctx, buf)
}

// readSync is the original synchronous read — one SCSI command per call.
func (d *Drive) readSync(ctx context.Context, buf []byte) (int, error) {
	log := d.log()
	fixed := d.cfg.blockSize > 0

	var transferLen, allocLen uint32
	if fixed {
		nBlocks := uint32(len(buf)) / d.cfg.blockSize
		if nBlocks == 0 {
			return 0, fmt.Errorf("tape: read: buffer size %d too small for block size %d", len(buf), d.cfg.blockSize)
		}
		transferLen = nBlocks
		allocLen = nBlocks * d.cfg.blockSize
	} else {
		transferLen = uint32(len(buf))
		allocLen = transferLen
	}

	cdb := ssc.ReadCDB(fixed, d.cfg.sili, transferLen)
	log.Debug("tape: read", "fixed", fixed, "sili", d.cfg.sili, "transferLen", transferLen, "allocLen", allocLen)

	sr, err := d.session.Raw().StreamExecute(ctx, d.lun, cdb, uiscsi.WithDataIn(allocLen))
	if err != nil {
		return 0, fmt.Errorf("tape: read: %w", err)
	}

	n, readErr := io.ReadFull(sr.Data, buf[:allocLen])
	if readErr == io.ErrUnexpectedEOF || readErr == io.EOF {
		readErr = nil
	}
	_, _ = io.Copy(io.Discard, sr.Data) // drain remaining data; error irrelevant, status from sr.Wait()

	if readErr != nil {
		return n, fmt.Errorf("tape: read: %w", readErr)
	}

	status, senseData, waitErr := sr.Wait()
	if waitErr != nil {
		return n, fmt.Errorf("tape: read: %w", waitErr)
	}

	if senseErr := interpretSense(status, senseData); senseErr != nil {
		log.Debug("tape: read condition", "n", n, "err", senseErr)
		return n, senseErr
	}

	log.Debug("tape: read complete", "n", n)
	return n, nil
}

// readPipelined reads from the pre-fetch pipeline. Lazy-starts the
// pipeline on first call. Auto-restarts after filemark (next file).
func (d *Drive) readPipelined(ctx context.Context, buf []byte) (int, error) {
	// Lazy-start the pipeline. Use caller's buffer size as the per-record
	// buffer size — this matches the SCSI READ transfer length the caller expects.
	if d.pipeline == nil {
		d.pipeline = newReadPipeline(pipelineConfig{
			session:   d.session,
			lun:       d.lun,
			blockSize: d.cfg.blockSize,
			sili:      d.cfg.sili,
			depth:     d.cfg.readAhead,
			bufSize:   len(buf),
			logger:    d.log(),
		})
		d.pipeline.start(ctx)
	} else if !d.pipeline.isRunning() {
		// Pipeline exhausted (filemark/error) — check if we should restart.
		// If there are still buffered results, consume them first.
		select {
		case result, ok := <-d.pipeline.results:
			if ok {
				n := copy(buf, result.data)
				if len(result.data) > len(buf) && result.err == nil {
					return n, fmt.Errorf("tape: read: record (%d bytes) exceeds buffer (%d bytes): %w",
						len(result.data), len(buf), ErrILI)
				}
				if errors.Is(result.err, ErrFilemark) {
					// Filemark — restart pipeline for next file.
					d.pipeline.restart(ctx)
				}
				return n, result.err
			}
		default:
			// Channel drained and fetcher dead — restart.
			d.pipeline.restart(ctx)
		}
	}

	select {
	case result, ok := <-d.pipeline.results:
		if !ok {
			return 0, io.EOF
		}
		n := copy(buf, result.data)
		if len(result.data) > len(buf) && result.err == nil {
			return n, fmt.Errorf("tape: read: record (%d bytes) exceeds buffer (%d bytes): %w",
				len(result.data), len(buf), ErrILI)
		}
		d.log().Debug("tape: read complete (pipelined)", "n", n)

		// On filemark, restart pipeline for the next file.
		if errors.Is(result.err, ErrFilemark) {
			d.pipeline.restart(ctx)
		}
		return n, result.err

	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// stopPipeline stops the read-ahead pipeline if active. Must be called
// before any operation that changes tape position or state.
func (d *Drive) stopPipeline() {
	if d.pipeline != nil {
		d.pipeline.stop()
		d.pipeline = nil
	}
}

// Write writes one record to the current tape position.
//
// In variable-block mode (the default), data is written as a single
// variable-length record of len(data) bytes.
//
// In fixed-block mode (configured via [WithBlockSize]), len(data) must
// be a multiple of the configured block size; otherwise Write returns
// an error without issuing a SCSI command.
//
// An end-of-medium early warning returns an error matching [ErrEOM].
// The data is still written — the caller should stop writing soon.
// A hard end-of-medium (volume overflow) returns an error matching
// neither ErrEOM nor nil.
//
// # Write Atomicity
//
// Write is not idempotent. If Write returns a transport error
// (*[uiscsi.TransportError]), the number of bytes that reached the tape
// head is unknown — the drive may have written a partial record and
// advanced its position by an indeterminate amount. Do not retry Write
// with the same data after a transport error. Instead, call
// [Drive.Position] to determine the current block address and rewind
// or space as needed before writing again.
//
// A SCSI error (*[TapeError]) indicates the drive responded; when
// [TapeError.Position] is valid, it indicates the number of blocks
// actually written.
func (d *Drive) Write(ctx context.Context, data []byte) error {
	d.stopPipeline()
	log := d.log()
	fixed := d.cfg.blockSize > 0

	var transferLen uint32
	if fixed {
		if uint32(len(data))%d.cfg.blockSize != 0 {
			return fmt.Errorf("tape: write: data length %d is not a multiple of block size %d", len(data), d.cfg.blockSize)
		}
		transferLen = uint32(len(data)) / d.cfg.blockSize
	} else {
		transferLen = uint32(len(data))
	}

	cdb := ssc.WriteCDB(fixed, transferLen)
	log.Debug("tape: write", "fixed", fixed, "transferLen", transferLen, "bytes", len(data))

	result, err := d.session.Raw().Execute(ctx, d.lun, cdb,
		uiscsi.WithDataOut(bytes.NewReader(data), uint32(len(data))),
	)
	if err != nil {
		return fmt.Errorf("tape: write: %w", err)
	}

	if senseErr := interpretSense(result.Status, result.SenseData); senseErr != nil {
		log.Debug("tape: write condition", "err", senseErr)
		return senseErr
	}

	log.Debug("tape: write complete", "bytes", len(data))
	return nil
}

// WriteFilemarks writes count filemarks at the current tape position.
// Filemarks serve as logical record separators on tape.
func (d *Drive) WriteFilemarks(ctx context.Context, count uint32) error {
	d.stopPipeline()
	log := d.log()
	log.Debug("tape: write filemarks", "count", count)

	cdb := ssc.WriteFilemarksCDB(count)
	result, err := d.session.Raw().Execute(ctx, d.lun, cdb)
	if err != nil {
		return fmt.Errorf("tape: write filemarks: %w", err)
	}

	if senseErr := interpretSense(result.Status, result.SenseData); senseErr != nil {
		return fmt.Errorf("tape: write filemarks: %w", senseErr)
	}
	return nil
}

// BlockSize queries the drive's current block size from the mode parameter
// block descriptor via MODE SENSE(6). Returns 0 for variable-block mode,
// >0 for fixed-block mode (block size in bytes).
func (d *Drive) BlockSize(ctx context.Context) (uint32, error) {
	log := d.log()
	log.Debug("tape: mode sense (block size query)")

	data, err := d.session.SCSI().ModeSense6(ctx, d.lun, 0x00, 0x00)
	if err != nil {
		return 0, fmt.Errorf("tape: mode sense: %w", err)
	}

	bd, err := ssc.ParseModeParameterHeader6(data)
	if err != nil {
		return 0, fmt.Errorf("tape: %w", err)
	}

	log.Debug("tape: current block size", "blockLength", bd.BlockLength)
	return bd.BlockLength, nil
}

// SetBlockSize configures the drive's block size via MODE SELECT(6).
// Set blockLength to 0 for variable-block mode, or >0 for fixed-block
// mode with that size in bytes. This must be called before Read/Write
// if fixed-block mode is desired; the drive will reject fixed-block
// CDBs unless its block descriptor matches.
func (d *Drive) SetBlockSize(ctx context.Context, blockLength uint32) error {
	d.stopPipeline()
	log := d.log()
	log.Debug("tape: mode select (set block size)", "blockLength", blockLength)

	payload := ssc.BuildModeSelectData6(blockLength)
	if err := d.session.SCSI().ModeSelect6(ctx, d.lun, payload); err != nil {
		return fmt.Errorf("tape: mode select: %w", err)
	}

	log.Debug("tape: block size configured", "blockLength", blockLength)
	return nil
}

// Compression queries the drive's current data compression settings
// via MODE SENSE(6) page 0x0F.
func (d *Drive) Compression(ctx context.Context) (dce, dde bool, err error) {
	log := d.log()
	log.Debug("tape: mode sense (compression query)")

	data, err := d.session.SCSI().ModeSense6(ctx, d.lun, 0x0F, 0x00)
	if err != nil {
		return false, false, fmt.Errorf("tape: mode sense compression: %w", err)
	}

	cc, err := ssc.ParseCompressionPage(data)
	if err != nil {
		return false, false, fmt.Errorf("tape: %w", err)
	}

	log.Debug("tape: compression", "dce", cc.DCE, "dde", cc.DDE)
	return cc.DCE, cc.DDE, nil
}

// SetCompression configures data compression on the drive via
// MODE SELECT(6) page 0x0F. Set dce=true to enable compression on
// writes, dde=true to enable decompression on reads. Most drives
// require DDE=true to read compressed tapes.
func (d *Drive) SetCompression(ctx context.Context, dce, dde bool) error {
	d.stopPipeline()
	log := d.log()
	log.Debug("tape: mode select (set compression)", "dce", dce, "dde", dde)

	payload := ssc.BuildCompressionPage(dce, dde)
	if err := d.session.SCSI().ModeSelect6(ctx, d.lun, payload); err != nil {
		return fmt.Errorf("tape: mode select compression: %w", err)
	}

	log.Debug("tape: compression configured", "dce", dce, "dde", dde)
	return nil
}

// Position returns the current logical block position on tape.
// Uses READ POSITION (short form, SSC-3 Section 7.7).
func (d *Drive) Position(ctx context.Context) (*Position, error) {
	log := d.log()
	log.Debug("tape: read position")

	cdb := ssc.ReadPositionCDB()
	result, err := d.session.Raw().Execute(ctx, d.lun, cdb, uiscsi.WithDataIn(20))
	if err != nil {
		return nil, fmt.Errorf("tape: read position: %w", err)
	}

	if senseErr := interpretSense(result.Status, result.SenseData); senseErr != nil {
		return nil, fmt.Errorf("tape: read position: %w", senseErr)
	}

	pos, err := ssc.ParseReadPosition(result.Data)
	if err != nil {
		return nil, fmt.Errorf("tape: %w", err)
	}

	log.Debug("tape: position", "block", pos.BlockNumber, "bop", pos.BOP, "eop", pos.EOP)
	return &Position{
		BOP:         pos.BOP,
		EOP:         pos.EOP,
		BlockNumber: pos.BlockNumber,
	}, nil
}

// Close releases drive resources. If the drive was opened with
// [WithBlockSize] (fixed-block mode), Close restores variable-block mode
// via MODE SELECT. Safe to call multiple times.
func (d *Drive) Close(ctx context.Context) error {
	d.stopPipeline()
	if d.cfg.blockSize > 0 {
		d.log().Debug("tape: close — restoring variable-block mode")
		return d.SetBlockSize(ctx, 0)
	}
	return nil
}

// Rewind repositions the tape to the beginning of the first partition.
// The call blocks until the rewind completes; use ctx for timeout control.
func (d *Drive) Rewind(ctx context.Context) error {
	d.stopPipeline()
	log := d.log()
	log.Debug("tape: rewind")

	cdb := ssc.RewindCDB(false)
	result, err := d.session.Raw().Execute(ctx, d.lun, cdb)
	if err != nil {
		return fmt.Errorf("tape: rewind: %w", err)
	}

	if senseErr := interpretSense(result.Status, result.SenseData); senseErr != nil {
		return fmt.Errorf("tape: rewind: %w", senseErr)
	}
	return nil
}
