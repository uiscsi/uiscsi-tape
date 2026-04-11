package tape_test

import (
	"context"
	"fmt"

	uiscsi "github.com/uiscsi/uiscsi"
	tape "github.com/uiscsi/uiscsi-tape"
)

func ExampleDrive_Read() {
	ctx := context.Background()
	sess, err := uiscsi.Dial(ctx, "192.168.1.100:3260",
		uiscsi.WithTarget("iqn.2026-03.com.example:tape"),
		uiscsi.WithMaxRecvDataSegmentLength(262144),
	)
	if err != nil {
		return
	}
	defer sess.Close()

	drive, err := tape.Open(ctx, sess, 0,
		tape.WithBlockSize(65536),
	)
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer drive.Close(ctx)

	buf := make([]byte, 65536)
	n, err := drive.Read(ctx, buf)
	if err != nil {
		fmt.Println("read:", err)
		return
	}
	fmt.Printf("read %d bytes\n", n)
}
