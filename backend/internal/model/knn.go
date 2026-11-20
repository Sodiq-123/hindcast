package model

import (
	"container/heap"
	"math"
)

// Analogs is a k-nearest-neighbour "historical analog" model: it finds the K
// past days whose standardised feature vector looked most like today's and
// reports what happened next. It captures non-linear interactions the
// logistic model cannot, and its evidence ("62 of the 100 most similar days
// rose") is directly checkable by a human.
type Analogs struct {
	Z       [][]float64 // standardised training rows
	Y       []float64   // 1 if forward return > 0
	Ret     []float64   // forward log return
	RetNorm []float64   // forward return / (daily vol × √h)
	Index   []int       // original bar index of each training row
	K       int
	Prior   float64 // base rate used to shrink small samples
}

// Neighbor is one historical analog.
type Neighbor struct {
	Index    int
	Distance float64
	Y        float64
	Ret      float64
	RetNorm  float64
}

type nbHeap []Neighbor // max-heap on distance

func (h nbHeap) Len() int           { return len(h) }
func (h nbHeap) Less(i, j int) bool { return h[i].Distance > h[j].Distance }
func (h nbHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *nbHeap) Push(x any)        { *h = append(*h, x.(Neighbor)) }
func (h *nbHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

func KFor(nTrain int) int {
	k := int(math.Sqrt(float64(nTrain)))
	return max(25, min(150, k))
}

// Neighbors returns the K nearest training rows, nearest first.
func (a *Analogs) Neighbors(z []float64) []Neighbor {
	h := make(nbHeap, 0, a.K+1)
	for i, r := range a.Z {
		d := 0.0
		for j, v := range r {
			diff := v - z[j]
			d += diff * diff
			if len(h) == a.K && d > h[0].Distance {
				break
			}
		}
		if len(h) < a.K {
			heap.Push(&h, Neighbor{a.Index[i], d, a.Y[i], a.Ret[i], a.RetNorm[i]})
		} else if d < h[0].Distance {
			h[0] = Neighbor{a.Index[i], d, a.Y[i], a.Ret[i], a.RetNorm[i]}
			heap.Fix(&h, 0)
		}
	}
	out := make([]Neighbor, len(h))
	for i := len(h) - 1; i >= 0; i-- {
		out[i] = heap.Pop(&h).(Neighbor)
	}
	for i := range out {
		out[i].Distance = math.Sqrt(out[i].Distance)
	}
	return out
}

// Prob turns neighbours into a probability, shrunk toward the prior with a
// pseudo-count so a handful of analogs cannot produce extreme estimates.
func (a *Analogs) Prob(nb []Neighbor) float64 {
	const pseudo = 20.0
	up := 0.0
	for _, n := range nb {
		up += n.Y
	}
	return (up + pseudo*a.Prior) / (float64(len(nb)) + pseudo)
}
