package audio

import (
	"math"
	"slices"
)

// RipSection is a stretch of a recording whose tempo, meter and bar line were
// decided on its own frames: its first bar of Rip.Bars, the second its first line falls
// on, and its tempo and meter
type RipSection struct {
	Bar   int
	Start float64
	BPM   int
	Meter Meter
}

// ripSectionWindow is the seconds either side of a frame over which the beat
// head's periodicity is compared, and ripSectionGain how much more the heads
// must agree with two stretches' grids than with one over both for the frame to
// part them: each fitted on the fit side of recordings of two sections (audio.md §9)
const (
	ripSectionWindow = 3.5
	ripSectionGain   = 0.292
	ripSectionStep   = 4 // frames between the frames compared
)

// ripSpan is a section as its grid was decoded: from the frame to the frame, its
// grid on the recording's frames and its tempo
type ripSpan struct {
	from, to int
	g        ripGrid
	bpm      float64
}

// spans reads the frames a to b in sections: one grid over them, unless the
// frame where the beat head's periodicity changes most (change) parts two
// stretches whose grids, each decoded on its own frames, the heads agree with by
// ripSectionGain more than with the one grid (gain); each stretch is then read so
// again. o is the beat head's strength, nil where no head is heard.
func (rp Ripper) spans(f *RipFrames, ev ripEvents, p RipParams, o []float64, a, b int) ([]ripSpan, error) {
	one, err := rp.section(f, ev, p, a, b)
	w := int(math.Round(ripSectionWindow / f.Hop))
	if err != nil || o == nil || b-a < 2*w {
		return []ripSpan{one}, err
	}
	at, most := -1, 0.0
	for c := a + w; c+w <= b; c += ripSectionStep {
		if d := change(o, c, w, f.Hop); d > most {
			at, most = c, d
		}
	}
	if at < 0 {
		return []ripSpan{one}, nil
	}
	left, lerr := rp.section(f, ev, p, a, at)
	right, rerr := rp.section(f, ev, p, at, b)
	if lerr != nil || rerr != nil || f.gain(one, left, right) <= ripSectionGain {
		return []ripSpan{one}, nil
	}
	before, err := rp.spans(f, ev, p, o, a, at)
	if err != nil {
		return nil, err
	}
	after, err := rp.spans(f, ev, p, o, at, b)
	if err != nil {
		return nil, err
	}
	return append(before, after...), nil
}

// section is the grid of the frames a to b decoded on them alone, their events and
// their own tonality, its lines on the recording's frames; the whole
// recording's is the grid a rip of one section takes
func (rp Ripper) section(f *RipFrames, ev ripEvents, p RipParams, a, b int) (ripSpan, error) {
	if a == 0 && b == f.frames() {
		g, bpm, _, err := rp.grid(f, ev, p, true)
		return ripSpan{a, b, g, bpm}, err
	}
	fp := *f
	fp.cut(a, b)
	evp := ev.within(a, b)
	evp.root, evp.scale = fp.tonality()
	g, bpm, _, err := rp.grid(&fp, evp, p, true)
	if err != nil {
		return ripSpan{}, err
	}
	for i := range g.lines {
		g.lines[i] += a
	}
	return ripSpan{a, b, g, bpm}, nil
}

// gain is how much more the heads agree with the grids of two stretches than
// with one over both (agreement), the beats' and the lines' gains summed
func (f *RipFrames) gain(one, left, right ripSpan) float64 {
	wb, wl := f.agreement(one.g, one.from, one.to)
	lb, ll := f.agreement(left.g, left.from, left.to)
	rb, rl := f.agreement(right.g, right.from, right.to)
	return lb.plus(rb).mean() - wb.mean() + ll.plus(rl).mean() - wl.mean()
}

// ripMarks is a head's sum over the frames it is read at and their count
type ripMarks struct {
	sum float64
	n   int
}

func (m ripMarks) plus(o ripMarks) ripMarks { return ripMarks{m.sum + o.sum, m.n + o.n} }

func (m ripMarks) mean() float64 { return m.sum / float64(max(m.n, 1)) }

// agreement is the beat head at g's beats, at its highest within a frame, and
// the downbeat head at its lines (lineHead), over the frames lo to hi
func (f *RipFrames) agreement(g ripGrid, lo, hi int) (beats, lines ripMarks) {
	for i := 0; i <= g.m.Beats()*g.bars(); i++ {
		if b := g.beat(i); b >= lo && b < hi {
			beats = beats.plus(ripMarks{peak(f.Beat, float64(b)), 1})
		}
	}
	for _, l := range g.lines {
		if l >= lo && l < hi {
			v, _ := f.lineHead(l)
			lines = lines.plus(ripMarks{v, 1})
		}
	}
	return beats, lines
}

// change is how unlike the beat head's periodicity is either side of frame c:
// one less the correlation of o's autocorrelations over the w frames before c
// and the w after, at the lags of a beat from 480 to 60 a minute
func change(o []float64, c, w int, hop float64) float64 {
	lo, hi := int(60/(480*hop)), int(60/(60*hop))
	return 1 - pearson(autocorr(o[c-w:c], lo, hi), autocorr(o[c:c+w], lo, hi))
}

// autocorr is x's autocorrelation about its mean at lags lo to hi, each over
// the pairs it spans, short of the last lag x holds
func autocorr(x []float64, lo, hi int) []float64 {
	var mean float64
	for _, v := range x {
		mean += v
	}
	mean /= float64(max(len(x), 1))
	var out []float64
	for l := lo; l <= hi && l < len(x)-1; l++ {
		var s float64
		for t := l; t < len(x); t++ {
			s += (x[t] - mean) * (x[t-l] - mean)
		}
		out = append(out, s/float64(len(x)-l))
	}
	return out
}

