package audio

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
)

// A live rip's pace: the decoders read the frames at every ripStreamStep
// seconds of them, once a bar could freeze there; a bar freezes when
// ripStreamBehind bars follow it; the decoders read ripStreamContext bars
// before the open ones, and before the first bars freeze the last
// ripStreamWait seconds at most.
const (
	ripStreamStep    = 0.5
	ripStreamBehind  = 2
	ripStreamContext = 2
	ripStreamWait    = 30
)

// RipStream rips a recording as it plays: blocks of samples go in at any rate, and
// each bar freezes once two follow it, as the offline rip lays it out, from the
// first six bars on, whose tempo, meter and key then hold (Bars). It keeps the
// frames the open bars and two before them read, and the recording for Final; its
// bars are the same whatever the blocks. It is not safe for concurrent use.
type RipStream struct {
	rp   Ripper
	p    RipParams
	ear  RipEar
	rate int       // the samples' rate, the first Write's
	n    int       // samples written, at rate
	kept []float32 // every sample written, for Final
	res  *resampler
	aw   *aWeighting
	tr   *toneReader
	// the window: the frames from frame w0, the samples at RipRate from w0's
	// first and the A-weighted samples at rate from sample y0
	f    *RipFrames
	w0   int
	x, y []float64
	y0   int
	nx   int // samples at RipRate heard
	next int // the frame the decoders next read up to
	// the frames heard since the last Write began, wrote frames in, that the
	// window has let go already, from frame late0: Frames' until the next Write
	late         *RipFrames
	late0, wrote int
	flux         []float64 // the decoders' onset strength, the window's left as heard
	// the loudest of each drum's onset strength and each onset band, and each
	// register's loudest row a frame, over the frames up to counted
	loud    [InstrClap]float64
	peak    [3]float64
	levels  [len(ripRegisters)]ripLevels
	counted int
	// held from the first bars frozen: the beat tracked and the reading the
	// bars are decoded in, the meter and tempo they are named, the key and the
	// frame the music starts
	held  bool
	track ripTrack
	meter Meter
	tempo float64
	root  int
	scale ScaleID
	start int
	// the last frozen bar's line and its end, the line the next bar starts on
	prev, line   int
	rip          *Rip
	phases       []float64          // the frozen notes' phases against the grid, sorted
	hitLoud      [InstrClap]float64 // each drum's strongest hit frozen
	melody, bass []ripNote          // the frozen notes, for the voices
	// the frozen notes and hits near the line, which a later read may hear a
	// frame or two off
	tail     []ripNote
	tailHits [InstrClap][]int
	hits     [InstrClap][]ripStreamHit
	// the bars after the frozen ones as last laid out, and the frame of each
	// one's line through the last one's end
	open   []RipBar
	openAt []int
	closed bool
}

// RipOpenBar is a bar after a stream's frozen ones as its decoders last laid it
// out, on the frames its line and the next bar's fall on, as Frames counts
// them: provisional, as it may change until it freezes among Bars
type RipOpenBar struct {
	RipBar
	Start, End int
}

// ripStreamHit is a frozen hit as the drums' tone reads it: where it starts, in
// seconds, and its tone and level there
type ripStreamHit struct {
	at    float64
	tone  toneRead
	level levelRead
}

// NewRipStream starts a live rip by rp, whose front end must hear a recording
// as it plays; a Grid pinning the downbeat or the beats is refused, as a
// stream decodes its bars.
func NewRipStream(rp Ripper) (*RipStream, error) {
	if rp.Grid != nil && (rp.Grid.Downbeat >= 0 || rp.Grid.Beats != nil) {
		return nil, errors.New("rip: a stream decodes its bars, so its grid pins the tempo alone")
	}
	var fe RipFrontEnd = analysis{}
	if rp.FrontEnd != nil {
		fe = rp.FrontEnd
	}
	live, ok := fe.(RipLiveFrontEnd)
	if !ok {
		return nil, errors.New("rip: the front end cannot hear a recording as it plays")
	}
	p := rp.params()
	ear, err := live.Ear(p, rp.Spectrum)
	if err != nil {
		return nil, err
	}
	s := &RipStream{rp: rp, p: p, ear: ear, tr: newToneReader(), f: &RipFrames{Hop: RipFrameSec}, peak: [3]float64{1e-9, 1e-9, 1e-9}}
	s.next = s.step()
	return s, nil
}

