package tape

import (
	"context"
	"io"
	"log/slog"
	"sync"

	"github.com/rkujawa/uiscsi"
	"github.com/rkujawa/uiscsi-tape/internal/ssc"
)

const defaultReadAheadBufSize = 262144

// readResult carries one pre-fetched tape record.
type readResult struct {
	data []byte
	n    int
	err  error
}

// readPipeline pre-fetches the NEXT tape record while the caller
// processes the current one. This overlaps one SCSI READ command
// with data consumption, roughly doubling sequential throughput.
//
// Only one read is ever in flight — this avoids consuming records
// past a filemark boundary (which would be discarded and lost).
type readPipeline struct {
	results chan readResult
	done    chan struct{}
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	session   *uiscsi.Session
	lun       uint64
	fixed     bool
	sili      bool
	blockSize uint32
	bufSize   int
	logger    *slog.Logger
}

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
		results:   make(chan readResult, 1),
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

func (p *readPipeline) start(ctx context.Context) {
	pCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.wg.Add(1)
	go p.run(pCtx)
}

func (p *readPipeline) stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	for {
		select {
		case <-p.results:
		default:
			return
		}
	}
}

func (p *readPipeline) isRunning() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *readPipeline) restart(ctx context.Context) {
	p.stop()
	p.done = make(chan struct{})
	p.results = make(chan readResult, 1)
	p.start(ctx)
}

func (p *readPipeline) run(ctx context.Context) {
	defer close(p.done)
	defer p.wg.Done()

	var transferLen, allocLen uint32
	if p.fixed {
		nBlocks := uint32(p.bufSize) / p.blockSize
		transferLen = nBlocks
		allocLen = nBlocks * p.blockSize
	} else {
		transferLen = uint32(p.bufSize)
		allocLen = transferLen
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Submit one read.
		buf := make([]byte, allocLen)
		n, err := p.readOneRecord(ctx, transferLen, allocLen, buf)

		result := readResult{data: buf[:max(n, 0)], n: max(n, 0), err: err}

		select {
		case p.results <- result:
		case <-ctx.Done():
			return
		}

		if err != nil {
			return
		}
	}
}

// readOneRecord submits a StreamExecute, then immediately returns the
// StreamResult handle. The actual data consumption happens next, while
// the SCSI command for the FOLLOWING record can already be queued by
// the session's command window.
func (p *readPipeline) readOneRecord(ctx context.Context, transferLen, allocLen uint32, buf []byte) (int, error) {
	cdb := ssc.ReadCDB(p.fixed, p.sili, transferLen)

	sr, err := p.session.Raw().StreamExecute(ctx, p.lun, cdb, uiscsi.WithDataIn(allocLen))
	if err != nil {
		return 0, err
	}

	n, readErr := io.ReadFull(sr.Data, buf)
	if readErr == io.ErrUnexpectedEOF || readErr == io.EOF {
		readErr = nil
	}
	io.Copy(io.Discard, sr.Data)

	if readErr != nil {
		sr.Wait()
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
