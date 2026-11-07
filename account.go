package audio

import (
	"cmp"
	"math"
	"math/cmplx"
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
	accSpan      = 1720   // frames a note is measured over at most, twenty seconds
	accOnset     = 8      // frames from the start its evidence is read over
	accRise      = 4      // frames after the start an activation may still rise freely
	accSwell     = 1.0116 // and after that by a tenth of a decibel a frame, a held note's swell, never a strike's
	accSweeps    = 5      // passes over the frames fitting the activations and templates
	accInner     = 4      // activation updates a frame each pass
	accDup       = 2      // frames within which two notes of one pitch are one
	accCovered   = 0.7    // the share of a note's harmonics the fit cannot see past which it has no say
	accHeld      = 86     // frames a note the fit cannot see lasts to stay on the heads' word, a second
	accPartials  = 6      // partials the memory follows through a note's start
	accMemGap    = 4      // frames either side of the start over which the window smears the attack
	accMemSpan   = 8      // frames the memory reads before that gap and after it
	accSame      = 0.15   // bins within which two notes' partials are one sound
	accStepCap   = 3.0    // the step a partial's new sound is read up to, in the sound before
)

// accNote is a note as the accounting measures it: its frames, its harmonics'
// magnitudes there, its activation, the other notes' harmonics under its own,
// and the memory of the sound at its first partials through its start
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
	mem    []complex64 // 2·accMemSpan frames × its partials: the sound at each, other pitches' partials taken out
	memCov []uint8     // per partial, the frames after the start another pitch's partial sits on it
	step   float64     // the share of the sound at its free partials the memory does not predict
}

// accTerm is another note's harmonic k under one of this note's, at c of its
// magnitude through the window's lobe
type accTerm struct {
	j int32
	k uint8
	c float32
}

func (n *accNote) frames() int { return n.t1 - n.t0 }

