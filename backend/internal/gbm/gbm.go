// Package gbm is a small gradient-boosted decision tree classifier (logistic
// loss), in the style of LightGBM/XGBoost: features are bucketed into
// quantile bins, trees are shallow and grown level by level, and every tree
// sees a random subset of rows and features. Early stopping on a held-out,
// later-in-time slice picks the number of trees.
//
// It exists because trees capture interactions ("momentum works, but only
// when volatility is low") that a linear model cannot, which is where most
// of the published gain from boosted trees on tabular financial data comes
// from. It is deliberately conservative: depth 3, a small learning rate and
// large minimum leaf sizes, because market data is mostly noise.
package gbm

import (
	"math"
	"math/rand/v2"
	"sort"
)

type Params struct {
	Trees        int     // maximum number of trees
	Depth        int     // tree depth
	LearningRate float64 // shrinkage per tree
	MinLeaf      int     // minimum rows in a leaf
	Lambda       float64 // L2 penalty on leaf values
	Subsample    float64 // share of rows per tree
	ColSample    float64 // share of features per tree
	Bins         int     // quantile bins per feature (≤ 255)
	Patience     int     // stop after this many trees without validation improvement
	Seed         uint64
}

func DefaultParams() Params {
	return Params{Trees: 300, Depth: 3, LearningRate: 0.05, MinLeaf: 300, Lambda: 10, Subsample: 0.5, ColSample: 0.8, Bins: 32, Patience: 40, Seed: 1}
}

type Node struct {
	Feature int     `json:"f"`
	Thresh  float64 `json:"t"` // go left if x <= Thresh
	Left    int     `json:"l"` // -1 marks a leaf
	Right   int     `json:"r"`
	Value   float64 `json:"v"` // leaf value (log-odds increment, already shrunk)
}

type Tree struct {
	Nodes []Node `json:"n"`
}

func (t *Tree) eval(x []float64) float64 {
	i := 0
	for {
		n := &t.Nodes[i]
		if n.Left < 0 {
			return n.Value
		}
		if x[n.Feature] <= n.Thresh {
			i = n.Left
		} else {
			i = n.Right
		}
	}
}

// Model is a fitted ensemble.
type Model struct {
	Base       float64   `json:"base"`
	Trees      []Tree    `json:"trees"`
	Importance []float64 `json:"importance"` // total split gain per feature, normalised to 1
}

func (m *Model) Logit(x []float64) float64 {
	z := m.Base
	for i := range m.Trees {
		z += m.Trees[i].eval(x)
	}
	return z
}

func (m *Model) Prob(x []float64) float64 { return 1 / (1 + math.Exp(-m.Logit(x))) }

