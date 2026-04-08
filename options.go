package tape

import "log/slog"

// Option configures Drive behavior.
type Option func(*driveConfig)

type driveConfig struct {
	logger     *slog.Logger
	blockSize  uint32
	sili       bool
	readAhead  int // pipeline depth (0 = disabled)
}

// WithLogger sets a structured logger for drive operations.
func WithLogger(l *slog.Logger) Option {
	return func(c *driveConfig) {
		c.logger = l
	}
}

// WithBlockSize sets the fixed block size for tape I/O.
// A value of 0 selects variable-block mode; any positive value selects
// fixed-block mode with that block size in bytes. When set, [Open]
// configures the drive via MODE SELECT and validates against the drive's
// [BlockLimits].
func WithBlockSize(n uint32) Option {
	return func(c *driveConfig) {
		c.blockSize = n
	}
}

// WithReadAhead enables a read-ahead pipeline that pre-fetches up to
// depth records from tape in a background goroutine. This overlaps tape
// I/O with data processing, significantly improving throughput for
// sequential reads (2-4× typical improvement).
//
// Default is 0 (disabled — synchronous reads). A depth of 4 is a good
// starting point. The pipeline is lazy-started on the first [Drive.Read]
// call and automatically stopped/restarted on tape position changes
// (Write, Rewind, WriteFilemarks, etc.).
//
// The pipeline allocates one buffer per depth slot: blockSize bytes for
// fixed-block mode, or 256KB for variable-block mode.
func WithReadAhead(depth int) Option {
	return func(c *driveConfig) {
		c.readAhead = depth
	}
}

// WithSILI controls the Suppress Incorrect Length Indicator bit on READ
// commands. When true, short reads do not generate CHECK CONDITION with
// ILI sense. Set once on Open, consistent for the session lifetime.
func WithSILI(enabled bool) Option {
	return func(c *driveConfig) {
		c.sili = enabled
	}
}