// Write hears the next samples, mono at rate, every block at the first's rate,
// and freezes the bars they complete; past the twenty minutes a rip reads they
// are let go.
func (s *RipStream) Write(x []float64, rate int) error {
	switch {
	case s.closed:
		return errors.New("rip: the stream is closed")
	case rate <= 0 || s.rate != 0 && rate != s.rate:
		return fmt.Errorf("rip: a block at %d Hz in a stream at %d Hz", rate, s.rate)
	}
	if s.rate == 0 {
		s.rate, s.res, s.aw = rate, newResampler(rate, RipRate), newAWeighting(rate)
	}
	x = x[:min(len(x), max(ripMaxSec*rate-s.n, 0))]
	if len(x) == 0 {
		return nil
	}
	s.late, s.wrote = nil, s.heard()
	s.n += len(x)
	for _, v := range x {
		s.kept = append(s.kept, float32(v))
	}
	at := len(s.y)
	s.y = append(s.y, x...)
	s.aw.filter(s.y[at:])
	at = len(s.x)
	s.x = s.res.write(s.x, x)
	s.nx += len(s.x) - at
	if err := s.hear(s.ear.Hear(s.x[at:])); err != nil {
		return err
	}
	for ; s.heard() >= s.next; s.next += s.step() {
		if err := s.decode(s.next, false); err != nil {
			return err
		}
	}
	return nil
}

// Bars is the rip of the bars frozen so far, nil before the first: the tempo,
// meter and key held, each bar's chord, beats, drums and notes, and the
// transcript of the notes heard up to the last; the voices and the drums' tone
// come with Close. It shares the stream's bars, so it is read, not changed.
func (s *RipStream) Bars() *Rip {
	if s.rip == nil {
		return nil
	}
	r := *s.rip
	r.Bars, r.Beats = slices.Clip(r.Bars), slices.Clip(r.Beats)
	r.Progression, r.Notes = slices.Clip(r.Progression), slices.Clip(r.Notes)
	return &r
}

// Frames is the frames heard from frame since on, as the ear gave them, and the
// index of the first given: since, or the first the stream still holds where
// that is later, which a caller that asks after every Write never meets, as a
// frame a Write hears stays until the next. It shares the stream's frames, so
// it is read, not changed.
func (s *RipStream) Frames(since int) (*RipFrames, int) {
	from := min(max(since, s.w0), s.heard())
	f := *s.f
	f.cut(from-s.w0, f.frames())
	if s.late == nil || since >= s.w0 {
		return &f, from
	}
	from = max(since, s.late0)
	late := *s.late
	late.cut(from-s.late0, late.frames())
	late.extend(&f)
	return &late, from
}

// Open is the bars after the frozen ones as the decoders laid them out when the
// last bar froze, the read that freezes a bar laying out those after it too:
// provisional, and none before the first bars freeze or once the stream is
// closed. It shares the stream's bars, so it is read, not changed.
func (s *RipStream) Open() []RipOpenBar {
	if len(s.open) == 0 {
		return nil
	}
	out := make([]RipOpenBar, len(s.open))
	for i, b := range s.open {
		out[i] = RipOpenBar{RipBar: b, Start: s.openAt[i], End: s.openAt[i+1]}
	}
	return out
}

