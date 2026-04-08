// Command uiscsi-tape-dd is a dd-like tool for streaming data between
// local files and iSCSI-attached tape drives via the uiscsi library.
//
// Usage:
//
//	# Write a file to tape:
//	uiscsi-tape-dd -portal 192.168.1.100:3260 -target iqn.example:tape -if data.bin
//
//	# Read from tape to a file:
//	uiscsi-tape-dd -portal 192.168.1.100:3260 -target iqn.example:tape -of data.bin
//
//	# Fixed 64KB blocks, 100 records:
//	uiscsi-tape-dd -portal ... -target ... -if data.bin -bs 65536 -count 100
//
//	# Pipe from stdin:
//	cat data.bin | uiscsi-tape-dd -portal ... -target ... -if -
//
//	# Pipe to stdout:
//	uiscsi-tape-dd -portal ... -target ... -of - > data.bin
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rkujawa/uiscsi"
	tape "github.com/rkujawa/uiscsi-tape"
)

func main() {
	portal := flag.String("portal", "", "iSCSI target portal address (host:port)")
	target := flag.String("target", "", "target IQN")
	lun := flag.Uint64("lun", 0, "LUN number")
	initiatorName := flag.String("initiator-name", "", "initiator IQN (optional)")
	inputFile := flag.String("if", "", "input file (- for stdin); tape is the output")
	outputFile := flag.String("of", "", "output file (- for stdout); tape is the input")
	bs := flag.Uint("bs", 65536, "I/O buffer size in bytes (default 65536)")
	fixed := flag.Bool("fixed", false, "enable fixed-block mode (requires -bs; configures drive via MODE SELECT)")
	count := flag.Uint64("count", 0, "number of records to transfer (0 = until EOF/filemark)")
	seek := flag.Uint64("seek", 0, "skip N records on tape before writing")
	skip := flag.Uint64("skip", 0, "skip N records on tape before reading")
	sili := flag.Bool("sili", false, "suppress incorrect length indicator on short reads")
	decompress := flag.Bool("decompress", false, "enable hardware decompression (for compressed tapes)")
	verbose := flag.Bool("verbose", false, "enable debug logging")
	flag.Parse()

	// Validate arguments.
	if *portal == "" || *target == "" {
		fmt.Fprintf(os.Stderr, "error: -portal and -target are required\n\n")
		flag.Usage()
		os.Exit(1)
	}
	ifSet := *inputFile != ""
	ofSet := *outputFile != ""
	if ifSet == ofSet {
		fmt.Fprintf(os.Stderr, "error: specify exactly one of -if (write to tape) or -of (read from tape)\n")
		os.Exit(1)
	}

	// Logger.
	logLevel := slog.LevelWarn
	if *verbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

	// Signal context.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Build uiscsi options.
	var opts []uiscsi.Option
	opts = append(opts, uiscsi.WithTarget(*target))
	opts = append(opts, uiscsi.WithLogger(logger))
	opts = append(opts, uiscsi.WithMaxRecvDataSegmentLength(1048576)) // 1MB — minimize PDUs per block
	if *initiatorName != "" {
		opts = append(opts, uiscsi.WithInitiatorName(*initiatorName))
	}

	// Connect.
	sess, err := uiscsi.Dial(ctx, *portal, opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: dial %s: %v\n", *portal, err)
		os.Exit(2)
	}
	defer sess.Close()

	// Open tape drive.
	var tapeOpts []tape.Option
	tapeOpts = append(tapeOpts, tape.WithLogger(logger))
	tapeOpts = append(tapeOpts, tape.WithReadAhead(4)) // pre-fetch for throughput
	if *fixed {
		if *bs == 0 {
			fmt.Fprintf(os.Stderr, "error: -fixed requires -bs to specify block size\n")
			os.Exit(1)
		}
		tapeOpts = append(tapeOpts, tape.WithBlockSize(uint32(*bs)))
	}
	if *sili {
		tapeOpts = append(tapeOpts, tape.WithSILI(true))
	}

	drive, err := tape.Open(ctx, sess, *lun, tapeOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open tape LUN %d: %v\n", *lun, err)
		os.Exit(2)
	}
	defer drive.Close(ctx)
	fmt.Fprintf(os.Stderr, "%s %s (rev %s)\n", drive.Info().VendorID, drive.Info().ProductID, drive.Info().Revision)

	if *decompress {
		if err := drive.SetCompression(ctx, true, true); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not enable decompression: %v\n", err)
		}
	}

	// Transfer.
	var st stats
	if ifSet {
		st, err = writeToTape(ctx, drive, *inputFile, uint32(*bs), *count, *seek)
	} else {
		st, err = readFromTape(ctx, drive, *outputFile, uint32(*bs), *count, *skip)
	}

	// Summary (dd-style, to stderr).
	fmt.Fprintf(os.Stderr, "%d+0 records in\n%d+0 records out\n%d bytes transferred\n",
		st.records, st.records, st.bytes)

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
}