// Fit trains on (X, y). valX/valY (may be nil) drive early stopping and must
// come strictly after the training rows in time.
func Fit(X [][]float64, y []float64, valX [][]float64, valY []float64, p Params) *Model {
	n := len(X)
	if n == 0 {
		return &Model{}
	}
	d := len(X[0])
	rng := rand.New(rand.NewPCG(p.Seed, 99))

	// Quantile bin edges per feature, then the binned matrix.
	edges := make([][]float64, d)
	sample := make([]float64, 0, min(n, 50000))
	stride := max(1, n/50000)
	for j := 0; j < d; j++ {
		sample = sample[:0]
		for i := 0; i < n; i += stride {
			sample = append(sample, X[i][j])
		}
		sort.Float64s(sample)
		var e []float64
		for b := 1; b < p.Bins; b++ {
			v := sample[b*len(sample)/p.Bins]
			if len(e) == 0 || v > e[len(e)-1] {
				e = append(e, v)
			}
		}
		edges[j] = e
	}
	bins := make([]uint8, n*d)
	for i := 0; i < n; i++ {
		for j := 0; j < d; j++ {
			bins[i*d+j] = uint8(sort.SearchFloat64s(edges[j], X[i][j]))
		}
	}

	base := 0.0
	for _, v := range y {
		base += v
	}
	base = math.Max(1e-4, math.Min(1-1e-4, base/float64(n)))
	m := &Model{Base: math.Log(base / (1 - base)), Importance: make([]float64, d)}

	F := make([]float64, n)
	for i := range F {
		F[i] = m.Base
	}
	var valF []float64
	if len(valX) > 0 {
		valF = make([]float64, len(valX))
		for i := range valF {
			valF[i] = m.Base
		}
	}
	bestLoss, bestTrees, sinceBest := logLoss(valF, valY), 0, 0

	g := make([]float64, n)
	h := make([]float64, n)
	rows := make([]int, 0, n)
	feats := make([]int, 0, d)
	nb := p.Bins + 1
	histG := make([]float64, d*nb)
	histH := make([]float64, d*nb)
	histC := make([]int, d*nb)

	for t := 0; t < p.Trees; t++ {
		for i := 0; i < n; i++ {
			pi := 1 / (1 + math.Exp(-F[i]))
			g[i], h[i] = y[i]-pi, pi*(1-pi)
		}
		rows = rows[:0]
		for i := 0; i < n; i++ {
			if rng.Float64() < p.Subsample {
				rows = append(rows, i)
			}
		}
		feats = feats[:0]
		for j := 0; j < d; j++ {
			if rng.Float64() < p.ColSample {
				feats = append(feats, j)
			}
		}
		if len(feats) == 0 {
			feats = append(feats, rng.IntN(d))
		}

		tree := Tree{}
		type work struct {
			node int
			rows []int
			dep  int
		}
		tree.Nodes = append(tree.Nodes, Node{Left: -1, Right: -1})
		queue := []work{{0, rows, 0}}
		for len(queue) > 0 {
			w := queue[0]
			queue = queue[1:]
			G, H := 0.0, 0.0
			for _, i := range w.rows {
				G += g[i]
				H += h[i]
			}
			leaf := func() { tree.Nodes[w.node].Value = p.LearningRate * G / (H + p.Lambda) }
			if w.dep >= p.Depth || len(w.rows) < 2*p.MinLeaf {
				leaf()
				continue
			}
			clear(histG)
			clear(histH)
			clear(histC)
			for _, i := range w.rows {
				off := i * d
				for _, j := range feats {
					k := j*nb + int(bins[off+j])
					histG[k] += g[i]
					histH[k] += h[i]
					histC[k]++
				}
			}
			parent := G * G / (H + p.Lambda)
			bestGain, bestF, bestB := 0.0, -1, -1
			for _, j := range feats {
				gl, hl, cl := 0.0, 0.0, 0
				for b := 0; b < len(edges[j]); b++ {
					k := j*nb + b
					gl += histG[k]
					hl += histH[k]
					cl += histC[k]
					cr := len(w.rows) - cl
					if cl < p.MinLeaf || cr < p.MinLeaf {
						continue
					}
					gr, hr := G-gl, H-hl
					gain := gl*gl/(hl+p.Lambda) + gr*gr/(hr+p.Lambda) - parent
					if gain > bestGain {
						bestGain, bestF, bestB = gain, j, b
					}
				}
			}
			if bestF < 0 {
				leaf()
				continue
			}
			m.Importance[bestF] += bestGain
			var left, right []int
			for _, i := range w.rows {
				if int(bins[i*d+bestF]) <= bestB {
					left = append(left, i)
				} else {
					right = append(right, i)
				}
			}
			li, ri := len(tree.Nodes), len(tree.Nodes)+1
			tree.Nodes = append(tree.Nodes, Node{Left: -1, Right: -1}, Node{Left: -1, Right: -1})
			tree.Nodes[w.node] = Node{Feature: bestF, Thresh: edges[bestF][bestB], Left: li, Right: ri}
			queue = append(queue, work{li, left, w.dep + 1}, work{ri, right, w.dep + 1})
		}
		m.Trees = append(m.Trees, tree)
		for i := 0; i < n; i++ {
			F[i] += tree.eval(X[i])
		}
		if valF != nil {
			for i := range valF {
				valF[i] += tree.eval(valX[i])
			}
			if l := logLoss(valF, valY); l < bestLoss-1e-9 {
				bestLoss, bestTrees, sinceBest = l, len(m.Trees), 0
			} else if sinceBest++; sinceBest >= p.Patience {
				break
			}
		}
	}
	if valF != nil {
		m.Trees = m.Trees[:bestTrees]
	}
	total := 0.0
	for _, v := range m.Importance {
		total += v
	}
	if total > 0 {
		for j := range m.Importance {
			m.Importance[j] /= total
		}
	}
	return m
}

func logLoss(F, y []float64) float64 {
	if len(F) == 0 {
		return 0
	}
	s := 0.0
	for i, f := range F {
		p := math.Max(1e-9, math.Min(1-1e-9, 1/(1+math.Exp(-f))))
		s -= y[i]*math.Log(p) + (1-y[i])*math.Log(1-p)
	}
	return s / float64(len(F))
}