// first is the first frame the measurement needs, where the memory starts
func (n *accNote) first() int { return min(n.t0, n.start-accMemGap-accMemSpan) }

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
		a.t1 = min(max(n.end+accAfter, n.start+accMemGap+accMemSpan+1), n.start+accSpan, frames)
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
// interpolated between the bins either side, and its memory through its start;
// a note measured before keeps its activation, a new one starts low
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
		n.mem, n.memCov = make([]complex64, 2*accMemSpan*n.partials()), make([]uint8, n.partials())
	}
	s := newSTFT(RipN)
	scale := 4.0 / RipN
	var m accMemory
	var active []*accNote
	next := 0
	T := 0
	for _, n := range notes {
		T = max(T, n.t1)
	}
	for t := 0; t < T; t++ {
		active = slices.DeleteFunc(active, func(n *accNote) bool { return n.t1 <= t })
		for next < len(notes) && notes[next].first() <= t {
			active = append(active, notes[next])
			next++
		}
		if len(active) == 0 {
			continue
		}
		mag := s.at(x, t*RipHop)
		for _, n := range active {
			if k := n.memFrame(t); k >= 0 {
				m.remember(n, k, t, s, scale, active)
			}
			if t < n.t0 {
				continue
			}
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
	for _, n := range notes {
		n.step = n.memStep()
	}
}

// partials is how many of a note's harmonics its memory follows
func (n *accNote) partials() int { return min(accPartials, n.nh) }

// memFrame is frame t's index among the note's memory frames, the accMemSpan
// before the gap around its start then the accMemSpan after, or -1
func (n *accNote) memFrame(t int) int {
	switch u := t - n.start; {
	case u >= -accMemGap-accMemSpan && u < -accMemGap:
		return u + accMemGap + accMemSpan
	case u > accMemGap && u <= accMemGap+accMemSpan:
		return accMemSpan + u - accMemGap - 1
	}
	return -1
}

// accMemory holds the small least-squares fit the memory reads a partial with:
// the sinusoids within the window's lobe of it, its own and the other reported
// pitches' partials, fitted together to the bins they span
type accMemory struct {
	fs []float64 // the sinusoids' frequencies, in bins, the note's own first
	a  [][]float64
	g  []float64 // each sinusoid's lobe at the bin being read
	re []float64
	im []float64
}

// remember reads note n's partials at its memory frame k, frame t of the
// spectrum s: each partial's complex amplitude with the other reported pitches'
// partials within the lobe fitted beside it; one within accSame bins is the same
// sound and marks the partial covered instead
func (m *accMemory) remember(n *accNote, k, t int, s *stft, scale float64, active []*accNote) {
	P := n.partials()
	for h := range P {
		f := float64(h+1) * n.f0 / accBin
		m.fs = append(m.fs[:0], f)
		covered := false
		for _, o := range active {
			if o == n || o.cand || o.midi == n.midi || t < o.start-1 {
				continue
			}
			k0 := int(math.Round(f * accBin / o.f0))
			for q := max(k0-1, 1); q <= min(k0+1, o.nh); q++ {
				switch d := math.Abs(float64(q)*o.f0/accBin - f); {
				case d < accSame:
					covered = true
				case d < 2:
					m.fs = append(m.fs, float64(q)*o.f0/accBin)
				}
			}
		}
		if covered && k >= accMemSpan {
			n.memCov[h]++
		}
		n.mem[k*P+h] = complex64(m.solve(s, scale))
	}
}

// solve fits the sinusoids at m.fs to the bins within the lobe of any of them,
// by the normal equations, and returns the first's complex amplitude
func (m *accMemory) solve(s *stft, scale float64) complex128 {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, f := range m.fs {
		lo, hi = math.Min(lo, f), math.Max(hi, f)
	}
	b0, b1 := max(int(math.Floor(lo-2))+1, 1), min(int(math.Ceil(hi+2))-1, len(s.mag)-2)
	u := len(m.fs)
	for len(m.a) < u {
		m.a = append(m.a, make([]float64, 0))
	}
	m.re, m.im, m.g = m.re[:0], m.im[:0], m.g[:0]
	for i := range u {
		m.a[i] = append(m.a[i][:0], make([]float64, u)...)
		m.re, m.im, m.g = append(m.re, 0), append(m.im, 0), append(m.g, 0)
	}
	for b := b0; b <= b1; b++ {
		xr, xi := s.re[b]*scale, s.im[b]*scale
		if b%2 == 1 { // the window's centre as the phase's reference
			xr, xi = -xr, -xi
		}
		for i := range u {
			m.g[i] = accLobe(float64(b) - m.fs[i])
		}
		for i := range u {
			if m.g[i] == 0 {
				continue
			}
			m.re[i] += m.g[i] * xr
			m.im[i] += m.g[i] * xi
			for j := range u {
				m.a[i][j] += m.g[i] * m.g[j]
			}
		}
	}
	for i := range u { // a ridge keeps two partials nearly one apart
		m.a[i][i] += 1e-6
	}
	// Gaussian elimination with the two right-hand sides, the real and the imaginary
	for c := range u {
		p := c
		for r := c + 1; r < u; r++ {
			if math.Abs(m.a[r][c]) > math.Abs(m.a[p][c]) {
				p = r
			}
		}
		m.a[c], m.a[p] = m.a[p], m.a[c]
		m.re[c], m.re[p] = m.re[p], m.re[c]
		m.im[c], m.im[p] = m.im[p], m.im[c]
		if math.Abs(m.a[c][c]) < 1e-12 {
			return 0
		}
		for r := c + 1; r < u; r++ {
			f := m.a[r][c] / m.a[c][c]
			for j := c; j < u; j++ {
				m.a[r][j] -= f * m.a[c][j]
			}
			m.re[r] -= f * m.re[c]
			m.im[r] -= f * m.im[c]
		}
	}
	for c := u - 1; c >= 0; c-- {
		for j := c + 1; j < u; j++ {
			m.re[c] -= m.a[c][j] * m.re[j]
			m.im[c] -= m.a[c][j] * m.im[j]
		}
		m.re[c] /= m.a[c][c]
		m.im[c] /= m.a[c][c]
	}
	return complex(m.re[0], m.im[0])
}

// accLine fits a partial's sound over the frames from t0 as a line in its
// log-amplitude, the slope held within lo and hi a frame, and a line in its
// phase about the nominal advance a frame, each frame weighted by its
// magnitude, and returns the sound the lines predict at frame 0
func accLine(z []complex64, t0 int, adv, lo, hi float64) complex128 {
	var top float64
	for _, v := range z {
		top = math.Max(top, cmplx.Abs(complex128(v)))
	}
	if top <= 0 {
		return 0
	}
	var sw, st, stt, sy, sty, sp, stp, prev float64
	for i, v := range z {
		zi := complex128(v)
		mag := cmplx.Abs(zi)
		w, t := mag/top, float64(t0+i)
		y := math.Log(mag + 1e-12)
		p := cmplx.Phase(zi) - adv*t
		if i > 0 {
			d := p - prev
			p = prev + math.Atan2(math.Sin(d), math.Cos(d))
		}
		prev = p
		sw, st, stt = sw+w, st+w*t, stt+w*t*t
		sy, sty, sp, stp = sy+w*y, sty+w*t*y, sp+w*p, stp+w*t*p
	}
	det := sw*stt - st*st
	if det <= 1e-9 {
		return 0
	}
	slope := min(max((sw*sty-st*sy)/det, lo), hi)
	d := (sw*stp - st*sp) / det
	return cmplx.Rect(math.Exp((sy-slope*st)/sw), (sp-d*st)/sw)
}

// memStep is what the memory of the sound before a note's start does not
// predict of the sound after it, at each of its partials no other pitch's
// partial sits on: the step between the lines fitted either side of the
// attack, in the sound before, weighed by that sound; a ring that sounds on
// leaves no step, a strike one as large as its new sound, whatever its phase
func (n *accNote) memStep() float64 {
	P := n.partials()
	var num, den float64
	for h := range P {
		if n.memCov[h] >= accMemSpan/2 {
			continue
		}
		adv := 2 * math.Pi * float64(h+1) * n.f0 * RipHop / RipRate
		before := make([]complex64, accMemSpan)
		after := make([]complex64, accMemSpan)
		for k := range accMemSpan {
			before[k], after[k] = n.mem[k*P+h], n.mem[(accMemSpan+k)*P+h]
		}
		zb := accLine(before, -accMemGap-accMemSpan, adv, -0.5, 0.05)
		za := accLine(after, accMemGap+1, adv, -0.5, 0.5)
		ring := cmplx.Abs(zb)
		if ring <= 0 {
			continue
		}
		num += ring * math.Min(cmplx.Abs(za-zb)/ring, accStepCap)
		den += ring
	}
	if den <= 0 {
		return accStepCap
	}
	return num / den
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
// the floor, its own sound and the other sounding notes' harmonics under it,
// and apart what other notes of its own pitch add, the ring a restrike rises over
func (f *accFit) predict(i, h, t int) (p, own, ring float32) {
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
		switch {
		case int(tm.j) == i:
			own = v
		case o.midi == n.midi:
			ring += v
		}
	}
	return p, own, ring
}

// sweep fits the activations frame by frame, and the floor, by multiplicative
// updates of the Kullback-Leibler fit, a frozen note's activation held; the
// templates are updated after it unless held, from what the frames accumulated
func (f *accFit) sweep(frozen func(*accNote) bool, templates bool) {
	notes := f.notes
	var numW, denW [RipPitches][accHarmonics]float64
	var active []int
	next := 0
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
		var acc *accTemplates
		if templates {
			acc = &accTemplates{&numW, &denW}
		}
		f.settle(t, active, -1, frozen, acc)
		for _, i := range active {
			f.local[i] = -1
		}
	}
	if templates {
		f.share(&numW, &denW)
	}
	// a note's activation may rise through its attack and only swell after
	for _, n := range notes {
		if frozen != nil && frozen(n) {
			continue
		}
		for u := n.start - n.t0 + accRise + 1; u < len(n.g); u++ {
			if u > 0 && n.g[u] > n.g[u-1]*accSwell {
				n.g[u] = n.g[u-1] * accSwell
			}
		}
	}
}

