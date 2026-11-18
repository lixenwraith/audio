package bank

import (
	"cmp"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/lixenwraith/audio"
)

var fitStruck = flag.Bool("fit", false, "fit a struck sound's genes to each bank drum and print what the fit keeps")

// struckPiece is a bank drum as a fit hears it, its six takes at full velocity,
// the floor (the mean distance between two of them) and its fit, a value for
// every row of the genome, at its distance, the mean to the takes
type struckPiece struct {
	name     string
	takes    [][][]float32
	floor, d float64
	x        []float64
}

// distance is how far a value for each row of the genome, rendered as a preview
// plays it, is from the takes
func (p *struckPiece) distance(rows []float64) float64 {
	d := audio.StruckFromRows(rows)
	d.Name = "struck"
	x, err := audio.RenderPreview(d, audio.SoundShape{})
	if err != nil {
		return math.Inf(1)
	}
	f := audio.HitFrames(x, audio.AudioSampleRate)
	var sum float64
	for _, take := range p.takes {
		sum += audio.MelDistance(f, take)
	}
	return sum / float64(len(p.takes))
}

// A struck sound's genes reach each bank drum: a fit of each over the genome's
// rows prints its distance to the drum's takes and its floor, then a row is held
// at its median over the fits, the rest fitted again, while that costs each drum
// less than its floor over its fit; the rows kept are the genes, and the genome
// holds each other row where the fit held it.
func TestStruckGenesReachTheBankDrums(t *testing.T) {
	if !*fitStruck {
		t.Skip("-fit fits the struck genome to the bank drums, half an hour on four cores: -timeout 2h")
	}
	names := audio.StruckRowNames()
	var pieces []*struckPiece
	for _, d := range loadSounds(t) {
		if !slices.Contains([]string{"kick", "snare", "hihat", "clap"}, d.Name) {
			continue
		}
		p := &struckPiece{name: d.Name}
		for n := range 6 {
			x, err := audio.RenderHit(d, audio.SoundShape{}, 1, n)
			if err != nil {
				t.Fatal(err)
			}
			p.takes = append(p.takes, audio.HitFrames(x, audio.AudioSampleRate))
		}
		var pairs int
		for i := range p.takes {
			for j := i + 1; j < len(p.takes); j++ {
				p.floor += audio.MelDistance(p.takes[i], p.takes[j])
				pairs++
			}
		}
		p.floor /= float64(pairs)
		prev, err := audio.RenderPreview(d, audio.SoundShape{})
		if err != nil {
			t.Fatal(err)
		}
		var own float64
		for _, take := range p.takes {
			own += audio.MelDistance(audio.HitFrames(prev, audio.AudioSampleRate), take)
		}
		own /= float64(len(p.takes))
		p.x, p.d = fitStruckGenes(p.distance)
		pieces = append(pieces, p)
		t.Logf("%-6s distance %6.2f  floor %6.2f  (%4.2f floors; the bank's own preview %4.2f)", p.name, p.d, p.floor, p.d/p.floor, own/p.floor)
		t.Logf("%-6s genes %s", p.name, geneLine(names, p.x))
	}
	full := make([]float64, len(pieces))
	for i, p := range pieces {
		full[i] = p.d
	}
	held := make([]bool, len(names))
	for {
		type hold struct {
			gene  int
			at    float64
			cost  float64
			costs []float64
			fits  [][]float64
			ds    []float64
		}
		var rows []hold
		for i := range names {
			if held[i] {
				continue
			}
			v := make([]float64, len(pieces))
			for k, p := range pieces {
				v[k] = p.x[i]
			}
			slices.Sort(v)
			// its median, to the three places the genome's table holds it at
			h := hold{gene: i, at: math.Round((v[(len(v)-1)/2]+v[len(v)/2])/2*1000) / 1000}
			for k, p := range pieces {
				free := slices.Clone(held)
				free[i] = true
				for j := range free {
					free[j] = !free[j]
				}
				x := slices.Clone(p.x)
				x[i] = h.at
				x, d := compass(p.distance, x, free, 0.1, 0.001)
				c := (d - full[k]) / p.floor
				h.costs, h.fits, h.ds = append(h.costs, c), append(h.fits, x), append(h.ds, d)
				h.cost = max(h.cost, c)
			}
			rows = append(rows, h)
		}
		slices.SortStableFunc(rows, func(a, b hold) int { return cmp.Compare(a.cost, b.cost) })
		var b strings.Builder
		fmt.Fprintf(&b, "%-12s %6s", "held", "at")
		for _, p := range pieces {
			fmt.Fprintf(&b, " %6s", p.name)
		}
		for _, r := range rows {
			fmt.Fprintf(&b, "\n%-12s %6.3f", names[r.gene], r.at)
			for _, c := range r.costs {
				fmt.Fprintf(&b, " %6.2f", c)
			}
		}
		t.Logf("each gene held at its median, what it costs each drum in floors:\n%s", b.String())
		if len(rows) == 0 || rows[0].cost >= 1 {
			break
		}
		r := rows[0]
		held[r.gene] = true
		for k, p := range pieces {
			p.x, p.d = r.fits[k], r.ds[k]
		}
		t.Logf("held %s at %.3f", names[r.gene], r.at)
	}
	var kept []string
	for i, n := range names {
		if !held[i] {
			kept = append(kept, n)
		}
	}
	t.Logf("kept %d of %d: %s", len(kept), len(names), strings.Join(kept, " "))
	genes := make([][]float64, len(pieces))
	for k, p := range pieces {
		for i, x := range p.x {
			if !held[i] {
				genes[k] = append(genes[k], x)
			}
		}
		t.Logf("%-6s distance %6.2f  floor %6.2f  (%4.2f floors) in the kept genes:%s", p.name, p.d, p.floor, p.d/p.floor, geneLine(kept, genes[k]))
	}
	if names := audio.StruckGeneNames(); !slices.Equal(kept, names) {
		t.Fatalf("the fit keeps %v, the genome %v", kept, names)
	}
	for k, p := range pieces {
		if !reflect.DeepEqual(audio.StruckFromGenes(genes[k]), audio.StruckFromRows(p.x)) {
			t.Errorf("%s: the genome holds a row other than where the fit held it", p.name)
		}
	}
}