// Close hears the end of the recording, freezes every bar left and returns the
// rip of everything heard: the bars frozen, the transcript of every note, the
// voices a front end named and the drums' tone.
func (s *RipStream) Close() (*Rip, error) {
	if s.closed {
		return nil, errors.New("rip: the stream is closed")
	}
	s.closed = true
	if s.rate == 0 || s.n < 4*s.rate {
		return nil, errors.New("rip: the recording is shorter than four seconds")
	}
	s.late, s.wrote = nil, s.heard()
	at := len(s.x)
	s.x = s.res.close(s.x)
	s.nx += len(s.x) - at
	if err := s.hear(s.ear.Hear(s.x[at:])); err != nil {
		return nil, err
	}
	if err := s.hear(s.ear.Close()); err != nil {
		return nil, err
	}
	if s.heard() < 2 {
		return nil, errors.New("rip: the front end heard nothing")
	}
	if err := s.decode(s.heard(), true); err != nil {
		return nil, err
	}
	if s.rip == nil {
		return nil, errors.New("rip: no full bar found")
	}
	r := s.rip
	last, dur := toneLast(s.nx), float64(s.n)/float64(s.rate)
	var starts [InstrClap][]float64
	for d, hs := range s.hits {
		for _, h := range hs {
			starts[d] = append(starts[d], h.at)
		}
	}
	for d, hs := range s.hits {
		var tones []toneRead
		var levels []levelRead
		for _, h := range hs {
			if toneFits(int(math.Round(h.at/RipFrameSec)), last) {
				tones = append(tones, h.tone)
			}
			if levelFits(InstrumentType(d), h.at, dur, starts) {
				levels = append(levels, h.level)
			}
		}
		r.Tone[d].Bands, r.Tone[d].Hits = toneOf(tones)
		r.Tone[d].Level, r.Tone[d].Levels = levelOf(levels)
	}
	r.MelodyVoice, r.UnderVoice, r.BassVoice = voiceOf(s.melody), voiceOf(s.melody), voiceOf(s.bass)
	slices.SortStableFunc(r.Notes, func(a, b RipNote) int { return cmp.Compare(a.Start, b.Start) })
	return s.Bars(), nil
}

// Final is the rip of the recording heard by the full offline path, as
// Ripper.Samples rips it: a whole recording decodes better than a stream.
func (s *RipStream) Final() (*Rip, error) {
	x := make([]float64, len(s.kept))
	for i, v := range s.kept {
		x[i] = float64(v)
	}
	return s.rp.Samples(x, s.rate)
}

// hear takes the frames an ear gave into the window
func (s *RipStream) hear(f *RipFrames, err error) error {
	if err == nil {
		s.f.extend(f)
	}
	return err
}

// heard is the frames heard
func (s *RipStream) heard() int { return s.w0 + s.f.frames() }

// step is the frames between two reads
func (s *RipStream) step() int { return int(ripStreamStep / s.f.Hop) }

// count takes the frames up to upto into the loudest onsets and bands heard
// and the registers' levels
func (s *RipStream) count(upto int) {
	f := s.f
	for t := s.counted - s.w0; t < upto-s.w0; t++ {
		for d := range f.Onset {
			if t < len(f.Onset[d]) {
				s.loud[d] = max(s.loud[d], f.Onset[d][t])
			}
		}
		for b := range f.bands {
			if t < len(f.bands[b]) {
				s.peak[b] = max(s.peak[b], f.bands[b][t])
			}
		}
		for i, lh := range ripRegisters {
			r := register{f: f, lo: lh[0], reach: registerReach(lh[0], lh[1])}
			s.levels[i].add(r.peak(t))
		}
	}
	s.counted = upto
}

