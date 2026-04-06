// Package tape provides a pure-userspace SCSI tape (SSC) driver over iSCSI.
// It wraps a uiscsi.Session to speak SSC commands to tape drives,
// handling sense data interpretation, block limits, and drive probing.
package tape
