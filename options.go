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

// WithReadAhead enables a 2-deep command pipeline that overlaps network
// RTT with data consumption. While the current record is being processed,
// the next SCSI READ is already in flight.
//
// Default is 0 (disabled — synchronous reads). Any positive value enables
// pipelining. The depth parameter is reserved for future use; currently
// the pipeline always uses 2-deep regardless of the value.
//
// On filemark boundaries, the look-ahead read's data is saved and
// delivered as the first record of the next file (no data loss).
//
// The pipeline is lazy-started on the first [Drive.Read] call and
// automatically stopped on tape position changes (Write, Rewind, etc.).
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