// decode reads the frames up to upto, all at the end, and the samples under
// them: before the first bars freeze, the bars over the frames so far once six
// bars of 4/4 are tracked in them, or at the end; after, the bars on from the
// last one frozen once a bar could freeze
func (s *RipStream) decode(upto int, end bool) error {
	s.count(upto)
	if !s.held && upto-s.w0 > int(ripStreamWait/s.f.Hop) {
		s.drop(upto - int(ripStreamWait/s.f.Hop))
	}
	if upto-s.w0 < 2 || s.held && !end && upto < s.line+(ripStreamBehind+1)*(s.line-s.prev) {
		return nil
	}
	f := *s.f
	f.cut(0, upto-s.w0)
	x := s.x
	if !end {
		x = x[:min(len(x), (upto-s.w0)*RipHop+RipN/2)]
	}
	f.loud, f.refs = s.loud, map[[2]int]float64{}
	for i, lh := range ripRegisters { // the ninetieth percentile, as register reads it
		f.refs[lh] = s.levels[i].at(s.levels[i].n * 9 / 10)
	}
	if f.bands[0] != nil { // against the loudest heard, on a copy: the window's stay as heard
		s.flux = append(s.flux[:0], f.Flux...)
		f.Flux = s.flux
		f.flux(s.peak)
	}
	if !s.held && !end {
		if _, tr := s.rp.track(&f, s.p); len(tr.pulses) <= StepsPerBeat*ripMeterBars*tr.k {
			return nil
		}
	}
	ev := s.rp.events(&f, x, s.p)
	if !s.held {
		return s.hold(&f, x, ev, end)
	}
	return s.follow(&f, x, ev, end)
}

// hold decodes the first bars as the offline rip decodes its own, over the
// frames heard, and holds their beat, reading, meter, tempo and key
func (s *RipStream) hold(f *RipFrames, x []float64, ev ripEvents, end bool) error {
	t := musicStart(x)
	if t > 0 {
		ev.hits, ev.bass, ev.melody = heardFrom(t, ev.hits, ev.bass, ev.melody)
	}
	g, bpm, tr, err := s.rp.grid(f, ev, s.p)
	if err != nil {
		if end {
			return err
		}
		return nil
	}
	s.held, s.track, s.meter, s.tempo = true, tr, g.m, bpm
	s.root, s.scale, s.start = ev.root, ev.scale, s.w0+t
	s.rip = &Rip{BPM: int(math.Round(bpm)), Meter: g.m, Tuning: f.Tuning, Root: ev.root, Scale: ev.scale, Downbeat: float64(s.w0+g.lines[0]) * f.Hop, Params: s.p}
	shift := int(math.Round(g.lag(slices.Concat(ev.melody, ev.bass)) * g.stepFrames()))
	return s.freeze(f, x, g, ev, 0, shift, end)
}

// follow decodes the bars on from the last one frozen: the beats tracked at the
// beat held from its line, the bars of the reading held on them from it, the
// last frozen bar the grid's first, so a note on its line lands where the
// offline grid would place it
func (s *RipStream) follow(f *RipFrames, x []float64, ev ripEvents, end bool) error {
	if t := s.start - s.w0; t > 0 {
		ev.hits, ev.bass, ev.melody = heardFrom(t, ev.hits, ev.bass, ev.melody)
	}
	ev.root, ev.scale = s.root, s.scale
	from := s.line - s.w0
	lines := []int{from}
	if pulses := f.beats(s.track.period/float64(s.track.k), s.p, from); len(pulses) > 1 {
		rds, err := f.readings(pulses, s.track.k, s.track.bpm, &s.track.rd.m)
		if err != nil {
			return err
		}
		lines, _, _, _, _ = f.barPath(rds[0], f.lineCues(ev.hits, s.root%12, ev.bass), true)
	}
	g := ripGrid{lines: slices.Concat([]int{s.prev - s.w0}, lines), m: s.meter}
	ev = s.dedup(ev, g.stepFrames()/2)
	shift := int(math.Round(s.lag(&g, slices.Concat(ev.melody, ev.bass)) * g.stepFrames()))
	return s.freeze(f, x, g, ev, 1, shift, end)
}

