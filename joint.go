package audio

import (
	"math"
	"slices"
)

// ripJointMeters are the meters the joint path reads, each one stapes' corpus
// states; a pinned meter is read alone
var ripJointMeters = [...]Meter{{}, {3, 4}, {2, 4}, {5, 4}, {6, 8}, {12, 8}, {7, 8}}

// The joint path's tempos are ripJointTempos quarters a constant ratio apart
// from MinBPM to MaxBPM, a beat lasting any whole frames within half that ratio
// of its tempo's; a head is read no nearer 0 or 1 than ripJointFloor, and a
// tempo given holds the path within ripJointGiven of it
const (
	ripJointTempos = 55
	ripJointFloor  = 0.01
	ripJointGiven  = 1.06
	ripJointNew    = 255 // a state's way back where its path starts on it
)

// jointCell is a tempo of the joint path in a meter: the lengths in frames,
// shortest first, a beat at it may take
type jointCell struct {
	steps [3]int
	n     int
	beat  float64 // its beat, in frames
}

// jointWords are what each frame says to a beat of the joint path on it, and to a
// bar line: each head's log-odds, the beat head's at its highest within a frame
// and the downbeat head's within ripHeadReach as lineHead reads it, over those
// of JointBeat and JointLine; nothing on the frames the heads have not heard
func (f *RipFrames) jointWords(p RipParams) (beat, line []float64) {
	logit := func(v float64) float64 {
		v = min(max(v, ripJointFloor), 1-ripJointFloor)
		return math.Log(v / (1 - v))
	}
	n := f.frames()
	beat, line = make([]float64, n), make([]float64, n)
	for t := range n {
		d, ok := f.lineHead(t)
		if !ok {
			continue
		}
		beat[t] = logit(peak(f.Beat, float64(t))) - logit(p.JointBeat)
		line[t] = logit(d) - logit(p.JointLine)
	}
	return beat, line
}

// jointRun is the joint path in meter m over the frames' words, a state a beat's
// place in the bar and its tempo cell, a beat on any frame scoring its beat word
// and a line its line word too, and a beat a cell from the one before cost a
// cell. A path starts on any frame, or where from is not negative only on a
// line there in cell k0, each cell from it cost a cell, and ends on any. With
// back, each state's way back is written there, a byte a frame: the index of
// its beat's length and the cell before, or ripJointNew.
func jointRun(beat, line []float64, m Meter, cells []jointCell, cost float64, from, k0 int, back []uint8) (score float64, t, j, k int) {
	n, per, nk := len(beat), m.Beats(), len(cells)
	width, reach := per*nk, 1
	for _, c := range cells {
		reach = max(reach, c.steps[c.n-1]+1)
	}
	best := make([]float64, reach*width) // the last reach frames' scores, a ring
	over := make([]float64, reach*width) // what a beat in each cell takes from each frame: its best across the cells
	took := make([]uint8, reach*width)   // and the cell that is
	for i := range over {
		over[i] = math.Inf(-1)
	}
	score = math.Inf(-1)
	for at := max(from, 0); at < n; at++ {
		row := at % reach * width
		for jj := range per {
			o, jp := beat[at], (jj+per-1)%per
			if jj == 0 {
				o += line[at]
			}
			for kk := range cells {
				v, arg := math.Inf(-1), uint8(ripJointNew)
				switch {
				case from < 0:
					v = 0
				case at == from && jj == 0:
					v = -cost * math.Abs(float64(kk-k0))
				}
				c := &cells[kk]
				for i, d := range c.steps[:c.n] {
					if d > at || at-d < from {
						break
					}
					if prev := (at-d)%reach*width + jp*nk + kk; over[prev] > v {
						v, arg = over[prev], uint8(i<<6)|took[prev]
					}
				}
				s := row + jj*nk + kk
				best[s] = o + v
				if back != nil {
					back[at*width+jj*nk+kk] = arg
				}
				if best[s] > score {
					score, t, j, k = best[s], at, jj, kk
				}
			}
			// what each cell takes from this frame, a cell away costing cost: two sweeps
			a := row + jj*nk
			b, ov, tk := best[a:a+nk], over[a:a+nk], took[a:a+nk]
			for kk := range nk {
				ov[kk], tk[kk] = b[kk], uint8(kk)
				if kk > 0 && ov[kk-1]-cost > ov[kk] {
					ov[kk], tk[kk] = ov[kk-1]-cost, tk[kk-1]
				}
			}
			for kk := nk - 2; kk >= 0; kk-- {
				if ov[kk+1]-cost > ov[kk] {
					ov[kk], tk[kk] = ov[kk+1]-cost, tk[kk+1]
				}
			}
		}
	}
	return score, t, j, k
}

