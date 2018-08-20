package main

import (
	"os"
	"testing"
)

func TestFoo(t *testing.T) {
	f, _ := os.Create("")
	f.Close() // MATCH "require checking"
}

func BenchmarkFoo(b *testing.B) {
	f, _ := os.Create("")
	f.Close()
}

func ExampleFoo() {
	f, _ := os.Create("")
	f.Close()
}
