package main

import (
	"bufio"
	"os"
)

func main() {
	osfile()
	fmtprint()
	fields()
	misc()
}

func misc() {
	// people handle bufio.Writer errors when calling Flush
	bw := bufio.NewWriter(nil)
	bw.Write(nil)

	f, _ := os.Open("")
	defer f.Close()
	f, _ = os.Create("")
	defer f.Close() // MATCH "require checking"
}
