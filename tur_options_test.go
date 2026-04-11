package tape

import (
	"testing"
	"time"
)

// TestTUROptionDefaults verifies that zero-value TUR options use defaults
// per D-02: zero means "use default", not "disable".
func TestTUROptionDefaults(t *testing.T) {
	var cfg driveConfig

	// Apply zero-value options -- should not change defaults.
	WithTURRetryInterval(0)(&cfg)
	WithTURMaxRetries(0)(&cfg)

	// Zero in config means "resolve to default at use site".
	if cfg.turRetryInterval != 0 {
		t.Errorf("WithTURRetryInterval(0) set interval to %v, want 0 (resolve later)", cfg.turRetryInterval)
	}
	if cfg.turMaxRetries != 0 {
		t.Errorf("WithTURMaxRetries(0) set retries to %v, want 0 (resolve later)", cfg.turMaxRetries)
	}
}

// TestTUROptionOverrides verifies that non-zero TUR options override defaults.
func TestTUROptionOverrides(t *testing.T) {
	var cfg driveConfig

	WithTURRetryInterval(50 * time.Millisecond)(&cfg)
	WithTURMaxRetries(5)(&cfg)

	if cfg.turRetryInterval != 50*time.Millisecond {
		t.Errorf("turRetryInterval = %v, want 50ms", cfg.turRetryInterval)
	}
	if cfg.turMaxRetries != 5 {
		t.Errorf("turMaxRetries = %d, want 5", cfg.turMaxRetries)
	}
}