// pearson is the correlation of a and b over their common length, 0 where
// either is flat
func pearson(a, b []float64) float64 {
	n := min(len(a), len(b))
	var ma, mb float64
	for i := range n {
		ma, mb = ma+a[i], mb+b[i]
	}
	ma, mb = ma/float64(max(n, 1)), mb/float64(max(n, 1))
	var ab, aa, bb float64
	for i := range n {
		ab += (a[i] - ma) * (b[i] - mb)
		aa += (a[i] - ma) * (a[i] - ma)
		bb += (b[i] - mb) * (b[i] - mb)
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return ab / math.Sqrt(aa*bb)
}

// within is the events of frames a to b, moved to start at a
func (ev ripEvents) within(a, b int) ripEvents {
	out := ev.between(a, b)
	for d := range out.hits {
		for i := range out.hits[d] {
			out.hits[d][i].frame -= a
		}
	}
	for _, ns := range [...][]ripNote{out.bass, out.melody} {
		for i := range ns {
			ns[i].start, ns[i].end, ns[i].cells = ns[i].start-a, ns[i].end-a, nil
		}
	}
	return out
}

// between is the events whose hits lie and notes start on frames a to b, copies
func (ev ripEvents) between(a, b int) ripEvents {
	out := ripEvents{root: ev.root, scale: ev.scale, trace: ev.trace}
	for d, hs := range ev.hits {
		for _, h := range hs {
			if h.frame >= a && h.frame < b {
				out.hits[d] = append(out.hits[d], h)
			}
		}
	}
	outside := func(n ripNote) bool { return n.start < a || n.start >= b }
	out.bass, out.melody = slices.DeleteFunc(slices.Clone(ev.bass), outside), slices.DeleteFunc(slices.Clone(ev.melody), outside)
	return out
}

// laySections lays a rip of several sections: each one's bars end on the next
// one's first line, the key is named over every bar, each one's events are laid
// on its grid, a hit's velocity against its drum's loudest over the recording,
// and the transcript is the whole recording's
func (f *RipFrames) laySections(x []float64, ev ripEvents, spans []ripSpan, p RipParams) *Rip {
	for k := 0; k+1 < len(spans); k++ {
		next, g := &spans[k+1].g, &spans[k].g
		for next.bars() > 1 && next.lines[0] <= g.lines[0] {
			next.lines = next.lines[1:]
		}
		g.lines = append(slices.DeleteFunc(g.lines, func(l int) bool { return l >= next.lines[0] }), next.lines[0])
	}
	var bars [][12]float64
	for _, s := range spans {
		bars = append(bars, f.barChroma(s.g)...)
	}
	c, b := f.chroma()
	ev.root, ev.scale = keyOver(c, b, bars, p)
	var loud [InstrClap]float64
	for d, hs := range ev.hits {
		for _, h := range hs {
			loud[d] = max(loud[d], h.strength)
		}
	}
	r := &Rip{Tuning: f.Tuning, Range: f.Pitches(), Root: ev.root, Scale: ev.scale, Params: p}
	for k, s := range spans {
		from, to := math.MinInt, math.MaxInt
		if k > 0 {
			from = s.g.lines[0]
		}
		if k+1 < len(spans) {
			to = spans[k+1].g.lines[0]
		}
		evk := ev.between(from, to)
		rk := s.g.laid(f, evk, s.bpm, s.g.shift(slices.Concat(evk.melody, evk.bass)), loud, p)
		if k == 0 {
			r.BPM, r.Meter, r.Downbeat = rk.BPM, rk.Meter, rk.Downbeat
		}
		r.Sections = append(r.Sections, RipSection{Bar: len(r.Bars), Start: rk.Downbeat, BPM: rk.BPM, Meter: rk.Meter})
		r.Bars, r.Progression = append(r.Bars, rk.Bars...), append(r.Progression, rk.Progression...)
		beats := rk.Beats
		if k > 0 { // the line the section before ends on
			beats = beats[1:]
		}
		r.Beats = append(r.Beats, beats...)
	}
	r.Notes = f.transcript(x, ev)
	return r
}

// sectionOf is the section bar b lies in and where its beats begin in Beats:
// the rip's own tempo and meter for a rip of one section
func (r *Rip) sectionOf(b int) (RipSection, int) {
	if len(r.Sections) == 0 {
		return RipSection{Start: r.Downbeat, BPM: r.BPM, Meter: r.Meter}, b * r.Meter.Beats()
	}
	at := 0
	for k, pt := range r.Sections {
		next := len(r.Bars)
		if k+1 < len(r.Sections) {
			next = r.Sections[k+1].Bar
		}
		if b < next || k+1 == len(r.Sections) {
			return pt, at + (b-pt.Bar)*pt.Meter.Beats()
		}
		at += (next - pt.Bar) * pt.Meter.Beats()
	}
	return RipSection{}, 0
}

// Lines is the second each bar's line falls on, and last the last bar's end:
// a beat every Meter.Beats() from the first, each section's in its own meter
func (r *Rip) Lines() []float64 {
	var out []float64
	if len(r.Sections) == 0 {
		for i := 0; i < len(r.Beats); i += max(r.Meter.Beats(), 1) {
			out = append(out, r.Beats[i])
		}
		return out
	}
	for b := range r.Bars {
		if _, i := r.sectionOf(b); i < len(r.Beats) {
			out = append(out, r.Beats[i])
		}
	}
	return append(out, r.Beats[len(r.Beats)-1])
}
