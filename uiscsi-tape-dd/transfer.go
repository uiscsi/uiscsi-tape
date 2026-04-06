package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	tape "github.com/rkujawa/uiscsi-tape"
)

type stats struct {
	records uint64
	bytes   uint64
}

// skipRecords reads and discards n records from tape to advance position.
func skipRecords(ctx context.Context, drive *tape.Drive, n uint64, bufSize int) error {
	buf := make([]byte, bufSize)
	for range n {
		_, err := drive.Read(ctx, buf)
		if err != nil {
			return fmt.Errorf("skip: %w", err)
		}
	}
	return nil
}

// writeToTape reads data from inputPath and writes it as records to tape.
func writeToTape(ctx context.Context, drive *tape.Drive, inputPath string, bs uint32, count, seek uint64) (stats, error) {
	var st stats

	// Seek: advance tape position by reading and discarding records.
	if seek > 0 {
		if err := skipRecords(ctx, drive, seek, int(bs)); err != nil {
			return st, fmt.Errorf("seek: %w", err)
		}
	}

	// Open input.
	var input io.Reader
	if inputPath == "-" {
		input = os.Stdin
	} else {
		f, err := os.Open(inputPath)
		if err != nil {
			return st, err
		}
		defer f.Close()
		input = f
	}

	buf := make([]byte, bs)

	for {
		// Check context before each record.
		if ctx.Err() != nil {
			return st, ctx.Err()
		}

		// Read one record's worth from input.
		n, readErr := io.ReadFull(input, buf)
		if n == 0 {
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				break // Clean end of input.
			}
			if readErr != nil {
				return st, fmt.Errorf("read input: %w", readErr)
			}
		}

		// Write to tape. For fixed-block mode, short final records are
		// padded to block size by the caller (dd convention: partial write).
		if err := drive.Write(ctx, buf[:n]); err != nil {
			if errors.Is(err, tape.ErrEOM) {
				fmt.Fprintf(os.Stderr, "warning: end of medium\n")
				st.records++
				st.bytes += uint64(n)
				return st, nil
			}
			return st, fmt.Errorf("write tape: %w", err)
		}

		st.records++
		st.bytes += uint64(n)

		if readErr == io.ErrUnexpectedEOF || readErr == io.EOF {
			break // Short final read — we already wrote it.
		}
		if count > 0 && st.records >= count {
			break
		}
	}

	return st, nil
}

// readFromTape reads records from tape and writes them to outputPath.
func readFromTape(ctx context.Context, drive *tape.Drive, outputPath string, bs uint32, count, skip uint64) (stats, error) {
	var st stats

	// Skip: advance tape position by reading and discarding records.
	if skip > 0 {
		if err := skipRecords(ctx, drive, skip, int(bs)); err != nil {
			return st, fmt.Errorf("skip: %w", err)
		}
	}

	// Open output.
	var output io.Writer
	if outputPath == "-" {
		output = os.Stdout
	} else {
		f, err := os.Create(outputPath)
		if err != nil {
			return st, err
		}
		defer f.Close()
		output = f
	}

	buf := make([]byte, bs)

	for {
		if ctx.Err() != nil {
			return st, ctx.Err()
		}

		n, readErr := drive.Read(ctx, buf)
		if readErr != nil {
			if errors.Is(readErr, tape.ErrFilemark) {
				break // End of file on tape.
			}
			if errors.Is(readErr, tape.ErrBlankCheck) {
				break // No more data on tape.
			}
			if errors.Is(readErr, tape.ErrILI) {
				// ILI: record size on tape differs from buffer size.
				if n < int(bs) {
					// Record shorter than buffer — data is complete.
					fmt.Fprintf(os.Stderr, "warning: record %d: short record (%d bytes, buffer %d); use -sili to suppress\n",
						st.records+1, n, bs)
				} else {
					// Record larger than buffer — DATA TRUNCATED.
					// The excess is lost; tape position advanced past
					// the entire record.
					fmt.Fprintf(os.Stderr, "WARNING: record %d: TRUNCATED (record on tape exceeds %d byte buffer); increase -bs\n",
						st.records+1, bs)
				}
				// Fall through to write whatever data we got.
			} else {
				return st, fmt.Errorf("read tape: %w", readErr)
			}
		}

		if n > 0 {
			if _, err := output.Write(buf[:n]); err != nil {
				return st, fmt.Errorf("write output: %w", err)
			}
			st.records++
			st.bytes += uint64(n)
		}

		if count > 0 && st.records >= count {
			break
		}
	}

	return st, nil
}
