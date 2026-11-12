package audio

import (
	"cmp"
	"maps"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
)

// TestRipStreamFreezesTheOfflineBars: the loop fed in 50 ms blocks gives no bar
// before six are heard, then each bar within two bars and a second of its end,
// never changed after; from the third on they are RipSamples's note for note, on
// its beats and bar lines.
func TestRipStreamFreezesTheOfflineBars(t *testing.T) {
	const bpm, bars = 140, 12
	ae, slots := startLoopEngine(t)
	buf, err := ae.RenderMusic(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	off, err := RipSamples(buf, AudioSampleRate, DefaultRipParams())
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewRipStream(Ripper{Params: DefaultRipParams()})
	if err != nil {
		t.Fatal(err)
	}
	barSec := 60.0 / bpm * BeatsPerBar
	var given []RipBar
	for i, block := 0, AudioSampleRate/20; i < len(buf); i += block {
		if err := s.Write(buf[i:min(i+block, len(buf))], AudioSampleRate); err != nil {
			t.Fatal(err)
		}
		heard, frozen := float64(min(i+block, len(buf)))/AudioSampleRate, 0.0
		if r := s.Bars(); r != nil {
			if len(r.Bars) < len(given) || len(given) > 0 && !reflect.DeepEqual(r.Bars[:len(given)], given) {
				t.Fatalf("at %.2fs the bars given before changed", heard)
			}
			given, frozen = slices.Clone(r.Bars), r.Beats[len(r.Beats)-1]
			if len(given) > 0 && heard < ripMeterBars*barSec {
				t.Fatalf("%d bars given at %.2fs, before six bars", len(given), heard)
			}
		}
		if heard > ripMeterBars*barSec+1 && frozen+(ripStreamBehind+1)*barSec+1 <= heard {
			t.Fatalf("at %.2fs the bar ending at %.2fs is not given", heard, frozen+barSec)
		}
	}
	r, err := s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Bars) != len(off.Bars) || r.BPM != off.BPM || r.Meter != off.Meter {
		t.Fatalf("%d bars at %d bpm in %s, the offline rip %d at %d in %s", len(r.Bars), r.BPM, r.Meter, len(off.Bars), off.BPM, off.Meter)
	}
	for i := 2; i < len(r.Bars); i++ {
		if d := BarDistance(&r.Bars[i], &off.Bars[i]); d != 0 {
			t.Errorf("bar %d is %.2f from the offline rip's: %v | %v | %v | %v, want %v | %v | %v | %v", i, d,
				r.Bars[i].Drums, r.Bars[i].Melody, r.Bars[i].Under, r.Bars[i].Bass, off.Bars[i].Drums, off.Bars[i].Melody, off.Bars[i].Under, off.Bars[i].Bass)
		}
	}
	var downbeats []float64
	for i := 0; i < len(off.Beats); i += off.Meter.Beats() {
		downbeats = append(downbeats, off.Beats[i])
	}
	if sc := MatchBars(r, off.Beats, downbeats); !sc.Same || sc.Downbeats < 1 || sc.Beats < 1 {
		t.Errorf("against the offline rip's beats and bar lines %+v", sc)
	}
}

