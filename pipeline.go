package tape

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"

	"github.com/rkujawa/uiscsi"
	"github.com/rkujawa/uiscsi-tape/internal/ssc"
)

const defaultReadAheadBufSize = 262144 // 256KB per record for variable-block

// readResult carries one pre-fetched tape record through the pipeline.
type readResult struct {
	data []byte // record data, owned by pipeline
	n    int    // bytes of actual data in data
	err  error  // nil on success, *TapeError for tape conditions
}

// readPipeline pre-fetches tape records in a background goroutine.
// It submits multiple SCSI READ commands via StreamExecute and buffers
// the results for Drive.Read to consume, overlapping tape I/O with
// data processing.
type readPipeline struct {
	results chan readResult
	done    chan struct{}
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// Configuration (immutable after start).
	session   *uiscsi.Session
	lun       uint64
	fixed     bool
	sili      bool
	blockSize uint32
	bufSize   int
	logger    *slog.Logger
}

// newReadPipeline creates an unstarted pipeline.
func newReadPipeline(cfg pipelineConfig) *readPipeline {
	bufSize := cfg.bufSize
	if bufSize <= 0 {
		if cfg.blockSize > 0 {
			bufSize = int(cfg.blockSize)
		} else {
			bufSize = defaultReadAheadBufSize
		}
	}
	return &readPipeline{
		results:   make(chan readResult, cfg.depth),
		done:      make(chan struct{}),
		session:   cfg.session,
		lun:       cfg.lun,
		fixed:     cfg.blockSize > 0,
		sili:      cfg.sili,
		blockSize: cfg.blockSize,
		bufSize:   bufSize,
		logger:    cfg.logger,
	}
}

type pipelineConfig struct {
	session   *uiscsi.Session
	lun       uint64
	blockSize uint32
	sili      bool
	depth     int
	bufSize   int
	logger    *slog.Logger
}

// start launches the fetcher goroutine.
func (p *readPipeline) start(ctx context.Context) {
	pCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.wg.Add(1)
	go p.run(pCtx)
}

// stop cancels the fetcher, waits for it to exit, and drains buffered results.
func (p *readPipeline) stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	// Drain any buffered results.
	for {
		select {
		case <-p.results:
		default:
			return
		}
	}
}

// isRunning reports whether the fetcher goroutine is still active.
func (p *readPipeline) isRunning() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// restart stops the current fetcher and starts a fresh one.
func (p *readPipeline) restart(ctx context.Context) {
	p.stop()
	p.done = make(chan struct{})
	p.results = make(chan readResult, cap(p.results))
	p.start(ctx)
}

func (p *readPipeline) run(ctx context.Context) {
	defer close(p.done)
	defer p.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		buf := make([]byte, p.bufSize)
		n, err := p.readOneRecord(ctx, buf)

		result := readResult{data: buf[:max(n, 0)], n: max(n, 0), err: err}

		select {
		case p.results <- result:
		case <-ctx.Done():
			return
		}

		// Terminal conditions stop pre-fetching.
		if err != nil {
			return
		}
	}
}

// readOneRecord issues a single SCSI READ and returns the result.
// This is the same logic as Drive.readSync but doesn't need a Drive reference.
func (p *readPipeline) readOneRecord(ctx context.Context, buf []byte) (int, error) {
	var transferLen, allocLen uint32
	if p.fixed {
		nBlocks := uint32(len(buf)) / p.blockSize
		if nBlocks == 0 {
			return 0, errors.New("tape: pipeline: buffer too small for block size")
		}
		transferLen = nBlocks
		allocLen = nBlocks * p.blockSize
	} else {
		transferLen = uint32(len(buf))
		allocLen = transferLen
	}

	cdb := ssc.ReadCDB(p.fixed, p.sili, transferLen)

	sr, err := p.session.Raw().StreamExecute(ctx, p.lun, cdb, uiscsi.WithDataIn(allocLen))
	if err != nil {
		return 0, err
	}

	n, readErr := io.ReadFull(sr.Data, buf[:allocLen])
	if readErr == io.ErrUnexpectedEOF || readErr == io.EOF {
		readErr = nil
	}
	io.Copy(io.Discard, sr.Data)

	if readErr != nil {
		return n, readErr
	}

	status, senseData, waitErr := sr.Wait()
	if waitErr != nil {
		return n, waitErr
	}

	if senseErr := interpretSense(status, senseData); senseErr != nil {
		p.logger.Debug("tape: pipeline read condition", "n", n, "err", senseErr)
		return n, senseErr
	}

	return n, nil
}
