# uiscsi-tape

A pure-userspace SCSI tape (SSC) driver over iSCSI, built on [uiscsi](https://github.com/rkujawa/uiscsi).

**Status:** v0.3.0 -- full record-oriented tape I/O: Read, Write, WriteFilemarks, Rewind. Variable and fixed block modes. 2-deep command pipelining. Bounded-memory streaming via `sess.Raw().StreamExecute`.

## Overview

uiscsi-tape wraps a `uiscsi.Session` to speak SSC (SCSI Stream Commands) to tape drives over iSCSI. It handles drive probing, block limit negotiation, record-oriented I/O, and tape-specific error conditions (filemark, end-of-medium, blank check, incorrect length).

Read operations use `sess.Raw().StreamExecute` internally for bounded-memory streaming (~64KB) suitable for tape's large block sizes (256KB-4MB) at sustained throughput (400+ MB/s). Drive configuration (block size, compression) uses `sess.SCSI().ModeSelect6`.

## Usage

```go
import (
    "github.com/rkujawa/uiscsi"
    tape "github.com/rkujawa/uiscsi-tape"
)

// Connect to an iSCSI target.
// For tape performance, increase MaxRecvDataSegmentLength from the
// default 8KB. 256KB is a good choice for LTO drives.
sess, err := uiscsi.Dial(ctx, "192.168.1.100:3260",
    uiscsi.WithTarget("iqn.2026-03.com.example:tape"),
    uiscsi.WithMaxRecvDataSegmentLength(262144),
)
if err != nil { log.Fatal(err) }
defer sess.Close()

// Probe the tape drive.
drive, err := tape.Open(ctx, sess, 0)
if err != nil { log.Fatal(err) }

fmt.Printf("Drive: %s %s\n", drive.Info().VendorID, drive.Info().ProductID)
fmt.Printf("Block limits: min=%d max=%d\n", drive.Limits().MinBlock, drive.Limits().MaxBlock)

// Write a record.
if err := drive.Write(ctx, []byte("hello tape")); err != nil {
    log.Fatal(err)
}

// Write a filemark (record separator).
if err := drive.WriteFilemarks(ctx, 1); err != nil {
    log.Fatal(err)
}

// Rewind and read back.
if err := drive.Rewind(ctx); err != nil {
    log.Fatal(err)
}

buf := make([]byte, 65536)
n, err := drive.Read(ctx, buf)
if err != nil { log.Fatal(err) }
fmt.Printf("Read %d bytes: %s\n", n, buf[:n])
```

## Features

- **Drive probing** -- TEST UNIT READY + INQUIRY (device type 0x01 check) + READ BLOCK LIMITS
- **Record I/O** -- `Read` and `Write` for record-oriented tape access
- **Tape control** -- `WriteFilemarks` for logical record separation, `Rewind` for repositioning, `Position` for block position query, `Close` for cleanup
- **Variable-block mode** -- default, each record can be a different size
- **Fixed-block mode** -- via `WithBlockSize(n)`, configures drive via MODE SELECT and reads/writes in fixed-size blocks
- **SILI support** -- via `WithSILI(true)`, suppresses ILI on short reads
- **Hardware compression** -- `Compression`/`SetCompression` for drive-level compression (LTO)
- **Read-ahead pipeline** -- `WithReadAhead(1)` enables 2-deep command pipelining, hiding network RTT
- **Bounded-memory streaming** -- Read uses `sess.Raw().StreamExecute` (~64KB peak memory regardless of block size)
- **Tape-specific errors** -- `TapeError` with Filemark, EOM, ILI, BlankCheck condition flags
- **Sentinel errors** -- `ErrFilemark`, `ErrEOM`, `ErrBlankCheck`, `ErrILI`, `ErrNotTape` for `errors.Is` matching
- **Sense parsing** -- uses `uiscsi.ParseSenseData` for SPC-4 parsing, adds tape-specific wrapping

## API

| Function/Type | Description |
|---------------|-------------|
| `Open` | Probe a LUN and return a `Drive` if it is a tape device |
| `Drive.Read` | Read one record from current position into buffer |
| `Drive.Write` | Write one record at current position |
| `Drive.WriteFilemarks` | Write N filemarks at current position |
| `Drive.Rewind` | Reposition to beginning of tape |
| `Drive.Position` | Query current logical block number (READ POSITION) |
| `Drive.BlockSize` | Query drive's current block size (MODE SENSE) |
| `Drive.SetBlockSize` | Set drive's block size (MODE SELECT) |
| `Drive.Compression` | Query drive compression settings |
| `Drive.SetCompression` | Enable/disable hardware compression |
| `Drive.Close` | Restore variable-block mode if fixed was configured |
| `Drive.Info` | Drive identification from INQUIRY |
| `Drive.Limits` | Block size limits from READ BLOCK LIMITS |
| `WithBlockSize` | Configure fixed-block mode (0 = variable, default) |
| `WithReadAhead` | Pre-fetch depth for sequential read throughput (0 = disabled) |
| `WithSILI` | Suppress Incorrect Length Indicator on short reads |
| `WithLogger` | Inject `slog.Logger` for diagnostics |
| `TapeError` | Error type with tape condition flags |
| `ErrFilemark` | Sentinel: filemark encountered during read |
| `ErrEOM` | Sentinel: end-of-medium reached |
| `ErrBlankCheck` | Sentinel: blank/unwritten area encountered |
| `ErrILI` | Sentinel: incorrect length (block size mismatch) |

## Error Handling

Tape conditions are returned as `*TapeError` supporting `errors.Is`:

```go
n, err := drive.Read(ctx, buf)
if errors.Is(err, tape.ErrFilemark) {
    // Filemark -- logical end of file on tape. Normal condition.
}
if errors.Is(err, tape.ErrEOM) {
    // End of medium -- stop writing soon.
}
if errors.Is(err, tape.ErrBlankCheck) {
    // No more data on tape.
}
if errors.Is(err, tape.ErrILI) {
    // Record was shorter than buffer (n has actual bytes read).
    // Use WithSILI(true) to suppress this and get (n, nil) instead.
}
```

## Requirements

- Go 1.25 or later
- [github.com/rkujawa/uiscsi](https://github.com/rkujawa/uiscsi) v1.3.0 or later
