package tape

import "log/slog"

// Option configures Drive behavior.
type Option func(*driveConfig)

type driveConfig struct {
	logger    *slog.Logger
	blockSize uint32
	sili      bool
}

// WithLogger sets a structured logger for drive operations.
func WithLogger(l *slog.Logger) Option {
	return func(c *driveConfig) {
		c.logger = l
	}
}

// WithBlockSize sets the fixed block size for tape I/O.
// A value of 0 selects variable-block mode; any positive value selects
// fixed-block mode with that block size in bytes. Validation against the
// drive's BlockLimits is deferred to Open.
func WithBlockSize(n uint32) Option {
	return func(c *driveConfig) {
		c.blockSize = n
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
