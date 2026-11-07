package audio

import (
	"cmp"
	"math"
	"slices"
	"sort"
)

// The accounting holds a rip's notes to the recording: each note's harmonics are
// measured frame by frame, one template a pitch and one activation a note explain
// them together, and a note whose sound the others account for goes, while a
// strike the decoder dropped comes back where the kept notes leave its sound
// unexplained. The constants are the measurement's shape; accScore holds the fit.
const (
	accHarmonics = 16     // harmonics measured a note
	accMaxHz     = 6000.0 // under this frequency
	accBefore    = 6      // frames measured before a note's start, for its rise
	accAfter     = 8      // and after its end
	accSpan      = 130    // frames a note is measured over at most
	accOnset     = 8      // frames from the start its evidence is read over
	accRise      = 4      // frames after the start an activation may still rise
	accSweeps    = 5      // passes over the frames fitting the activations and templates
	accInner     = 4      // activation updates a frame each pass
	accDup       = 2      // frames within which two notes of one pitch are one
)

// accNote is a note as the accounting measures it: its frames, its harmonics'
// magnitudes there, its activation, and the other notes' harmonics under its own
type accNote struct {
	ripNote
	cand   bool // a strike the decoder dropped, judged against the kept notes
	t0, t1 int  // frames measured
	f0     float64
	nh     int
	floor  []float32 // the floor's shape at each harmonic
	mag    []float32 // (t1-t0) × nh, a unit sine reading one
	g      []float32 // t1-t0
	terms  []accTerm // by harmonic, termAt[h] to termAt[h+1]
	termAt []int32
}

// accTerm is another note's harmonic k under one of this note's, at c of its
// magnitude through the window's lobe
type accTerm struct {
	j int32
	k uint8
	c float32
}

func (n *accNote) frames() int { return n.t1 - n.t0 }

// accLobe is a Hann window's response d bins off its centre, one at zero, zero
// past two
func accLobe(d float64) float64 {
	d = math.Abs(d)
	if d >= 2 {
		return 0
	}
	if math.Abs(d-1) < 1e-6 {
		return 0.5
	}
	if d < 1e-6 {
		return 1
	}
	return math.Abs(math.Sin(math.Pi*d) / (math.Pi * d) / (1 - d*d))
}

// accFloorShape is the floor's spectral shape, a pink slope
func accFloorShape(hz float64) float32 { return float32(math.Sqrt(100 / math.Max(hz, 100))) }

// accNotes lays the notes out for the measurement, in time order
func accNotes(notes []ripNote, cand bool, tuning float64, frames int) []*accNote {
	out := make([]*accNote, 0, len(notes))
	for _, n := range notes {
		a := &accNote{ripNote: n, cand: cand, f0: NoteFreq(n.midi) * math.Pow(2, tuning/1200)}
		a.t0 = max(n.start-accBefore, 0)
		a.t1 = min(max(n.end+accAfter, n.start+accOnset+2), n.start+accSpan, frames)
		a.nh = min(accHarmonics, int(accMaxHz/a.f0))
		if a.t1 <= a.t0 || a.nh < 1 {
			continue
		}
		a.floor = make([]float32, a.nh)
		for h := range a.floor {
			a.floor[h] = accFloorShape(float64(h+1) * a.f0)
		}
		out = append(out, a)
	}
	slices.SortFunc(out, byFrame)
	return out
}

// accBin is the Hz a bin of the measuring window spans
const accBin = float64(RipRate) / RipN