// jointCells are meter m's tempo cells, those within ripJointGiven of a quarter
// given when it is not zero, and the one whose bar lasts nearest bar frames
func (f *RipFrames) jointCells(m Meter, given, bar float64) (cells []jointCell, near int) {
	ratio := math.Pow(float64(MaxBPM)/MinBPM, 1/float64(ripJointTempos-1))
	gap := math.Inf(1)
	for k := range ripJointTempos {
		q := MinBPM * math.Pow(ratio, float64(k))
		if given > 0 && math.Abs(math.Log(q/given)) > math.Log(ripJointGiven) {
			continue
		}
		c := jointCell{beat: 60 / (q * f.Hop) * float64(m.Beat()) / StepsPerBeat}
		for _, x := range [...]float64{1 / math.Sqrt(ratio), 1, math.Sqrt(ratio)} {
			if d := max(int(math.Round(c.beat*x)), 1); c.n == 0 || d != c.steps[c.n-1] {
				c.steps[c.n], c.n = d, c.n+1
			}
		}
		if d := math.Abs(math.Log(c.beat * float64(m.Beats()) / bar)); d < gap {
			near, gap = len(cells), d
		}
		cells = append(cells, c)
	}
	return cells, near
}

// jointGrid decodes the bars as the joint path the heads give (jointRun) in the
// meter of meters whose path scores highest: its lines, the last bar's end where
// it falls within half a beat past the frames, at its quarters' median tempo
// over the bars. From a line at frame from when it is not negative, in a bar of
// meters' one alone, its tempo starting at that of a bar of bar frames. A path
// with no whole bar gives none.
func (f *RipFrames) jointGrid(p RipParams, meters []Meter, given float64, from int, bar float64) (ripGrid, float64, ripTrack) {
	beat, line := f.jointWords(p)
	cost := p.JointMove * math.Log(float64(MaxBPM)/MinBPM) / (ripJointTempos - 1)
	m, top := Meter{}, math.Inf(-1)
	var cells []jointCell
	var k0 int
	for _, mm := range meters {
		c, near := f.jointCells(mm, given, bar)
		if len(meters) == 1 {
			m, cells, k0 = mm, c, near
			break
		}
		s, _, _, _ := jointRun(beat, line, mm, c, cost, from, near, nil)
		if mm != (Meter{}) {
			s -= p.JointMargin
		}
		if s > top {
			m, top, cells, k0 = mm, s, c, near
		}
	}
	if len(cells) == 0 {
		return ripGrid{m: m}, 0, ripTrack{}
	}
	per, nk := m.Beats(), len(cells)
	back := make([]uint8, f.frames()*per*nk)
	s, t, j, k := jointRun(beat, line, m, cells, cost, from, k0, back)
	if math.IsInf(s, -1) {
		return ripGrid{m: m}, 0, ripTrack{}
	}
	g, place := ripGrid{m: m}, j // the last beat's place in its bar
	var beats []int
	for {
		if beats = append(beats, f.centre(t)); j == 0 {
			g.lines = append(g.lines, beats[len(beats)-1])
		}
		b := back[t*per*nk+j*nk+k]
		if b == ripJointNew {
			break
		}
		t, j, k = t-cells[k].steps[b>>6], (j+per-1)%per, int(b&63)
	}
	slices.Reverse(g.lines)
	slices.Reverse(beats)
	// the last bar's end, on at the mean beat of the path's last bar where it falls
	// within half a beat past the frames: a render of whole bars keeps its last
	last, q := cells[k].beat, min(per, len(beats)-1)
	if q > 0 {
		last = float64(beats[len(beats)-1]-beats[len(beats)-1-q]) / float64(q)
	}
	if end := float64(beats[len(beats)-1]) + float64(per-place)*last; place != 0 && end <= float64(f.frames())+last/2 {
		g.lines = append(g.lines, int(math.Round(end)))
	}
	if g.bars() < 1 {
		return ripGrid{m: m}, 0, ripTrack{}
	}
	tempos := make([]float64, g.bars())
	for i := range tempos {
		tempos[i] = 60 * float64(m.Steps()) / StepsPerBeat / (float64(g.lines[i+1]-g.lines[i]) * f.Hop)
	}
	slices.Sort(tempos)
	tempo := min(max((tempos[(len(tempos)-1)/2]+tempos[len(tempos)/2])/2, MinBPM), MaxBPM)
	rd := ripReading{m: m, pulses: beats, unit: m.Beat(), stride: 1, tempo: tempo}
	return g, tempo, ripTrack{bpm: tempo, period: last, k: 1, pulses: beats, rd: rd, heard: true, path: PathJoint}
}

// centre is the frame within two of t the beat head peaks on, the middle of the
// frames it holds its peak on: a mark written a frame either side of a beat
// reads alike on each, and the beat is its middle
func (f *RipFrames) centre(t int) int {
	a, z := max(t-2, 0), min(t+2, len(f.Beat)-1)
	if a > z {
		return t
	}
	top := slices.Max(f.Beat[a : z+1])
	var at []int
	for u := a; u <= z; u++ {
		if f.Beat[u] == top {
			at = append(at, u)
		}
	}
	return at[len(at)/2]
}
