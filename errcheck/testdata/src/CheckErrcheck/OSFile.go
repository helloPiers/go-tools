package main

import (
	"io"
	"os"
)

func file1(c io.Closer) { c.Close() }
func file2(c io.Closer) { c.Close() } // MATCH "require checking"
func file3(c io.Closer) { c.Close() } // MATCH /write-enabled.+OSFile.go:37/
func file4(c io.Closer) { c.Close() }
func file5(c io.Closer) { c.Close() } // MATCH /write-enabled.+OSFile.go:45/
func file6(f *os.File)  { f.Close() }
func file7(f *os.File)  { f.Close() } // MATCH /write-enabled.+OSFile.go:45/

func open(path string) *os.File {
	f, _ := os.Open(path)
	return f
}

func create(path string) *os.File {
	f, _ := os.Create(path)
	return f
}

func osfile() {
	// Closing read-only files shouldn't get flagged
	c1, _ := os.Open("")
	c1.Close()

	// Closing write-enabled files should get flagged
	c2, _ := os.Create("")
	c2.Close() // MATCH "require checking"

	// Same as before, but going through functions and interfaces
	c3, _ := os.Open("")
	c4, _ := os.Create("")
	file1(c3)
	file2(c4)
	file3(c3)
	file3(c4)

	// Same as before, but using custom wrappers for opening files
	c5 := open("")
	c6 := create("")
	file4(c5)
	file5(c6)

	// Same as before, but not using interfaces
	file6(c5)
	file7(c6)

	m := map[string]*os.File{}
	m[""], _ = os.Create("")
	m[""].Close() // MATCH "require checking"
	f, ok := m[""]
	_ = ok
	f.Close() // MATCH "require checking"
}