// accMeasure reads each note's harmonics off the recording, frame by frame
// under a Hann window of the rip's length, each harmonic's magnitude
// interpolated between the bins either side; a note measured before keeps its
// activation, a new one starts low
func accMeasure(x []float64, notes []*accNote) {
	slices.SortFunc(notes, byFrame)
	for _, n := range notes {
		n.mag = make([]float32, n.frames()*n.nh)
		if len(n.g) != n.frames() {
			n.g = make([]float32, n.frames())
			for u := range n.g {
				n.g[u] = 0.01
			}
		}
	}
	s := newSTFT(RipN)
	scale := 4.0 / RipN
	var active []*accNote
	next := 0
	T := 0
	for _, n := range notes {
		T = max(T, n.t1)
	}
	for t := 0; t < T; t++ {
		active = slices.DeleteFunc(active, func(n *accNote) bool { return n.t1 <= t })
		for next < len(notes) && notes[next].t0 <= t {
			active = append(active, notes[next])
			next++
		}
		if len(active) == 0 {
			continue
		}
		mag := s.at(x, t*RipHop)
		for _, n := range active {
			row := n.mag[(t-n.t0)*n.nh:][:n.nh]
			for h := range n.nh {
				pos := float64(h+1) * n.f0 / accBin
				b := int(pos)
				if b+1 >= len(mag) {
					break
				}
				frac := pos - float64(b)
				row[h] = float32((mag[b]*(1-frac) + mag[b+1]*frac) * scale)
			}
		}
	}
}

// accTerms finds, for each note, the harmonics of every note it overlaps in time
// that fall within its window's lobe of its own harmonics; a candidate sees no
// other candidate, as each is judged against the kept notes alone
func accTerms(notes []*accNote) {
	for i, n := range notes {
		n.terms, n.termAt = n.terms[:0], make([]int32, n.nh+1)
		for h := range n.nh {
			n.termAt[h] = int32(len(n.terms))
			n.terms = append(n.terms, accTerm{j: int32(i), k: uint8(h), c: 1})
			fh := float64(h+1) * n.f0
			for j, o := range notes {
				if o.t0 >= n.t1 {
					break
				}
				if j == i || o.t1 <= n.t0 || n.cand && o.cand {
					continue
				}
				lo, hi := max(int((fh-2*accBin)/o.f0), 1), min(int((fh+2*accBin)/o.f0)+1, o.nh)
				for k := lo; k <= hi; k++ {
					if c := accLobe((float64(k)*o.f0 - fh) / accBin); c > 0.01 {
						n.terms = append(n.terms, accTerm{j: int32(j), k: uint8(k - 1), c: float32(c)})
					}
				}
			}
		}
		n.termAt[n.nh] = int32(len(n.terms))
	}
}

// accFit is the model of the recording the notes make: a template a pitch, the
// share of each harmonic in a note's sound, and a floor a frame under them all
type accFit struct {
	notes []*accNote
	w     [RipPitches][accHarmonics]float32
	b     []float32
	local []int // a note's frame index at the frame being fitted, -1 when not sounding
}

func newAccFit(notes []*accNote, frames int) *accFit {
	f := &accFit{notes: notes, b: make([]float32, frames), local: make([]int, len(notes))}
	for p := range f.w {
		var sum float32
		for h := range f.w[p] {
			f.w[p][h] = float32(math.Pow(float64(h+1), -0.7))
			sum += f.w[p][h]
		}
		for h := range f.w[p] {
			f.w[p][h] /= sum
		}
	}
	for t := range f.b {
		f.b[t] = 1e-4
	}
	for i := range f.local {
		f.local[i] = -1
	}
	return f
}

// predict is harmonic h of note i at the frame the local indices are set for:
// the floor, its own sound and the other sounding notes' harmonics under it
func (f *accFit) predict(i, h, t int) (p, own float32) {
	n := f.notes[i]
	p = f.b[t] * n.floor[h]
	for _, tm := range n.terms[n.termAt[h]:n.termAt[h+1]] {
		u := f.local[tm.j]
		if u < 0 {
			continue
		}
		o := f.notes[tm.j]
		v := o.g[u] * f.w[o.midi-RipMinMIDI][tm.k] * tm.c
		p += v
		if int(tm.j) == i {
			own = v
		}
	}
	return p, own
}

