package main

import (
	"fmt"
	"io"
	"os"
)

type T1 struct{ w io.Writer }
type T2 struct{ w io.Writer }

func (t1 T1) Foo() { fmt.Fprint(t1.w, "") }
func (t2 T2) Foo() { fmt.Fprint(t2.w, "") } // MATCH "*os.File"

func fields() {
	T1{os.Stdout}.Foo()
	f, _ := os.Create("")
	T2{f}.Foo()
}
