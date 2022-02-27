package graph

import (
	"math"

	"github.com/0xnibbler/mev-q4-2020/model"

	"github.com/soniakeys/bits"
)

// copied from github.com/soniakeys/graph

type LabeledAdjacencyList [][]model.Half

type LabeledDirected struct {
	LabeledAdjacencyList // embedded to include LabeledAdjacencyList methods
}

type PathEnd struct {
	From int32  // a "from" half arc, the node the arc comes from
	Len  int // number of nodes in path from start
}

type FromList struct {
	Paths  []PathEnd // tree representation
	Leaves bits.Bits // leaves of tree
	MaxLen int       // length of longest path, max of all PathEnd.Len values
}

func NewFromList(n int) FromList {
	return FromList{Paths: make([]PathEnd, n)}
}

func (g LabeledDirected) BellmanFord(start int32) (f FromList, labels []float64, dist []float64, end int32) {
	a := g.LabeledAdjacencyList
	f = NewFromList(len(a))
	labels = make([]float64, len(a))
	dist = make([]float64, len(a))
	inf := math.Inf(1)
	for i := range dist {
		dist[i] = inf
	}
	rp := f.Paths
	rp[start] = PathEnd{Len: 1, From: -1}
	dist[start] = 0
	for _ = range a[1:] {
		imp := false
		for from, nbs := range a {
			fp := &rp[from]
			d1 := dist[from]
			for _, nb := range nbs {
				d2 := d1 + nb.Weight
				to := &rp[nb.To]
				// TODO improve to break ties
				if fp.Len > 0 && d2 < dist[nb.To] {
					*to = PathEnd{From: int32(from), Len: fp.Len + 1}
					labels[nb.To] = nb.Weight
					dist[nb.To] = d2
					imp = true
				}
			}
		}
		if !imp {
			break
		}
	}
	for from, nbs := range a {
		d1 := dist[from]
		for _, nb := range nbs {
			if d1+nb.Weight < dist[nb.To] {
				// return nb as end of a path with negative cycle at root
				return f, labels, dist, int32(from)
			}
		}
	}
	return f, labels, dist, -1
}

// BellmanFordCycle decodes a negative cycle detected by BellmanFord.
//
// Receiver f and argument end must be results returned from BellmanFord.
func (f FromList) BellmanFordCycle(end int32) (c []int32) {
	p := f.Paths
	b := bits.New(len(p))
	for b.Bit(int(end)) == 0 {
		b.SetBit(int(end), 1)
		end = p[end].From
	}
	for b.Bit(int(end)) == 1 {
		c = append(c, end)
		b.SetBit(int(end), 0)
		end = p[end].From
	}
	for i, j := 0, len(c)-1; i < j; i, j = i+1, j-1 {
		c[i], c[j] = c[j], c[i]
	}
	return
}

func (g LabeledDirected) Copy() (c LabeledDirected, ma int) {
	l, s := g.LabeledAdjacencyList.Copy()
	return LabeledDirected{l}, s
}

func (g LabeledAdjacencyList) Copy() (c LabeledAdjacencyList, ma int) {
	c = make(LabeledAdjacencyList, len(g))
	for n, to := range g {
		c[n] = append([]model.Half{}, to...)
		ma += len(to)
	}
	return
}