// sweep fits the activations frame by frame, and the floor, by multiplicative
// updates of the Kullback-Leibler fit, a frozen note's activation held; the
// templates are updated after it unless held, from what the frames accumulated
func (f *accFit) sweep(frozen func(*accNote) bool, templates bool) {
	notes := f.notes
	var numW, denW [RipPitches][accHarmonics]float64
	var active []int
	next := 0
	const eps = 1e-9
	num, den := make([]float32, len(notes)), make([]float32, len(notes))
	for t := range f.b {
		active = slices.DeleteFunc(active, func(i int) bool { return notes[i].t1 <= t })
		for next < len(notes) && notes[next].t0 <= t {
			active = append(active, next)
			next++
		}
		if len(active) == 0 {
			continue
		}
		for _, i := range active {
			f.local[i] = t - notes[i].t0
		}
		for it := range accInner {
			last := it == accInner-1
			for _, i := range active {
				num[i], den[i] = 0, 0
			}
			var numB, denB float32
			for _, i := range active {
				n := notes[i]
				row := n.mag[f.local[i]*n.nh:][:n.nh]
				for h := range n.nh {
					p, _ := f.predict(i, h, t)
					r := row[h] / (p + eps)
					numB, denB = numB+r*n.floor[h], denB+n.floor[h]
					for _, tm := range n.terms[n.termAt[h]:n.termAt[h+1]] {
						u := f.local[tm.j]
						if u < 0 {
							continue
						}
						o := notes[tm.j]
						wk := f.w[o.midi-RipMinMIDI][tm.k] * tm.c
						num[tm.j] += r * wk
						den[tm.j] += wk
						if last && templates {
							numW[o.midi-RipMinMIDI][tm.k] += float64(r * o.g[u] * tm.c)
							denW[o.midi-RipMinMIDI][tm.k] += float64(o.g[u] * tm.c)
						}
					}
				}
			}
			for _, j := range active {
				if frozen != nil && frozen(notes[j]) {
					continue
				}
				notes[j].g[f.local[j]] *= (num[j] + eps) / (den[j] + eps)
			}
			f.b[t] *= (numB + eps) / (denB + eps)
		}
		for _, i := range active {
			f.local[i] = -1
		}
	}
	if templates {
		f.share(&numW, &denW)
	}
	// a note's activation may rise through its attack and only fall after
	for _, n := range notes {
		if frozen != nil && frozen(n) {
			continue
		}
		for u := n.start - n.t0 + accRise + 1; u < len(n.g); u++ {
			if u > 0 && n.g[u] > n.g[u-1] {
				n.g[u] = n.g[u-1]
			}
		}
	}
}

// share applies the templates' update, each pitch's shared with its neighbours
// within two semitones at half their weight where its own notes are few, and
// renormalizes each to a unit sum with the activations scaled to match
func (f *accFit) share(numW, denW *[RipPitches][accHarmonics]float64) {
	const eps = 1e-9
	var count [RipPitches]float64
	for _, n := range f.notes {
		count[n.midi-RipMinMIDI]++
	}
	var next [RipPitches][accHarmonics]float32
	for p := range next {
		var sum float64
		for h := range next[p] {
			var acc, wt float64
			for d := -2; d <= 2; d++ {
				q := p + d
				if q < 0 || q >= RipPitches || count[q] == 0 {
					continue
				}
				share := count[q]
				if d != 0 {
					share *= 0.5
				}
				acc += share * float64(f.w[q][h]) * (numW[q][h] + eps) / (denW[q][h] + eps)
				wt += share
			}
			if wt == 0 {
				next[p][h] = f.w[p][h]
			} else {
				next[p][h] = float32(acc / wt)
			}
			sum += float64(next[p][h])
		}
		for h := range next[p] {
			next[p][h] = float32(float64(next[p][h]) / (sum + eps))
		}
		if sum > 0 && count[p] > 0 {
			for _, n := range f.notes {
				if n.midi-RipMinMIDI == p {
					for u := range n.g {
						n.g[u] *= float32(sum)
					}
				}
			}
		}
	}
	f.w = next
}

