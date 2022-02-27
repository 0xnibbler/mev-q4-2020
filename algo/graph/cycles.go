package graph

import (
	"math"

	"github.com/0xnibbler/mev-q4-2020/model"
)

func (g LabeledDirected) Cycles(emit func([]model.Half) bool) {
	a := g.LabeledAdjacencyList
	k := make(LabeledAdjacencyList, len(a))
	B := make([]map[int32]bool, len(a))
	blocked := make([]bool, len(a))
	for i := range a {
		blocked[i] = true
		B[i] = map[int32]bool{}
	}
	var s int32
	var stack []model.Half
	var unblock func(int32)
	unblock = func(u int32) {
		blocked[u] = false
		for w := range B[u] {
			delete(B[u], w)
			if blocked[w] {
				unblock(w)
			}
		}
	}
	var circuit func(int32) (bool, bool)
	circuit = func(v int32) (found, ok bool) {
		f := false
		blocked[v] = true
		for _, w := range k[v] {
			if w.To == s {
				if !emit(append(stack, w)) {
					return
				}
				f = true
			} else if !blocked[w.To] {
				stack = append(stack, w)
				switch found, ok = circuit(w.To); {
				case !ok:
					return
				case found:
					f = true
				}
				stack = stack[:len(stack)-1]
			}
		}
		if f {
			unblock(v)
		} else {
			for _, w := range k[v] {
				B[w.To][v] = true
			}
		}
		return f, true
	}
	for s = 0; int(s) < len(a); s++ {
		for z := int32(0); z < s; z++ {
			k[z] = nil
		}
		for z := int(s); z < len(a); z++ {
			k[z] = a[z]
		}
		var scc []int32
		LabeledDirected{k}.StronglyConnectedComponents(func(c []int32) bool {
			for _, n := range c {
				if n == s {
					scc = c
					return false
				}
			}
			return true
		})
		for n := range k {
			k[n] = nil
		}
		for _, n := range scc {
			blocked[n] = false
		}
		for _, fr := range scc {
			var kt []model.Half
			for _, to := range a[fr] {
				if !blocked[to.To] {
					kt = append(kt, to)
				}
			}
			k[fr] = kt
		}
		if _, ok := circuit(s); !ok {
			return
		}
		for _, n := range scc {
			blocked[n] = true
		}
	}
}

func (g LabeledDirected) StronglyConnectedComponents(emit func([]int32) bool) {
	// See Algorithm 3 PEA FIND SCC2(V,E) in "An Improved Algorithm for
	// Finding the Strongly Connected Components of a Directed Graph"
	// by David J. Pearce.
	a := g.LabeledAdjacencyList
	rindex := make([]int, len(a))
	var S, scc []int32
	index := 1
	c := len(a) - 1
	var visit func(int32) bool
	visit = func(v int32) bool {
		root := true
		rindex[v] = index
		index++
		for _, w := range a[v] {
			if rindex[w.To] == 0 {
				if !visit(w.To) {
					return false
				}
			}
			if rindex[w.To] < rindex[v] {
				rindex[v] = rindex[w.To]
				root = false
			}
		}
		if !root {
			S = append(S, v)
			return true
		}
		scc = scc[:0]
		index--
		for last := len(S) - 1; last >= 0; last-- {
			w := S[last]
			if rindex[v] > rindex[w] {
				break
			}
			S = S[:last]
			rindex[w] = c
			scc = append(scc, w)
			index--
		}
		rindex[v] = c
		c--
		return emit(append(scc, v))
	}
	for v := range a {
		if rindex[v] == 0 && !visit(int32(v)) {
			break
		}
	}
}

type DistanceMatrix [][]float64

func (g LabeledAdjacencyList) DistanceMatrix() (d DistanceMatrix) {
	d = newDM(len(g))
	for fr, to := range g {
		for _, to := range to {
			// < to pick min of parallel arcs (also nicely ignores NaN)
			if wt := to.Weight; wt < d[fr][to.To] {
				d[fr][to.To] = wt
			}
		}
	}
	return
}

func newDM(n int) DistanceMatrix {
	inf := math.Inf(1)
	d := make(DistanceMatrix, n)
	for i := range d {
		di := make([]float64, n)
		for j := range di {
			di[j] = inf
		}
		di[i] = 0
		d[i] = di
	}
	return d
}
