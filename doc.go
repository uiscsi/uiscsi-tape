// Package tape provides a pure-userspace SCSI tape (SSC) driver over iSCSI.
// It wraps a [github.com/rkujawa/uiscsi.Session] to speak SSC commands to
// tape drives, handling sense data interpretation, block limits, and drive
// probing.
//
// For data transfer, use [github.com/rkujawa/uiscsi.Session.StreamExecute]
// which provides bounded-memory streaming suitable for tape's large block
// sizes (256KB–4MB) at sustained throughput (400+ MB/s).
//
// Sense data from raw SCSI commands is parsed via
// [github.com/rkujawa/uiscsi.ParseSenseData] and wrapped into [TapeError]
// with tape-specific condition flags (Filemark, EOM, ILI, BlankCheck).
package tape
