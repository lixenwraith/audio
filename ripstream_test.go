package audio

import (
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

// BenchmarkRipStream is a live rip of the loop, twelve bars in 50 ms blocks
// through the analysis: unread, and read after every block as a view reads it,
// the frames since the last with their spectrum and the open bars.
func BenchmarkRipStream(b *testing.B) {
	ae, slots := startLoopEngine(b)
	buf, err := ae.RenderMusic(slots, 140, Meter{}, 12, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		b.Fatal(err)
	}
	for _, read := range []bool{false, true} {
		b.Run(map[bool]string{false: "unread", true: "read"}[read], func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				s, err := NewRipStream(Ripper{Params: DefaultRipParams(), Spectrum: read})
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