// geneLine is genes by name
func geneLine(names []string, x []float64) string {
	var b strings.Builder
	for i, n := range names {
		fmt.Fprintf(&b, " %s=%.3f", n, x[i])
	}
	return b.String()
}

// fitStruckGenes is the least of f found over the genome's rows: a compass walk
// from each of 24 starts spread over every row's range as a Latin hypercube, the
// best three walked on at a finer step
func fitStruckGenes(f func([]float64) float64) ([]float64, float64) {
	const starts = 24
	n := len(audio.StruckRowNames())
	all := make([]bool, n)
	strata := make([][]int, n)
	r := rand.New(rand.NewPCG(1, 1))
	for i := range n {
		all[i], strata[i] = true, r.Perm(starts)
	}
	type fit struct {
		x []float64
		f float64
	}
	fits := make([]fit, starts)
	for s := range fits {
		x0 := make([]float64, n)
		for i := range x0 {
			x0[i] = (float64(strata[i][s]) + 0.5) / starts
		}
		fits[s].x, fits[s].f = compass(f, x0, all, 0.2, 0.01)
	}
	slices.SortStableFunc(fits, func(a, b fit) int { return cmp.Compare(a.f, b.f) })
	best := fit{f: math.Inf(1)}
	for _, c := range fits[:3] {
		if x, fx := compass(f, c.x, all, 0.01, 0.001); fx < best.f {
			best = fit{x, fx}
		}
	}
	return best.x, best.f
}

// compass walks x to a least of f: each free gene up and down by step, the best
// move taken, the step halved when none helps, until it is under stop
func compass(f func([]float64) float64, x0 []float64, free []bool, step, stop float64) ([]float64, float64) {
	x := slices.Clone(x0)
	fx := f(x)
	type move struct {
		i    int
		v, f float64
	}
	for step >= stop {
		var moves []move
		for i := range x {
			for _, s := range []float64{-step, step} {
				if v := min(max(x[i]+s, 0), 1); free[i] && v != x[i] {
					moves = append(moves, move{i: i, v: v})
				}
			}
		}
		var wg sync.WaitGroup
		for k := range moves {
			wg.Go(func() {
				y := slices.Clone(x)
				y[moves[k].i] = moves[k].v
				moves[k].f = f(y)
			})
		}
		wg.Wait()
		best := -1
		for k, mv := range moves {
			if mv.f < fx && (best < 0 || mv.f < moves[best].f) {
				best = k
			}
		}
		if best < 0 {
			step /= 2
			continue
		}
		x[moves[best].i], fx = moves[best].v, moves[best].f
	}
	return x, fx
}