// accEvidence is what the fit says of one note over its onset: the share of its
// points that are its own sound, how much of its fundamental and of its odd
// harmonics the others leave unexplained, the rise of that unexplained sound over
// the frames before, the fit's loss without the note, and its own level in dB
type accEvidence struct {
	share, unF0, unOdd, rise, loo, level float64
}

// evidences is every note's evidence
func (f *accFit) evidences() []accEvidence {
	out := make([]accEvidence, len(f.notes))
	for i := range f.notes {
		out[i] = f.evidence(i)
	}
	return out
}

func (f *accFit) evidence(i int) accEvidence {
	n := f.notes[i]
	const eps = 1e-9
	// the notes whose harmonics fall under this one's, the only ones predict reads
	var near []int
	for _, tm := range n.terms {
		if !slices.Contains(near, int(tm.j)) {
			near = append(near, int(tm.j))
		}
	}
	var sumM, sumOwn, mF0, unF0, mOdd, unOdd, klFull, klLess, resOn, resPre, peak float64
	for t := n.t0; t < n.t1; t++ {
		u := t - n.t0
		onset := t >= n.start-1 && t < n.start+accOnset
		if !onset && t >= n.start-2 {
			continue
		}
		for _, j := range near {
			if o := f.notes[j]; o.t0 <= t && t < o.t1 {
				f.local[j] = t - o.t0
			}
		}
		row := n.mag[u*n.nh:][:n.nh]
		var res float64
		for h := range n.nh {
			p, own := f.predict(i, h, t)
			m, others := float64(row[h]), float64(p-own)
			un := math.Max(m-others, 0)
			res += un
			if !onset {
				continue
			}
			sumM, sumOwn = sumM+m, sumOwn+float64(own)
			peak = math.Max(peak, float64(own))
			if h == 0 {
				mF0, unF0 = mF0+m, unF0+un
			}
			if h%2 == 0 {
				mOdd, unOdd = mOdd+m, unOdd+un
			}
			klFull += m*math.Log((m+eps)/(float64(p)+eps)) - m + float64(p)
			klLess += m*math.Log((m+eps)/(others+eps)) - m + others
		}
		if onset {
			resOn = math.Max(resOn, res)
		} else {
			resPre = math.Max(resPre, res)
		}
		for _, j := range near {
			f.local[j] = -1
		}
	}
	return accEvidence{
		share: sumOwn / (sumM + eps), unF0: unF0 / (mF0 + eps), unOdd: unOdd / (mOdd + eps),
		rise: min(max(20*math.Log10((resOn+1e-7)/(resPre+1e-7)), -20), 60), loo: (klLess - klFull) / (sumM + eps),
		level: 20 * math.Log10(peak+1e-7),
	}
}

// accScore weighs a note's evidence with what the front end said of it into the
// odds that it is a note of the recording: a logistic fit on graded recordings, a
// piano in a room and through a phone, a keyboard and a distorted guitar
// rendered from the same piece. Reported notes and candidates are weighed apart.
type accScore struct {
	bias, share, unF0, unOdd, rise, loo, strike, support, low, level, ringing float64
}

var (
	accReported  = accScore{-4.87, 1.18, 1.58, 1.18, 0.55, -0.19, 0.15, 3.93, -0.99, 0.17, 0.24}
	accCandidate = accScore{-5.29, 0.52, -1.15, 2.07, 0.39, 0.04, 0.26, 4.42, -0.28, 0.61, 0.76}
	// accBar and accCandBar are the odds a reported note keeps and a candidate
	// comes back at
	accBar, accCandBar = 0.35, 0.5
)

// accInput is a note's evidence beside what the front end heard of it
type accInput struct {
	accEvidence
	strike, support float64 // the strike head's sureness, the pitch head's salience over the register's reference
	low, ringing    bool    // the bass register; another note of its pitch sounding at its start
}

