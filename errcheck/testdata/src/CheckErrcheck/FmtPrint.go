package main

import (
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

	// make sure we're not just ignoring all errors
	f, _ := os.Create("")
	fmt.Fprint(f) // MATCH "require checking"
}