// dedup drops the events a frozen one near the line holds already: a note of
// its pitch or a hit of its drum starting within gap frames of it
func (s *RipStream) dedup(ev ripEvents, gap float64) ripEvents {
	near := func(a, b int) bool { return math.Abs(float64(a-b)) <= gap }
	again := func(n ripNote) bool {
		return slices.ContainsFunc(s.tail, func(o ripNote) bool { return o.midi == n.midi && near(o.start, s.w0+n.start) })
	}
	ev.melody, ev.bass = slices.DeleteFunc(ev.melody, again), slices.DeleteFunc(ev.bass, again)
	for d := range ev.hits {
		ev.hits[d] = slices.DeleteFunc(ev.hits[d], func(h ripHit) bool {
			return slices.ContainsFunc(s.tailHits[d], func(t int) bool { return near(t, s.w0+h.frame) })
		})
	}
	return ev
}

// lag is the median of the frozen notes' phases and the open bars' notes', in
// steps, as the offline grid's lag is of every note's; zero for too few notes
// to say
func (s *RipStream) lag(g *ripGrid, notes []ripNote) float64 {
	ph := slices.Clone(s.phases)
	for _, n := range notes {
		if bar, _, ok := g.at(n.start); ok && bar > 0 {
			if p, ok := g.phase(n.start); ok {
				ph = append(ph, p)
			}
		}
	}
	if len(ph) < 4 {
		return 0
	}
	slices.Sort(ph)
	return ph[len(ph)/2]
}

// freeze lays the events on the grid and freezes its bars from first, all but the
// newest ripStreamBehind until the recording ends, with the notes and hits they
// hold for the transcript, the lag, the voices and the drums' tone; the first
// freeze takes the events before the first bar too, and the end those after the
// last. The window then lets go of the frames no later read needs.
func (s *RipStream) freeze(f *RipFrames, x []float64, g ripGrid, ev ripEvents, first, shift int, end bool) error {
	n := g.bars() - first
	if !end {
		n -= ripStreamBehind
	}
	if n <= 0 && !end {
		return nil
	}
	n = max(n, 0)
	loud := s.hitLoud
	for d, hs := range ev.hits {
		for _, h := range hs {
			loud[d] = max(loud[d], h.strength)
		}
	}
	r := g.lay(f, x, ev, s.tempo, shift, loud, s.p)
	s.open, s.openAt = r.Bars[first+n:], s.openAt[:0]
	for _, l := range g.lines[first+n:] {
		s.openAt = append(s.openAt, s.w0+l)
	}
	frozen := func(frame int) bool {
		bar, _, ok := g.at(frame)
		switch {
		case ok:
			return bar >= first && bar < first+n
		case frame < g.lines[0]:
			return first == 0
		}
		return end
	}
	if n > 0 {
		s.rip.Bars = append(s.rip.Bars, r.Bars[first:first+n]...)
		s.rip.Progression = append(s.rip.Progression, r.Progression[first:first+n]...)
		if k := len(s.rip.Beats); k > 0 { // the last bar's end, the first new bar's line
			s.rip.Beats = s.rip.Beats[:k-1]
		}
		per := g.m.Beats()
		for i := first * per; i <= (first+n)*per; i++ {
			s.rip.Beats = append(s.rip.Beats, float64(s.w0+g.beat(i))*f.Hop)
		}
	}
	line := s.w0 + g.lines[first+n]
	near := int(math.Ceil(g.stepFrames())) // frames before the line a frozen event may be heard again from
	var notes []RipNote
	var tail []ripNote
	for i, ns := range [2][]ripNote{ev.melody, ev.bass} {
		for _, nt := range ns {
			if !frozen(nt.start - shift) {
				continue
			}
			if p, ok := g.phase(nt.start); ok {
				at, _ := slices.BinarySearch(s.phases, p)
				s.phases = slices.Insert(s.phases, at, p)
			}
			nt.start, nt.end = s.w0+nt.start, s.w0+nt.end
			bright := nt.bright
			if i == 1 {
				bright = RipUnmeasured
				s.bass = append(s.bass, nt)
			} else {
				s.melody = append(s.melody, nt)
			}
			notes = append(notes, RipNote{Start: float64(nt.start) * f.Hop, End: float64(nt.end) * f.Hop, MIDI: nt.midi, Vel: nt.vel, Bass: i == 1, Second: nt.second, Bright: bright, Bend: nt.bend, BendAt: nt.bendAt, Program: -1})
			if nt.start >= line-near {
				tail = append(tail, nt)
			}
		}
	}
	slices.SortStableFunc(notes, func(a, b RipNote) int { return cmp.Compare(a.Start, b.Start) })
	s.rip.Notes = append(s.rip.Notes, notes...)
	var tailHits [InstrClap][]int
	for d, hs := range ev.hits {
		for _, h := range hs {
			if !frozen(h.frame) {
				continue
			}
			s.hitLoud[d] = max(s.hitLoud[d], h.strength)
			at := hitStart(s.x, s.w0*RipHop, float64(s.w0+h.frame)*f.Hop)
			tone := s.tr.read(s.x, s.w0*RipHop, int(math.Round(at/RipFrameSec)))
			s.hits[d] = append(s.hits[d], ripStreamHit{at: at, tone: tone, level: readLevel(s.y, s.y0, s.rate, at)})
			if s.w0+h.frame >= line-near {
				tailHits[d] = append(tailHits[d], s.w0+h.frame)
			}
		}
	}
	if n > 0 {
		s.prev, s.line, s.tail, s.tailHits = s.w0+g.lines[first+n-1], line, tail, tailHits
		s.drop(s.line - ripStreamContext*(s.line-s.prev))
	}
	return nil
}