// TestRipStreamHoldsOnlyItsLastBars: however long the recording and whatever its
// blocks, once its first bars freeze a stream holds the frames and samples of the
// open bars, the two before them and a step, the bar lines on their frames.
func TestRipStreamHoldsOnlyItsLastBars(t *testing.T) {
	const bpm, bars = 140, 12
	ae, slots := startLoopEngine(t)
	buf, err := ae.RenderMusic(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewRipStream(Ripper{Params: DefaultRipParams()})
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	bar := 60.0 / bpm * BeatsPerBar
	most := (ripStreamContext+ripStreamBehind+1)*(bar+RipFrameSec) + ripStreamStep
	for i := 0; i < len(buf); {
		k := min(rng.IntN(AudioSampleRate/5)+1, len(buf)-i)
		if err := s.Write(buf[i:i+k], AudioSampleRate); err != nil {
			t.Fatal(err)
		}
		i += k
		if s.rip == nil {
			continue
		}
		// the samples run on past the frames by what the front end has yet to frame
		if held := float64(s.f.frames()) * s.f.Hop; held > most || float64(len(s.x)) > (most+0.5)*RipRate || float64(len(s.y)) > (most+0.5)*AudioSampleRate {
			t.Fatalf("at %.2fs the stream holds %.2fs of frames, %d samples and %d at the recording's rate; at most %.2fs", float64(i)/AudioSampleRate, held, len(s.x), len(s.y), most)
		}
	}
	if s.rip == nil {
		t.Fatal("no bar frozen")
	}
}

// TestRipStreamBarsAreTheSameWhateverTheBlocks: the loop fed in 50 ms blocks and
// in blocks of one sample to a second rips alike, bar for bar and note for note.
func TestRipStreamBarsAreTheSameWhateverTheBlocks(t *testing.T) {
	const bpm, bars = 140, 8
	ae, slots := startLoopEngine(t)
	buf, err := ae.RenderMusic(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(3, 4))
	var rips []*Rip
	for _, block := range []func() int{func() int { return AudioSampleRate / 20 }, func() int { return rng.IntN(AudioSampleRate) + 1 }} {
		s, err := NewRipStream(Ripper{Params: DefaultRipParams()})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(buf); {
			k := min(block(), len(buf)-i)
			if err := s.Write(buf[i:i+k], AudioSampleRate); err != nil {
				t.Fatal(err)
			}
			i += k
		}
		r, err := s.Close()
		if err != nil {
			t.Fatal(err)
		}
		rips = append(rips, r)
	}
	if !reflect.DeepEqual(rips[0], rips[1]) {
		t.Errorf("%d bars and %d notes in 50 ms blocks, %d and %d in others", len(rips[0].Bars), len(rips[0].Notes), len(rips[1].Bars), len(rips[1].Notes))
	}
}

// TestRipStreamGivesEachFrameOnce: asked after every Write for the frames since
// the last given, a caller gets every frame the stream heard, once and in order,
// each with the bands MelStream gives it, the same in 50 ms blocks and in blocks
// of six seconds, whose frames the window lets go of within the Write that
// hears them.
func TestRipStreamGivesEachFrameOnce(t *testing.T) {
	const bpm, bars = 140, 8
	ae, slots := startLoopEngine(t)
	buf, err := ae.RenderMusic(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := range buf { // louder to its end, so the loudest onset heard keeps moving
		buf[i] *= 0.5 + 0.5*float64(i)/float64(len(buf))
	}
	var given []*RipFrames
	for _, block := range []int{AudioSampleRate / 20, 6 * AudioSampleRate} {
		s, err := NewRipStream(Ripper{Params: DefaultRipParams(), Spectrum: true})
		if err != nil {
			t.Fatal(err)
		}
		all, next, late := &RipFrames{}, 0, false
		take := func() {
			f, from := s.Frames(next)
			if from != next {
				t.Fatalf("asked from frame %d, given from %d", next, from)
			}
			all.extend(f)
			next, late = next+f.frames(), late || s.late != nil
		}
		for i := 0; i < len(buf); i += block {
			if err := s.Write(buf[i:min(i+block, len(buf))], AudioSampleRate); err != nil {
				t.Fatal(err)
			}
			take()
		}
		if _, err := s.Close(); err != nil {
			t.Fatal(err)
		}
		take()
		if next != s.heard() || next < len(buf)/AudioSampleRate*RipRate/RipHop {
			t.Fatalf("%d frames given of %d heard, in %.1fs", next, s.heard(), float64(len(buf))/AudioSampleRate)
		}
		if given = append(given, all); late != (block > AudioSampleRate) {
			t.Errorf("in blocks of %d samples, frames let go within a Write: %v", block, late)
		}
	}
	if !reflect.DeepEqual(given[0], given[1]) {
		t.Errorf("the %d frames given depend on the blocks", given[0].frames())
	}
	var bands []float32
	ms := NewMelStream()
	for _, row := range append(ms.Write(Resample(buf, AudioSampleRate, RipRate)), ms.Close()...) {
		for _, v := range row {
			bands = append(bands, float32(v))
		}
	}
	if !slices.Equal(given[0].Spectrum, bands) {
		t.Errorf("the spectrum given, %d bands, is not MelStream's %d", len(given[0].Spectrum), len(bands))
	}
}

// TestRipStreamFreezesItsOpenBarsAsRead: no bar is open before the first freeze
// and none once the stream is closed; between, the open bars follow the frozen
// ones frame for frame, and each bar the loop then freezes, at the next freeze
// or at Close, is the bar read open in its place, on its frames, note for note.
func TestRipStreamFreezesItsOpenBarsAsRead(t *testing.T) {
	const bpm, bars = 140, 8
	ae, slots := startLoopEngine(t)
	buf, err := ae.RenderMusic(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewRipStream(Ripper{Params: DefaultRipParams()})
	if err != nil {
		t.Fatal(err)
	}
	frame := func(sec float64) int { return int(math.Round(sec / RipFrameSec)) }
	var open []RipOpenBar
	frozen, read := 0, 0
	// froze holds the bars frozen since the last read to the bars open at it
	froze := func(r *Rip) {
		for i, o := range open[:min(len(open), len(r.Bars)-frozen)] {
			k := frozen + i
			if at := frame(r.Beats[k*r.Meter.Beats()]); o.Start != at || BarDistance(&o.RipBar, &r.Bars[k]) != 0 {
				t.Errorf("bar %d froze at frame %d as %v, read open at %d as %v", k, at, r.Bars[k], o.Start, o.RipBar)
			}
			read++
		}
		frozen = len(r.Bars)
	}
	for i, block := 0, AudioSampleRate/20; i < len(buf); i += block {
		if err := s.Write(buf[i:min(i+block, len(buf))], AudioSampleRate); err != nil {
			t.Fatal(err)
		}
		r := s.Bars()
		if r == nil {
			if s.Open() != nil {
				t.Fatalf("at %.2fs a bar is open before any froze", float64(i+block)/AudioSampleRate)
			}
			continue
		}
		froze(r)
		open = s.Open()
		for j, o := range open {
			if from := frame(r.Beats[len(r.Beats)-1]); j == 0 && o.Start != from || j > 0 && o.Start != open[j-1].End || o.End <= o.Start {
				t.Fatalf("at %.2fs open bar %d spans frames %d to %d, the frozen bars ending at %d", float64(i+block)/AudioSampleRate, j, o.Start, o.End, from)
			}
		}
	}
	r, err := s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if froze(r); read < 3 || s.Open() != nil {
		t.Errorf("%d bars froze that were read open, of %d; %d open once closed", read, len(r.Bars), len(s.Open()))
	}
}

// TestRipStreamFinalUnderAJudgeIsTheOfflineRip: a stream whose Ripper carries a
// judge rips what it heard, at the end, as the offline rip does under that judge,
// which is not the rip the installed scores make.
func TestRipStreamFinalUnderAJudgeIsTheOfflineRip(t *testing.T) {
	ae, slots := startLoopEngine(t)
	buf, err := ae.RenderMusic(slots, 140, Meter{}, 8, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range buf { // as a stream keeps them
		buf[i] = float64(float32(v))
	}
	rp := Ripper{Params: DefaultRipParams(), FrontEnd: struck{}, Judge: judgeSaying([judgeStages]float64{0.9, 0.9})}
	off, err := rp.Samples(buf, AudioSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewRipStream(rp)
	if err != nil {
		t.Fatal(err)
	}
	for i, block := 0, AudioSampleRate/2; i < len(buf); i += block {
		if err := s.Write(buf[i:min(i+block, len(buf))], AudioSampleRate); err != nil {
			t.Fatal(err)
		}
	}
	final, err := s.Final()
	if err != nil || !reflect.DeepEqual(final, off) {
		t.Errorf("the stream's final rip is not the offline rip under the judge: %v", err)
	}
	rp.Judge = nil
	if plain, err := rp.Samples(buf, AudioSampleRate); err != nil || len(plain.Notes) >= len(off.Notes) {
		t.Errorf("the judge that says yes left %d notes of the installed scores' %d: %v", len(off.Notes), len(plain.Notes), err)
	}
}

// TestRipStreamGivesTheStrikesOfTheFramesItHolds: a stream whose Ripper reads
// the strikes gives, asked after every block for those since the last frozen,
// each strike once its bar freezes and the open bars' after them; the ones kept
// are the notes its bars freeze, as the offline rip's are its notes.
func TestRipStreamGivesTheStrikesOfTheFramesItHolds(t *testing.T) {
	ae, slots := startLoopEngine(t)
	buf, err := ae.RenderMusic(slots, 140, Meter{}, 12, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	kept := func(strikes []RipStrike) (at [][2]int) {
		for _, st := range strikes {
			if st.Kept {
				at = append(at, [2]int{st.Frame, st.MIDI})
			}
		}
		return at
	}
	notes := func(r *Rip) (at [][2]int) {
		for _, n := range r.Notes {
			at = append(at, [2]int{int(math.Round(n.Start / RipFrameSec)), n.MIDI})
		}
		slices.SortFunc(at, func(a, b [2]int) int { return cmp.Or(a[0]-b[0], a[1]-b[1]) })
		return at
	}
	rp := Ripper{Params: DefaultRipParams(), FrontEnd: struck{}, Strikes: true}
	off, err := rp.Samples(buf, AudioSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := kept(off.Strikes), notes(off); !slices.Equal(got, want) || len(off.Strikes) <= len(want) {
		t.Errorf("the offline rip keeps %d of %d strikes, its notes are %d: %v, want %v", len(got), len(off.Strikes), len(want), got, want)
	}
	s, err := NewRipStream(rp)
	if err != nil {
		t.Fatal(err)
	}
	var frozen []RipStrike
	next, open := 0, 0
	for i, block := 0, AudioSampleRate/20; i < len(buf); i += block {
		if err := s.Write(buf[i:min(i+block, len(buf))], AudioSampleRate); err != nil {
			t.Fatal(err)
		}
		strikes, n := s.Strikes(next)
		if len(strikes) > 0 && strikes[0].Frame < next || n > 0 && s.Bars() == nil {
			t.Fatalf("asked since frame %d, %d strikes from frame %d, %d frozen", next, len(strikes), strikes[0].Frame, n)
		}
		if frozen, open = append(frozen, strikes[:n]...), max(open, len(strikes)-n); n > 0 {
			next = strikes[n-1].Frame + 1
		}
	}
	r, err := s.Close()
	if err != nil {
		t.Fatal(err)
	}
	strikes, n := s.Strikes(next)
	if frozen = append(frozen, strikes...); n != len(strikes) || open == 0 {
		t.Errorf("%d of the %d strikes left at the end frozen, and %d open at most before it", n, len(strikes), open)
	}
	if got, want := kept(frozen), notes(r); !slices.Equal(got, want) || len(frozen) <= len(want) {
		t.Errorf("the stream froze %d strikes, %d kept, its notes are %d: %v, want %v", len(frozen), len(got), len(want), got, want)
	}
}

// ripWays are the ways a rip through a front end that hears strikes is
// measured: with no judge, with one of the fitted size loaded, and with the
// strikes read with their rows, as the rows a fit takes are written
func ripWays() map[string]Ripper {
	rp := Ripper{Params: DefaultRipParams(), FrontEnd: struck{}}
	judged, read := rp, rp
	judged.Judge = judgeSized(256, 64, [judgeStages]float64{0.9, 0.1})
	read.Strikes, read.Rows = true, true
	return map[string]Ripper{"unjudged": rp, "judged": judged, "read": read}
}

// BenchmarkRip is the offline rip of the loop, twelve bars, through a front end
// that hears strikes, so the accounting runs, each of ripWays.
func BenchmarkRip(b *testing.B) {
	ae, slots := startLoopEngine(b)
	buf, err := ae.RenderMusic(slots, 140, Meter{}, 12, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		b.Fatal(err)
	}
	for _, way := range slices.Sorted(maps.Keys(ripWays())) {
		rp := ripWays()[way]
		b.Run(way, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := rp.Samples(buf, AudioSampleRate); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkRipStream is a live rip of the loop, twelve bars in 50 ms blocks:
// through the analysis unread, and read after every block as a view reads it,
// the frames since the last with their spectrum, the open bars and the
// strikes; and through a front end that hears strikes, unread, each of ripWays.
func BenchmarkRipStream(b *testing.B) {
	ae, slots := startLoopEngine(b)
	buf, err := ae.RenderMusic(slots, 140, Meter{}, 12, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		b.Fatal(err)
	}
	ways := map[string]Ripper{"unread": {Params: DefaultRipParams()}, "read": {Params: DefaultRipParams(), Spectrum: true, Strikes: true}}
	for way, rp := range ripWays() {
		ways["struck/"+way] = rp
	}
	for _, way := range slices.Sorted(maps.Keys(ways)) {
		rp, read := ways[way], way == "read"
		b.Run(way, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				s, err := NewRipStream(rp)
				if err != nil {
					b.Fatal(err)
				}
				next := 0
				for i, block := 0, AudioSampleRate/20; i < len(buf); i += block {
					if err := s.Write(buf[i:min(i+block, len(buf))], AudioSampleRate); err != nil {
						b.Fatal(err)
					}
					if read {
						f, from := s.Frames(next)
						s.Strikes(next)
						next = from + f.frames()
						s.Open()
					}
				}
				if _, err := s.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// headFront is a front end that gives frames built by hand, a frame a hop of
// the samples heard, as a model's ear gives its own
type headFront struct{ f *RipFrames }

type headEar struct {
	f        *RipFrames
	n, given int
}

func (h headFront) Hear(x []float64, _ int, _ RipParams) (*RipFrames, error) {
	e := &headEar{f: h.f}
	return e.Hear(x)
}

func (h headFront) Ear(RipParams, bool) (RipEar, error) { return &headEar{f: h.f}, nil }

func (e *headEar) Hear(x []float64) (*RipFrames, error) {
	e.n += len(x)
	f := *e.f
	f.cut(e.given, e.n/RipHop)
	e.given = max(e.given, e.n/RipHop)
	return &f, nil
}

func (e *headEar) Close() (*RipFrames, error) { return nil, nil }

// headRip rips frames built by hand through headFront, offline or streamed in
// blocks of 50 ms, over a sine as long as the frames
func headRip(rp Ripper, f *RipFrames, stream bool) (*Rip, error) {
	n := f.frames()
	for d := range f.Onset {
		f.Onset[d] = make([]float64, n)
	}
	f.Salience = make([]float32, n*RipPitches)
	x := make([]float64, n*RipHop)
	for i := range x {
		x[i] = 0.3 * math.Sin(float64(i)*2*math.Pi*220/RipRate)
	}
	rp.Params, rp.FrontEnd = DefaultRipParams(), headFront{f}
	if !stream {
		return rp.Samples(x, RipRate)
	}
	s, err := NewRipStream(rp)
	if err != nil {
		return nil, err
	}
	for i, block := 0, RipRate/20; i < len(x); i += block {
		if err := s.Write(x[i:min(i+block, len(x))], RipRate); err != nil {
			return nil, err
		}
	}
	return s.Close()
}

// headLines are a rip's bar lines in frames
func headLines(r *Rip) (lines []int) {
	for i := 0; i < len(r.Beats); i += r.Meter.Beats() {
		lines = append(lines, int(math.Round(r.Beats[i]/RipFrameSec)))
	}
	return lines
}

// TestRipStreamReadsTheHeads: a stream through an ear whose frames carry the
// heads holds the head's tempo and meter, and every bar it freezes, the first
// and those it follows on with, starts on a line the downbeat head marks
func TestRipStreamReadsTheHeads(t *testing.T) {
	const n, beat, per = 4000, 40, 3
	f := headFrames(n, 43, beat, 0, per, 1)
	for _, m := range []*Meter{nil, {3, 4}} {
		off, err := headRip(Ripper{Meter: m}, f, false)
		if err != nil {
			t.Fatal(err)
		}
		r, err := headRip(Ripper{Meter: m}, f, true)
		if err != nil {
			t.Fatal(err)
		}
		lines := headLines(r)
		want := Meter{3, 8} // three heard beats with no drum and no sixteenth are written in eighths
		if m != nil {
			want = *m
		}
		if r.Meter != want || r.Meter != off.Meter || r.BPM != off.BPM || len(r.Bars) < n/(per*beat)-2 || !every(lines, beat, per*beat) {
			t.Errorf("pinned %v: %d bars in %s at %d bpm, the offline rip in %s at %d, lines %v: want %s on the head's lines, every %d frames from %d",
				m != nil, len(r.Bars), r.Meter, r.BPM, off.Meter, off.BPM, lines[:min(len(lines), 6)], want, per*beat, beat)
		}
	}
}

// TestAPinnedMeterKeepsItsTempoInTheSequencersRange: beats heard at 215 a minute
// with a line every sixth pinned 3/4, and at 73.8 with one every third pinned
// 6/8, take the beat an eighth and a quarter, offline and streamed: 3/4 at 108
// and 6/8 at 74, whose songs validate
func TestAPinnedMeterKeepsItsTempoInTheSequencersRange(t *testing.T) {
	for _, c := range []struct {
		beat, per int
		pin       Meter
		bpm       int
	}{{24, 6, Meter{3, 4}, 108}, {70, 3, Meter{6, 8}, 74}} {
		for _, stream := range []bool{false, true} {
			r, err := headRip(Ripper{Meter: &c.pin}, headFrames(4000, c.beat, c.beat, 0, c.per, 1), stream)
			if err != nil {
				t.Fatal(err)
			}
			_, song := r.Song("pinned", [RipParts][]int{})
			if err := ValidateSong(song); r.Meter != c.pin || r.BPM != c.bpm || err != nil {
				t.Errorf("beats every %d frames pinned %s, streamed %v: %s at %d (%v), want %s at %d", c.beat, c.pin, stream, r.Meter, r.BPM, err, c.pin, c.bpm)
			}
		}
	}
}

// TestHeadsThatGiveNoReadingLeaveTheGridToTheCues: a pinned bar longer than the
// beats heard, a head that is no posterior at a frame, or heads that mark
// nothing give no reading, and the rip is that of the same frames without the
// heads, offline and streamed
func TestHeadsThatGiveNoReadingLeaveTheGridToTheCues(t *testing.T) {
	down, beat, zero := headFrames(3000, 40, 40, 0, 4, 0), headFrames(3000, 40, 40, 0, 4, 0), headFrames(3000, 33, 40, 0, 4, 0)
	down.Downbeat[100], beat.Beat[100] = math.NaN(), math.NaN()
	clear(zero.Beat)
	clear(zero.Downbeat)
	for _, c := range []struct {
		name string
		f    *RipFrames
		pin  *Meter
	}{{"12/8 pinned over ten beats", headFrames(430, 40, 40, 0, 4, 0), &Meter{12, 8}}, {"a NaN in the downbeat head", down, nil}, {"a NaN in the beat head", beat, nil}, {"both heads zero", zero, nil}} {
		for _, stream := range []bool{false, true} {
			plain := *c.f
			plain.Beat, plain.Downbeat = nil, nil
			want, err := headRip(Ripper{Meter: c.pin}, &plain, stream)
			if err != nil {
				t.Fatal(err)
			}
			if r, err := headRip(Ripper{Meter: c.pin}, c.f, stream); err != nil || !reflect.DeepEqual(r, want) {
				t.Errorf("%s, streamed %v: %v, want the rip without the heads, %d bars of %s at %d", c.name, stream, err, len(want.Bars), want.Meter, want.BPM)
			}
		}
	}
}
