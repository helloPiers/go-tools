package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
)

type alwaysWriter struct{}

func (alwaysWriter) Write([]byte) (int, error) { return 0, nil }

func fmtprint() {
	// Nobody cares when writing to stdout or stderr fails
	fmt.Print("")
	fmt.Printf("")
	fmt.Println("")
	fmt.Fprint(os.Stdout, "")
	fmt.Fprint(os.Stderr, "")

	// No possible write errors
	buf := &bytes.Buffer{}
	fmt.Fprint(buf, "")

	// ... not even on custom types
	fmt.Fprint(alwaysWriter{}, "")

	// ... and people handle bufio.Writer errors when calling Flush
	bw := bufio.NewWriter(nil)
	fmt.Fprint(bw)

	// make sure we're not just ignoring all errors
	f, _ := os.Create("")
	fmt.Fprint(f) // MATCH "unchecked error"
}
