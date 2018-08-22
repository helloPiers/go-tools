package errcheck

import (
	"fmt"
	"go/token"
	"go/types"
	"os"
	"strings"

	. "honnef.co/go/tools/arg"
	"honnef.co/go/tools/callgraph"
	"honnef.co/go/tools/lint"
	. "honnef.co/go/tools/lint/lintdsl"
	"honnef.co/go/tools/pointer"
	"honnef.co/go/tools/ssa"
)

// TODO currently, we use PTA and custom logic for direct function
// calls, but only PTA for indirect function calls. This may lead to
// false positives when passing a type with manually ignored methods
// to another function that calls these methods and returns their
// errors.

// TODO a common case of unhelpful positive is calls to os.Remove in
// unit tests, when cleaning up temporary files.

// TODO functions taking callbacks that only return an error if the
// callback does

// TODO methods on *tabwriter.Writer
// TODO WriteTo
// TODO io.WriteString
// TODO io.Copy, io.CopyN
// TODO calls to bufio.Writer.Flush within the bufio package, since
//   that code checks b.err

type Checker struct {
	db *Knowledge
}

func NewChecker() *Checker {
	return &Checker{}
}

func (c *Checker) Init(prog *lint.Program) {
	c.db = NewKnowledge(prog)
}

func (*Checker) Name() string   { return "errcheck" }
func (*Checker) Prefix() string { return "ERR" }

func (c *Checker) Funcs() map[string]lint.Func {
	return map[string]lint.Func{
		"ERR9999": c.CheckErrors,
	}
}

func ignoreFprint(db *Knowledge, call *callgraph.Edge) (bool, []string) {
	arg0 := db.PTA.Node(call.Site.Common().Args[0])

	for _, l := range arg0.PointsTo().Labels() {
	tracedLoop:
		for _, c := range trace(db.PTA.CallGraph, l.Value()) {
			if c, ok := c.(*ssa.UnOp); ok && c.Op == token.MUL {
				if g, ok := c.X.(*ssa.Global); ok {
					switch g.RelString(nil) {
					case "os.Stdout", "os.Stderr":
						continue tracedLoop
					}
				}
			}

			if T, ok := Dereference(c.Type()).(*types.Named); ok {
				n := T.NumMethods()
				for i := 0; i < n; i++ {
					if meth := T.Method(i); meth.Name() == "Write" {
						fn := db.Prog.SSA.FuncValue(meth)
						if db.PTA.Node(fn).(pointer.Func).Results().At(1).DynamicTypes().Len() == 0 {
							continue tracedLoop
						}
					}
				}
			}

			return false, []string{fmt.Sprintf("first argument is %s", c.Type())}
		}
	}

	return true, nil
}

func drop(db *Knowledge, call *callgraph.Edge) (bool, []string) { return true, nil }

var handlers = map[string]func(db *Knowledge, call *callgraph.Edge) (bool, []string){
	// Nobody cares about resp.Body.Close errors
	"(*net/http.cancelTimerBody).Close": drop,
	"(*net/http.http2gzipReader).Close": drop,
	"(*net/http.body).Close":            drop,
	"(*net/http.bodyEOFSignal).Close":   drop,
	"(*net/http.gzipReader).Close":      drop,

	// Nobody cares about fmt.Print errors
	"fmt.Print":   drop,
	"fmt.Printf":  drop,
	"fmt.Println": drop,

	// Writing to certain destinations either doesn't produce errors, or is irrelevant (stdout, stderr)
	"fmt.Fprint":   ignoreFprint,
	"fmt.Fprintf":  ignoreFprint,
	"fmt.Fprintln": ignoreFprint,

	// A common usage pattern is to ignore the Write errors and
	// only check the error of Flush, because Flush returns any
	// previous Write error.
	"(*bufio.Writer).Write": drop,

	// closing read-only files doesn't produce useful errors
	"(*os.File).Close": func(db *Knowledge, call *callgraph.Edge) (bool, []string) {
		openedReadOnly := func(c ssa.Value) bool {
			var call *ssa.Call
			switch c := c.(type) {
			case *ssa.Extract:
				var ok bool
				call, ok = c.Tuple.(*ssa.Call)
				if !ok {
					// TODO(dh): support this case
					return false
				}
			case *ssa.Call:
				call = c
			default:
				return false
			}

			if CallName(call.Common()) == "os.OpenFile" {
				return db.isReadOnlyOpenFileCall(call)
			}
			return db.IsReadOnlyOpenFileWrapper(call.Common().StaticCallee()) == Yes
		}

		var getPos func(v ssa.Value) token.Pos
		getPos = func(v ssa.Value) token.Pos {
			if v.Pos() != token.NoPos {
				return v.Pos()
			}
			switch v := v.(type) {
			case *ssa.Extract:
				return getPos(v.Tuple)
			case *ssa.MakeInterface:
				return getPos(v.X)
			}
			panic(fmt.Sprintf("unsupported value %T", v))
		}

		var recvs []ssa.Value
		// XXX figuring out the receivers should probably be done
		// by whoever calls the handler, not the handler itself.
		if call.Site.Common().IsInvoke() {
			// interface method
			pts := db.PTA.Node(call.Site.Common().Value).PointsTo()
			for _, l := range pts.Labels() {
				x := l.Value().(*ssa.MakeInterface).X
				if IsType(x.Type(), "*os.File") {
					recvs = append(recvs, trace(db.PTA.CallGraph, x)...)
				}
			}
		} else {
			// static call
			recvs = append(recvs, trace(db.PTA.CallGraph, call.Site.Common().Args[0])...)
		}
		var reasons []string
		for _, recv := range recvs {
			if !openedReadOnly(recv) {
				pos := db.Prog.Fset().Position(getPos(recv))
				reasons = append(reasons, fmt.Sprintf("receiver is write-enabled file created at %s", pos))
			}
		}
		return len(reasons) == 0, reasons
	},
}

