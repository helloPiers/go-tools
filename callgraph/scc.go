package callgraph

func (g *Graph) ComputeSCCs() {
	// use Tarjan to find the SCCs

	index := 1
	var s []*Node

	scc := 0
	var strongconnect func(v *Node)
	strongconnect = func(v *Node) {
		// set the depth index for v to the smallest unused index
		v.index = index
		v.lowlink = index
		index++
		s = append(s, v)
		v.stack = true

		for _, e := range v.Out {
			w := e.Callee
			if w.index == 0 {
				// successor w has not yet been visited; recurse on it
				strongconnect(w)
				if w.lowlink < v.lowlink {
					v.lowlink = w.lowlink
				}
			} else if w.stack {
				// successor w is in stack s and hence in the current scc
				if w.index < v.lowlink {
					v.lowlink = w.index
				}
			}
		}

		if v.lowlink == v.index {
			for {
				w := s[len(s)-1]
				s = s[:len(s)-1]
				w.stack = false
				w.SCC = scc
				if w == v {
					break
				}
			}
			scc++
		}
	}
	for _, v := range g.Nodes {
		if v.index == 0 {
			strongconnect(v)
		}
	}

	for _, n := range g.Nodes {
		n.SCC = scc - n.SCC - 1
	}
}