func (s accScore) odds(in accInput, ref float64) float64 {
	logit := func(p float64) float64 { p = min(max(p, 1e-4), 1-1e-4); return math.Log(p / (1 - p)) }
	flag := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}
	v := s.bias + s.share*math.Log(math.Max(in.share, 1e-3)) + s.unF0*in.unF0 + s.unOdd*in.unOdd + s.rise*in.rise/10 +
		s.loo*math.Log(math.Max(in.loo, 1e-3)) + s.strike*logit(in.strike) + s.support*min(in.support, 1.5) + s.low*flag(in.low) +
		s.level*min(max(in.level-ref, -60), 10)/10 + s.ringing*flag(in.ringing)
	return 1 / (1 + math.Exp(-v))
}

// account holds the registers' notes to the recording x: two notes of one pitch
// at one start are one; the notes are fitted together and one whose sound the
// others account for goes; then every strike over the bar the decoder dropped is
// judged against the notes kept, and comes back where its sound is its own.
func (f *RipFrames) account(x []float64, p RipParams, bass, melody []ripNote) ([]ripNote, []ripNote) {
	if f.Strike == nil {
		return bass, melody
	}
	refs := f.accRefs()
	notes := accNotes(accDedup(slices.Concat(bass, melody)), false, f.Tuning, f.frames())
	cands := accNotes(f.accCandidates(p, notes, refs), true, f.Tuning, f.frames())
	all := slices.Concat(notes, cands)
	accMeasure(x, all)
	accTerms(notes)
	fit := newAccFit(notes, f.frames())
	for range accSweeps {
		fit.sweep(nil, true)
	}
	ev := fit.evidences()
	ref := accRef(ev)
	var kept []*accNote
	for i, n := range notes {
		if accReported.odds(f.accInput(n, ev[i], notes, refs), ref) >= accBar {
			kept = append(kept, n)
		}
	}
	if len(cands) > 0 {
		all = slices.Concat(kept, cands)
		slices.SortFunc(all, byFrame)
		accTerms(all)
		fit2 := &accFit{notes: all, w: fit.w, b: fit.b, local: make([]int, len(all))}
		for i := range fit2.local {
			fit2.local[i] = -1
		}
		frozen := func(n *accNote) bool { return !n.cand }
		for range accSweeps - 2 {
			fit2.sweep(frozen, false)
		}
		ev2 := fit2.evidences()
		for i, n := range all {
			if n.cand && accCandidate.odds(f.accInput(n, ev2[i], all, refs), ref) >= accCandBar {
				kept = append(kept, n)
			}
		}
	}
	bass, melody = bass[:0], melody[:0]
	for _, n := range kept {
		if n.midi < RipBassTop {
			bass = append(bass, n.ripNote)
		} else {
			melody = append(melody, n.ripNote)
		}
	}
	byStart := func(a, b ripNote) int { return cmp.Or(a.start-b.start, a.midi-b.midi) }
	slices.SortFunc(bass, byStart)
	slices.SortFunc(melody, byStart)
	return bass, melody
}

// byFrame orders notes by their first measured frame, as the fits sweep them
func byFrame(a, b *accNote) int { return cmp.Or(a.t0-b.t0, a.midi-b.midi) }

// accRefs is each register's reference loudness, the bass's then the melody's
func (f *RipFrames) accRefs() [2]float64 {
	var refs [2]float64
	if r, ok := f.register(RipMinMIDI, RipBassTop); ok {
		refs[0] = r.ref
	}
	if r, ok := f.register(RipBassTop, RipMaxMIDI); ok {
		refs[1] = r.ref
	}
	return refs
}