func (c *Checker) CheckErrors(j *lint.Job) {
	if c.db == nil {
		// no mains packages
		// TODO(dh): somehow emit a warning
		return
	}

	for _, fn := range j.Program.InitialFunctions {
		if IsInTest(j, fn) && (strings.HasPrefix(fn.Name(), "Benchmark") || strings.HasPrefix(fn.Name(), "Example")) {
			// Don't flag errors in benchmarks
			continue
		}
		node := c.db.PTA.CallGraph.Nodes[fn]
		if node == nil {
			// Function isn't in the call graph. Dead function?
			continue
		}

		unchecked := map[ssa.CallInstruction][]string{}

		for _, call := range node.Out {
			sig := call.Callee.Func.Signature
			if n := sig.Results().Len(); n == 0 || !IsType(sig.Results().At(n-1).Type(), "error") {
				continue
			}
			if v, ok := call.Site.(ssa.Value); ok {
				refs := v.Referrers()
				if refs == nil || len(FilterDebug(*refs)) > 0 {
					continue
				}

				// We check for guaranteed-nil errors in two ways: By
				// checking the function call's value, as well as the
				// combined return values of the called function. This
				// covers context-sensitive calls as well as
				// individual targets in dynamic calls.
				results := c.db.PTA.Node(v)
				res := results
				if results, ok := results.(pointer.Tuple); ok {
					res = results.At(results.Len() - 1)
				}
				if len(res.PointsTo().Labels()) == 0 {
					// this call doesn't return an error
					continue
				}
				{
					results := c.db.PTA.Node(call.Callee.Func).(pointer.Func).Results()
					res = results.At(results.Len() - 1)
					if len(res.PointsTo().Labels()) == 0 {
						// this call doesn't return an error.
						continue
					}
				}
			}

			name := call.Callee.Func.Object().(*types.Func).FullName()
			h, ok := handlers[name]
			var reasons []string
			if call.Site.Common().IsInvoke() {
				reasons = append(reasons, "concrete function is "+name)
			}
			if !ok {
				unchecked[call.Site] = append(unchecked[call.Site], reasons...)
				continue
			}
			ignore, hReasons := h(c.db, call)
			if !ignore {
				reasons = append(reasons, hReasons...)
				unchecked[call.Site] = append(unchecked[call.Site], reasons...)
			}
		}

		for call, reasons := range unchecked {
			s := "make sure the error returned by this function doesn't require checking"
			if len(reasons) > 0 {
				s += "\n\t" + strings.Join(reasons, "\n\t")
			}
			j.Errorf(call, s)
		}
	}
}

func trace(graph *callgraph.Graph, v ssa.Value) []ssa.Value {
	// TODO(dh): handle recursion
	//
	// TODO(dh): we could even support channels, a la guru's "peers".
	// Again, however, we have to figure out how to add queries to the
	// PTA.
	switch v := v.(type) {
	case *ssa.Phi:
		var out []ssa.Value
		for _, e := range v.Edges {
			out = append(out, trace(graph, e)...)
		}
		return out
	case *ssa.Extract:
		return []ssa.Value{v}
	case *ssa.TypeAssert:
		return []ssa.Value{v.X}
	case *ssa.MakeInterface:
		return []ssa.Value{v.X}
	case *ssa.Parameter:
		fn := v.Parent()

		paramIdx := -1
		for i, param := range fn.Params {
			if param == v {
				paramIdx = i
				break
			}
		}
		if paramIdx == -1 {
			panic("internal error: couldn't find index of parameter")
		}
		var out []ssa.Value
		for _, caller := range graph.Nodes[v.Parent()].In {
			out = append(out, trace(graph, caller.Site.Common().Args[paramIdx])...)
		}
		return out
	case *ssa.Call:
		return []ssa.Value{v}
	case *ssa.Const:
		return []ssa.Value{v}
	case *ssa.Alloc:
		return []ssa.Value{v}
	case *ssa.UnOp:
		if v.Op != token.MUL {
			panic(fmt.Sprintf("unsupported token %s (%s)", v.Op, v))
		}
		return trace(graph, v.X)
	case *ssa.FieldAddr:
		// XXX at this point we need PTA
		return []ssa.Value{v.X}
	case *ssa.FreeVar:
		// XXX use PTA
		return []ssa.Value{v}
	case *ssa.Lookup:
		// XXX use PTA
		return []ssa.Value{v}
	default:
		panic(fmt.Sprintf("unsupported type %T (%s)", v, v))
	}
}

