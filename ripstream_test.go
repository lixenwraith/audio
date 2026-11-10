package audio

import (
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