// accTemplates accumulates what a frame says of each template's harmonics
type accTemplates struct {
	num, den *[RipPitches][accHarmonics]float64
}

// settle fits the activations of the notes active at frame t, their local
// indices set, and the floor there, over accInner multiplicative updates, a
// frozen note's held and note skip absent; the last update feeds the templates'
// accumulators when given
func (f *accFit) settle(t int, active []int, skip int, frozen func(*accNote) bool, acc *accTemplates) {
	notes := f.notes
	const eps = 1e-9
	num, den := make([]float32, len(notes)), make([]float32, len(notes))
	for it := range accInner {
		last := it == accInner-1
		for _, i := range active {
			num[i], den[i] = 0, 0
		}
		var numB, denB float32
		for _, i := range active {
			if i == skip {
				continue
			}
			n := notes[i]
			row := n.mag[f.local[i]*n.nh:][:n.nh]
			for h := range n.nh {
				p, _, _ := f.predict(i, h, t)
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
					if last && acc != nil {
						acc.num[o.midi-RipMinMIDI][tm.k] += float64(r * o.g[u] * tm.c)
						acc.den[o.midi-RipMinMIDI][tm.k] += float64(o.g[u] * tm.c)
					}
				}
			}
		}
		for _, j := range active {
			if j == skip || frozen != nil && frozen(notes[j]) {
				continue
			}
			notes[j].g[f.local[j]] *= (num[j] + eps) / (den[j] + eps)
		}
		f.b[t] *= (numB + eps) / (denB + eps)
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
// harmonics the others leave unexplained, the rise of the sound measured at its
// harmonics over the frames before, the fit's loss without the note, its own
// level in dB, its share over every frame measured, the share of its
// harmonics the fit cannot see as its own, under the floor or another pitch's,
// and the step its memory reads at its free partials
type accEvidence struct {
	share, unF0, unOdd, rise, loo, level, span, covered, step float64
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
	var sumM, sumOwn, mF0, unF0, mOdd, unOdd, klFull, klLess, resOn, resPre, peak, spanM, spanOwn float64
	var hM, hNotes, hFloor [accHarmonics]float64 // each harmonic over the onset: measured, other pitches' notes, the floor
	for t := n.t0; t < n.t1; t++ {
		u := t - n.t0
		onset := t >= n.start-1 && t < n.start+accOnset
		if !onset && t >= n.start-2 && t < n.start+accOnset {
			continue
		}
		active := f.notesAt(t)
		for _, j := range active {
			f.local[j] = t - f.notes[j].t0
		}
		row := n.mag[u*n.nh:][:n.nh]
		var res float64
		for h := range n.nh {
			p, own, ring := f.predict(i, h, t)
			m, others := float64(row[h]), float64(p-own)
			un := math.Max(m-others, 0)
			res += m
			if t >= n.start {
				spanM, spanOwn = spanM+m, spanOwn+float64(own)
			}
			if !onset {
				continue
			}
			floor := float64(f.b[t] * n.floor[h])
			hM[h], hNotes[h], hFloor[h] = hM[h]+m, hNotes[h]+others-float64(ring)-floor, hFloor[h]+floor
			sumM, sumOwn = sumM+m, sumOwn+float64(own)
			peak = math.Max(peak, float64(own))
			if h == 0 {
				mF0, unF0 = mF0+m, unF0+un
			}
			if h%2 == 0 {
				mOdd, unOdd = mOdd+m, unOdd+un
			}
			klFull += m*math.Log((m+eps)/(float64(p)+eps)) - m + float64(p)
		}
		if onset { // the fit's loss here without the note, the others settled again in its absence
			klLess += f.without(i, t, active)
		}
		switch {
		case onset:
			resOn = math.Max(resOn, res)
		case t < n.start:
			resPre = math.Max(resPre, res)
		}
		for _, j := range active {
			f.local[j] = -1
		}
	}
	var covered float64 // the harmonics the fit cannot see as the note's: under the floor, or a third of them another pitch's
	for h := range n.nh {
		if hM[h] <= 2*hFloor[h] || hNotes[h] >= hM[h]/3 {
			covered++
		}
	}
	covered /= float64(n.nh)
	return accEvidence{
		share: sumOwn / (sumM + eps), unF0: unF0 / (mF0 + eps), unOdd: unOdd / (mOdd + eps),
		rise: min(max(20*math.Log10((resOn+1e-7)/(resPre+1e-7)), -20), 60), loo: (klLess - klFull) / (sumM + eps),
		level: 20 * math.Log10(peak+1e-7), span: spanOwn / (spanM + eps), covered: covered, step: n.step,
	}
}

// without is the Kullback-Leibler loss over note i's harmonics at frame t with
// the note absent and the other notes and the floor settled again without it,
// their fit restored after
func (f *accFit) without(i, t int, active []int) float64 {
	const eps = 1e-9
	saved := make([]float32, len(active))
	for k, j := range active {
		saved[k] = f.notes[j].g[f.local[j]]
	}
	b := f.b[t]
	u := f.local[i]
	f.local[i] = -1
	f.settle(t, active, i, nil, nil)
	n := f.notes[i]
	row := n.mag[u*n.nh:][:n.nh]
	var kl float64
	for h := range n.nh {
		p, _, _ := f.predict(i, h, t)
		m := float64(row[h])
		kl += m*math.Log((m+eps)/(float64(p)+eps)) - m + float64(p)
	}
	f.local[i] = u
	for k, j := range active {
		f.notes[j].g[f.local[j]] = saved[k]
	}
	f.b[t] = b
	return kl
}

// notesAt is the notes measured at frame t
func (f *accFit) notesAt(t int) []int {
	var out []int
	for j, o := range f.notes {
		if o.t0 > t {
			break
		}
		if o.t1 > t {
			out = append(out, j)
		}
	}
	return out
}

// accScore weighs a note's evidence with what the front end said of it into the
// odds that it is a note of the recording: a logistic fit on graded recordings, a
// piano in a room and through a phone, a keyboard and a distorted guitar
// rendered from the same piece. Reported notes and candidates are weighed apart;
// the fit's loss without the note is read but unweighed, its fitted sign running
// against its sense beside the shares.
type accScore struct {
	bias, share, unF0, unOdd, rise, loo, strike, support, low, level, ringing, span, step float64
}

var (
	accReported  = accScore{-3.83, 1.76, 1.48, 1.04, 0.60, 0, 0.13, 3.42, -0.09, 0.29, 0.64, 0.76, 1.04}
	accCandidate = accScore{-4.61, 0.29, -1.58, 2.60, 0.46, 0, 0.32, 4.68, -0.35, 0.97, 0.94, 0.45, 0.35}
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
		s.level*min(max(in.level-ref, -60), 10)/10 + s.ringing*flag(in.ringing) + s.span*math.Log(math.Max(in.span, 1e-3)) +
		s.step*math.Log(math.Max(in.step, 0.05))
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
	var kept, dropped []*accNote
	for i, n := range notes {
		// a held note whose harmonics all sit under louder pitches' is one the fit
		// cannot see, a soft line under a chord: it stays on the heads' word
		if ev[i].covered > accCovered && n.end-n.start >= accHeld || accReported.odds(f.accInput(n, ev[i], notes, refs), ref) >= accBar {
			kept = append(kept, n)
		} else {
			dropped = append(dropped, n)
		}
	}
	accRingOn(kept, dropped)
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
		// a strike on a pitch still sounding is the decoder's StrikeAgain to judge: the
		// fit cannot tell a held note's swell from a new strike, and one admitted cuts it
		ev2 := fit2.evidences()
		for i, n := range all {
			if in := f.accInput(n, ev2[i], all, refs); n.cand && !in.ringing && accCandidate.odds(in, ref) >= accCandBar {
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

// accRingOn lets a kept note sound on through a dropped note of its pitch that
// started where it ended: the decoder cuts a note at every strike of its pitch,
// and a strike its ring explains is no new note but the same one still sounding
func accRingOn(kept, dropped []*accNote) {
	for _, d := range dropped {
		for _, k := range kept {
			if k.midi == d.midi && k.start < d.start && k.end >= d.start-accDup && k.end < d.end {
				k.end = d.end
			}
		}
	}
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
				for end < T && r.sal(end, s) >= p.Voicing*r.ref && !(float64(at(f.Strike, end, midi)) >= p.Strike && at(f.Strike, end, midi) >= at(f.Strike, end-1, midi) && at(f.Strike, end, midi) > at(f.Strike, end+1, midi)) {
					end++
				}
				out = append(out, r.note(t, min(max(end, t+5), T), s))
			}
		}
	}
	return out
}