//go:generate stringer -type Tristate

type Tristate byte

const (
	Unknown Tristate = iota
	Yes
	No
)

type Knowledge struct {
	Prog *lint.Program

	PTA *pointer.Result

	ReadOnlyOpenFileWrapper map[*ssa.Function]Tristate

	seen map[*ssa.Function]bool
}

func NewKnowledge(prog *lint.Program) *Knowledge {
	var mains []*ssa.Package
	for _, pkg := range prog.InitialPackages {
		if pkg.SSA.Pkg.Name() == "main" {
			if pkg.SSA.Func("main") == nil {
				// A main package without a main function won't link,
				// but it will compile. It will also crash PTA.
				continue
			}
			mains = append(mains, pkg.SSA)
		}
	}

	if len(mains) == 0 {
		return nil
	}

	db := &Knowledge{
		Prog: prog,
		ReadOnlyOpenFileWrapper: map[*ssa.Function]Tristate{},
	}
	db.buildFunctions(mains, prog.AllFunctions)
	return db
}

func (db *Knowledge) IsReadOnlyOpenFileWrapper(fn *ssa.Function) Tristate {
	db.seen = map[*ssa.Function]bool{}
	return db.isReadOnlyOpenFileWrapper(db.PTA.CallGraph, fn)
}

func (db *Knowledge) isReadOnlyOpenFileCall(call *ssa.Call) bool {
	flags := trace(db.PTA.CallGraph, call.Common().Args[Arg("os.OpenFile.flag")])
	for _, flag := range flags {
		k, ok := flag.(*ssa.Const)
		if !ok {
			// TODO(dh): for parameters, we could analyze
			// the call graph.

			// not static flags
			return false
		}
		if k.Int64()&int64(os.O_WRONLY|os.O_RDWR) != 0 {
			// not read-only
			return false
		}
	}
	return true
}

func (db *Knowledge) isReadOnlyOpenFileWrapper(graph *callgraph.Graph, fn *ssa.Function) Tristate {
	if v, ok := db.ReadOnlyOpenFileWrapper[fn]; ok {
		return v
	}
	if db.seen[fn] {
		return Unknown
	}
	db.seen[fn] = true

	resIdx := -1
	res := fn.Signature.Results()
	for i := 0; i < res.Len(); i++ {
		if IsType(res.At(i).Type(), "*os.File") {
			if resIdx != -1 {
				// TODO(dh): support functions that return more than
				// one os.File
				return No
			}
			resIdx = i
		}
	}
	if resIdx == -1 {
		// function doesn't return any *os.Files
		db.ReadOnlyOpenFileWrapper[fn] = No
		return No
	}

	var results []Tristate
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			ret, ok := ins.(*ssa.Return)
			if !ok {
				continue
			}
			v := trace(graph, ret.Results[resIdx])
			for _, vv := range v {
				var call *ssa.Call
				switch vv := vv.(type) {
				case *ssa.Extract:
					call = vv.Tuple.(*ssa.Call)
				case *ssa.Call:
					call = vv
				default:
					continue
				}

				if !IsCallTo(call.Common(), "os.OpenFile") {
					for _, e := range graph.Nodes[fn].Out {
						if e.Site == call {
							results = append(results, db.isReadOnlyOpenFileWrapper(graph, e.Callee.Func))
						}
					}
					continue
				}

				if db.isReadOnlyOpenFileCall(call) {
					results = append(results, Yes)
				} else {
					results = append(results, No)
				}
			}
		}
	}

	v := Unknown
	for _, vv := range results {
		if v == Unknown {
			v = vv
		} else if v == Yes && vv == No {
			v = vv
		}
	}
	if v != Unknown {
		db.ReadOnlyOpenFileWrapper[fn] = v
	}
	return v
}

func (db *Knowledge) buildFunctions(mains []*ssa.Package, fns []*ssa.Function) {
	cfg := &pointer.Config{
		Mains:          mains,
		BuildCallGraph: true,
	}

	var err error
	db.PTA, err = pointer.Analyze(cfg)
	if err != nil {
		// Internal error in PTA
		panic(err)
	}
	db.PTA.CallGraph.ComputeSCCs()
}