// accDedup merges two notes of one pitch starting within accDup frames of each
// other while the first sounds, as the strike and the bass line's own path can
// both report one note, keeping the earlier start and the later end
func accDedup(notes []ripNote) []ripNote {
	slices.SortFunc(notes, func(a, b ripNote) int { return cmp.Or(a.midi-b.midi, a.start-b.start) })
	out := notes[:0]
	for _, n := range notes {
		if k := len(out) - 1; k >= 0 && out[k].midi == n.midi && n.start-out[k].start <= accDup && out[k].end > n.start {
			out[k].end = max(out[k].end, n.end)
			out[k].vel = max(out[k].vel, n.vel)
			continue
		}
		out = append(out, n)
	}
	return out
}

// accRef is the level the notes' own levels are read against, the ninetieth
// percentile of the reported notes'
func accRef(ev []accEvidence) float64 {
	levels := make([]float64, 0, len(ev))
	for _, e := range ev {
		levels = append(levels, e.level)
	}
	if len(levels) == 0 {
		return 0
	}
	sort.Float64s(levels)
	return levels[len(levels)*9/10]
}

// accInput reads what the heads said of a note beside its evidence
func (f *RipFrames) accInput(n *accNote, ev accEvidence, notes []*accNote, refs [2]float64) accInput {
	in := accInput{accEvidence: ev, low: n.midi < RipBassTop}
	at := func(h []float32, t, midi int) float64 {
		if h == nil || t < 0 || t >= f.frames() {
			return 0
		}
		return float64(h[t*RipPitches+midi-RipMinMIDI])
	}
	for t := n.start - 2; t <= n.start+2; t++ {
		in.strike = math.Max(in.strike, at(f.Strike, t, n.midi))
	}
	ref := refs[1]
	if in.low {
		ref = refs[0]
	}
	if ref > 0 {
		for t := n.start; t < n.start+6; t++ {
			in.support = math.Max(in.support, at(f.Salience, t, n.midi)/ref)
		}
	}
	for _, o := range notes {
		if o != n && o.midi == n.midi && o.start < n.start-accDup && o.end > n.start-accDup {
			in.ringing = true
		}
	}
	return in
}

// accCandidates is every strike over the bar that starts no note, read as the
// decoder reads one, its pitch sounding over Voicing of its register's reference
// until another strike, outside StrikeGap of a reported note of its pitch
func (f *RipFrames) accCandidates(p RipParams, reported []*accNote, refs [2]float64) []ripNote {
	T, gap := f.frames(), int(p.StrikeGap/f.Hop)
	at := func(h []float32, t, midi int) float32 {
		if t < 0 || t >= T {
			return 0
		}
		return h[t*RipPitches+midi-RipMinMIDI]
	}
	var out []ripNote
	for reg, lo, hi := 0, RipMinMIDI, RipBassTop; lo < RipMaxMIDI; reg, lo, hi = reg+1, hi, RipMaxMIDI {
		r := register{f: f, lo: lo, states: hi - lo, reach: hi - lo, ref: refs[reg]}
		if r.ref <= 0 {
			continue
		}
		for s := range r.states {
			midi := lo + s
			var starts []int
			for _, k := range reported {
				if k.midi == midi {
					starts = append(starts, k.start)
				}
			}
			last := -gap
			for t := 0; t < T; t++ {
				v := at(f.Strike, t, midi)
				if float64(v) < p.Strike || v < at(f.Strike, t-1, midi) || v <= at(f.Strike, t+1, midi) || t-last < gap {
					continue
				}
				last = t
				if slices.ContainsFunc(starts, func(a int) bool { return a-gap < t && t < a+gap }) {
					continue
				}
				end := t + 1
				for end < T && end-t < accSpan && r.sal(end, s) >= p.Voicing*r.ref && !(float64(at(f.Strike, end, midi)) >= p.Strike && at(f.Strike, end, midi) >= at(f.Strike, end-1, midi) && at(f.Strike, end, midi) > at(f.Strike, end+1, midi)) {
					end++
				}
				out = append(out, r.note(t, min(max(end, t+5), T), s))
			}
		}
	}
	return out
}