// drop lets go of the frames before frame w and the samples only they read
func (s *RipStream) drop(w int) {
	w = min(w, s.heard())
	if w <= s.w0 {
		return
	}
	if a := max(s.w0, s.wrote); a < w {
		late := *s.f
		late.cut(a-s.w0, w-s.w0)
		if s.late == nil {
			s.late, s.late0 = &late, a
		} else {
			s.late.extend(&late)
		}
	}
	s.f.cut(w-s.w0, s.f.frames())
	cut := min((w-s.w0)*RipHop, len(s.x))
	s.x = s.x[:copy(s.x, s.x[cut:])]
	s.w0 = w
	y0 := int(float64(w*RipHop) / RipRate * float64(s.rate))
	cut = min(max(y0-s.y0, 0), len(s.y))
	s.y, s.y0 = s.y[:copy(s.y, s.y[cut:])], s.y0+cut
}

// ripLevelSteps is the steps an octave a live rip bins a register's levels in
const ripLevelSteps = 1024

// ripLevels is the levels heard of a register's loudest row a frame, binned in
// steps of a 1024th of an octave: its reference loudness over the whole
// recording heard, as the offline rip reads it over every frame, in memory
// the levels' range sets
type ripLevels struct {
	bins     map[int]int
	zeros, n int
}

func (l *ripLevels) add(v float64) {
	l.n++
	if v <= 0 {
		l.zeros++
		return
	}
	if l.bins == nil {
		l.bins = map[int]int{}
	}
	l.bins[int(math.Floor(math.Log2(v)*ripLevelSteps))]++
}

// at is the level of rank i among those heard, from the quietest, its bin's
// middle
func (l *ripLevels) at(i int) float64 {
	if i -= l.zeros; i < 0 {
		return 0
	}
	for _, b := range slices.Sorted(maps.Keys(l.bins)) {
		if i -= l.bins[b]; i < 0 {
			return math.Exp2((float64(b) + 0.5) / ripLevelSteps)
		}
	}
	return 0
}
