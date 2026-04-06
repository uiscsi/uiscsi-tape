package tape

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/rkujawa/uiscsi"
	"github.com/rkujawa/uiscsi-tape/internal/ssc"
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

	sr, err := d.session.StreamExecute(ctx, d.lun, cdb, uiscsi.WithDataIn(allocLen))
	if err != nil {
		return 0, fmt.Errorf("tape: read: %w", err)
	}

	// Consume streamed data into buf. Short reads and empty reads are
	// normal for tape: variable-block records may be shorter than the
	// buffer, and conditions like filemark/blank check produce no data
	// at all (immediate EOF from the chanReader).
	n, readErr := io.ReadFull(sr.Data, buf[:allocLen])
	if readErr == io.ErrUnexpectedEOF || readErr == io.EOF {
		readErr = nil
	}
	// Drain any unconsumed data to unblock Wait.
	io.Copy(io.Discard, sr.Data)

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
func (d *Drive) Write(ctx context.Context, data []byte) error {
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

	result, err := d.session.Execute(ctx, d.lun, cdb,
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
	log := d.log()
	log.Debug("tape: write filemarks", "count", count)

	cdb := ssc.WriteFilemarksCDB(count)
	result, err := d.session.Execute(ctx, d.lun, cdb)
	if err != nil {
		return fmt.Errorf("tape: write filemarks: %w", err)
	}

	if senseErr := interpretSense(result.Status, result.SenseData); senseErr != nil {
		return fmt.Errorf("tape: write filemarks: %w", senseErr)
	}
	return nil
}

// Position returns the current logical block position on tape.
// Uses READ POSITION (short form, SSC-3 Section 7.7).
func (d *Drive) Position(ctx context.Context) (*Position, error) {
	log := d.log()
	log.Debug("tape: read position")

	cdb := ssc.ReadPositionCDB()
	result, err := d.session.Execute(ctx, d.lun, cdb, uiscsi.WithDataIn(20))
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

// Rewind repositions the tape to the beginning of the first partition.
// The call blocks until the rewind completes; use ctx for timeout control.
func (d *Drive) Rewind(ctx context.Context) error {
	log := d.log()
	log.Debug("tape: rewind")

	cdb := ssc.RewindCDB(false)
	result, err := d.session.Execute(ctx, d.lun, cdb)
	if err != nil {
		return fmt.Errorf("tape: rewind: %w", err)
	}

	if senseErr := interpretSense(result.Status, result.SenseData); senseErr != nil {
		return fmt.Errorf("tape: rewind: %w", senseErr)
	}
	return nil
}
