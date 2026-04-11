package tape

import (
	"context"
	"io"
	"log/slog"
	"sync"

	"github.com/uiscsi/uiscsi"
	"github.com/uiscsi/uiscsi-tape/internal/ssc"
)

const defaultReadAheadBufSize = 262144

// readResult carries one pre-fetched tape record.
type readResult struct {
	data []byte
	n    int
	err  error
}

// pendingRead holds a submitted but not-yet-consumed StreamExecute.
type pendingRead struct {
	sr  *uiscsi.StreamResult
	buf []byte
}

// readPipeline pre-fetches tape records using 2-deep command pipelining.
// Two SCSI READ commands are kept in flight simultaneously: while the
// current response is being consumed, the next command's data is already
// arriving over the network. This hides the network RTT (~5ms) and
// roughly doubles remote throughput.
//
// On filemark: the second in-flight read consumes data from the next
// file. This data is saved (not discarded) and delivered as the first
// result when the pipeline restarts for the next file.
type readPipeline struct {
	results chan readResult
	done    chan struct{}
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	session     *uiscsi.Session
	lun         uint64
	fixed       bool
	sili        bool
	blockSize   uint32
	bufSize     int
	logger      *slog.Logger
	savedResult *readResult // orphaned post-filemark read for next file
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

	// Deliver saved result from previous file's orphaned read.
	if p.savedResult != nil {
		saved := *p.savedResult
		p.savedResult = nil
		select {
		case p.results <- saved:
		case <-ctx.Done():
			return
		}
		if saved.err != nil {
			return
		}
	}

	// Submit first read.
	cur, err := p.submitRead(ctx, transferLen, allocLen)
	if err != nil {
		select {
		case p.results <- readResult{err: err}:
		case <-ctx.Done():
		}
		return
	}

	// Submit second read (look-ahead).
	next, err := p.submitRead(ctx, transferLen, allocLen)
	if err != nil {
		// Can't submit look-ahead — consume single and stop.
		n, consumeErr := p.consumeRead(ctx, cur)
		result := readResult{data: cur.buf[:max(n, 0)], n: max(n, 0), err: consumeErr}
		select {
		case p.results <- result:
		case <-ctx.Done():
		}
		return
	}

	for {
		// Consume current (oldest) read.
		n, consumeErr := p.consumeRead(ctx, cur)
		result := readResult{data: cur.buf[:max(n, 0)], n: max(n, 0), err: consumeErr}

		if consumeErr != nil {
			// Terminal condition (filemark, blank check, error).
			// Consume the look-ahead BEFORE sending the terminal result
			// to the channel. Once the result is delivered, readPipelined
			// may call restart() → stop() → cancel(), which would kill
			// the in-flight StreamExecute for next and lose its data.
			savedN, savedErr := p.consumeRead(ctx, next)
			if savedN > 0 || savedErr == nil {
				p.savedResult = &readResult{
					data: next.buf[:max(savedN, 0)],
					n:    max(savedN, 0),
					err:  savedErr,
				}
				p.logger.Debug("tape: pipeline saved orphaned read",
					"n", savedN, "err", savedErr)
			} else {
				p.logger.Debug("tape: pipeline look-ahead also errored",
					"err", savedErr)
			}

			select {
			case p.results <- result:
			case <-ctx.Done():
			}
			return
		}

		select {
		case p.results <- result:
		case <-ctx.Done():
			p.drainPending(next)
			return
		}

		// Rotate: current = next, submit new look-ahead.
		cur = next
		next, err = p.submitRead(ctx, transferLen, allocLen)
		if err != nil {
			// Can't submit replacement — consume remaining and stop.
			n, consumeErr := p.consumeRead(ctx, cur)
			result := readResult{data: cur.buf[:max(n, 0)], n: max(n, 0), err: consumeErr}
			select {
			case p.results <- result:
			case <-ctx.Done():
			}
			return
		}
	}
}

func (p *readPipeline) submitRead(ctx context.Context, transferLen, allocLen uint32) (pendingRead, error) {
	cdb := ssc.ReadCDB(p.fixed, p.sili, transferLen)
	buf := make([]byte, allocLen)

	sr, err := p.session.Raw().StreamExecute(ctx, p.lun, cdb, uiscsi.WithDataIn(allocLen))
	if err != nil {
		return pendingRead{}, err
	}
	return pendingRead{sr: sr, buf: buf}, nil
}

func (p *readPipeline) consumeRead(_ context.Context, pr pendingRead) (int, error) {
	n, readErr := io.ReadFull(pr.sr.Data, pr.buf)
	if readErr == io.ErrUnexpectedEOF || readErr == io.EOF {
		readErr = nil
	}
	_, _ = io.Copy(io.Discard, pr.sr.Data) // drain remaining data; error irrelevant, status from sr.Wait()

	if readErr != nil {
		_, _, _ = pr.sr.Wait()
		return n, readErr
	}

	status, senseData, waitErr := pr.sr.Wait()
	if waitErr != nil {
		return n, waitErr
	}

	if senseErr := interpretSense(status, senseData); senseErr != nil {
		p.logger.Debug("tape: pipeline read condition", "n", n, "err", senseErr)
		return n, senseErr
	}

	return n, nil
}

func (*readPipeline) drainPending(pr pendingRead) {
	if pr.sr != nil && pr.sr.Data != nil {
		_, _ = io.Copy(io.Discard, pr.sr.Data) // drain remaining data; error irrelevant
		_, _, _ = pr.sr.Wait()
	}
}
