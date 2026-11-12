package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// startLoopEngine starts a null engine on the test bank with a snare, a hihat,
// saw voices and three patterns, a beat, a melody and a bass line, so a loop the
// engine renders of them has a rip a perfect listener would make
func startLoopEngine(t testing.TB) (*AudioEngine, [MusicSlots]PatternID) {
	t.Helper()
	t.Cleanup(ResetRegistries)
	ResetRegistries()
	cfg := DefaultAudioConfig()
	cfg.Enabled = true
	cfg.ForceBackend = BackendNameNull
	cfg.BaseSounds = append(testBank(),
		&SoundDef{Name: "snare", Duration: 0.18, Norm: 0.8, Chain: []Proc{{Kind: "hp", Freq: 150}}, Layer: []Layer{
			{Gain: 0.7, Source: Source{Kind: "sweep", Wave: "sine", Freq: 200, FreqEnd: 170, Curve: "exp"}, Chain: []Proc{{Kind: "decay", Tau: 0.05}}},
			{Source: Source{Kind: "noise"}, Chain: []Proc{{Kind: "bp", Freq: 2500, Q: 0.6}, {Kind: "decay", Tau: 0.06}}}}},
		&SoundDef{Name: "hihat", Duration: 0.07, Norm: 0.7, Layer: []Layer{
			{Source: Source{Kind: "noise"}, Chain: []Proc{{Kind: "hp", Freq: 7000}, {Kind: "decay", Tau: 0.012}}}}})
	cfg.BaseVoices = []*VoiceDef{
		{Name: "lead", Wave: "saw", Cutoff: 2500, Q: 0.5, Attack: 0.005, Decay: 0.1, Sustain: 0.7, Release: 0.08},
		{Name: "bass", Wave: "saw", Cutoff: 600, Q: 0.7, Attack: 0.005, Decay: 0.1, Sustain: 0.8, Release: 0.06},
	}
	cfg.BasePatterns = []*Pattern{
		{Name: "beat", Steps: 16, Tracks: []Track{
			{Instr: InstrKick, Events: []Step{{Pos: 0, Vel: 1}, {Pos: 4, Vel: 1}, {Pos: 8, Vel: 1}, {Pos: 12, Vel: 1}}},
			{Instr: InstrSnare, Events: []Step{{Pos: 4, Vel: 0.9}, {Pos: 12, Vel: 0.9}}},
			{Instr: InstrHihat, Events: []Step{{Pos: 2, Vel: 0.6}, {Pos: 6, Vel: 0.6}, {Pos: 10, Vel: 0.6}, {Pos: 14, Vel: 0.6}}}}},
		{Name: "tune", Steps: 16, Tracks: []Track{{Instr: InstrLead, Events: []Step{
			{Pos: 0, Vel: 0.9, Deg: 0, Oct: 2, Dur: 2}, {Pos: 2, Vel: 0.8, Deg: 1, Oct: 2, Dur: 2}, {Pos: 4, Vel: 0.8, Deg: 2, Oct: 2, Dur: 2}, {Pos: 6, Vel: 0.8, Deg: 3, Oct: 2, Dur: 2},
			{Pos: 8, Vel: 0.9, Deg: 4, Oct: 2, Dur: 2}, {Pos: 10, Vel: 0.8, Deg: 5, Oct: 2, Dur: 2}, {Pos: 12, Vel: 0.8, Deg: 6, Oct: 2, Dur: 2}, {Pos: 14, Vel: 0.9, Deg: 0, Oct: 3, Dur: 2}}}}},
		{Name: "low", Steps: 16, Tracks: []Track{{Instr: InstrBass, Events: []Step{
			{Pos: 0, Vel: 0.9, Deg: 0, Oct: 0, Dur: 4}, {Pos: 8, Vel: 0.9, Deg: 4, Oct: 0, Dur: 4}}}}},
	}
	ae, err := NewAudioEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := ae.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(ae.Stop)
	return ae, [MusicSlots]PatternID{PatternIDByName("beat"), PatternIDByName("tune"), PatternSilence, PatternIDByName("low")}
}

// TestRipHearsTheLoop is the rule a rip answers to: played the engine's own music,
// it finds the tempo, the bars, the key, the drums on their steps and most of the
// melody and bass notes on theirs, graded against the rip a perfect listener would
// make of the same patterns, which grades itself perfect.
func TestRipHearsTheLoop(t *testing.T) {
	const bpm, bars = 140, 8
	ae, slots := startLoopEngine(t)
	buf, err := ae.RenderMusic(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := ExpectedRip(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil)
	if self := CompareRips(want, want); self.Total != 1 {
		t.Fatalf("the truth grades itself %+v", self)
	}
	r, err := RipSamples(buf, AudioSampleRate, DefaultRipParams())
	if err != nil {
		t.Fatal(err)
	}
	if r.Root%12 != DefaultRootNote%12 || r.Scale != ScalePhrygian {
		t.Errorf("key root %d scale %d, want E phrygian", r.Root, r.Scale)
	}
	score := CompareRips(r, want)
	t.Logf("bpm %d bars %d downbeat %.2fs; score %+v", r.BPM, len(r.Bars), r.Downbeat, score)
	// on a pinned grid the decoders of tempo and bar line are skipped and the
	// rest reads the same
	pinned, err := Ripper{Params: DefaultRipParams(), Grid: &RipGrid{BPM: bpm}}.Samples(buf, AudioSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	if ps := CompareRips(pinned, want); pinned.BPM != bpm || pinned.Downbeat != 0 || len(pinned.Bars) != bars || ps.Total < score.Total-0.01 {
		t.Errorf("pinned: bpm %d downbeat %.2f bars %d score %+v", pinned.BPM, pinned.Downbeat, len(pinned.Bars), ps)
	}
	// the tempo alone pinned: the beats tracked at it and the bar line decoded,
	// so the loop reads as it does free; at another tempo it reads on that grid
	tempoed, err := Ripper{Params: DefaultRipParams(), Grid: &RipGrid{BPM: bpm, Downbeat: -1}}.Samples(buf, AudioSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	other, err := Ripper{Params: DefaultRipParams(), Grid: &RipGrid{BPM: 100, Downbeat: -1}}.Samples(buf, AudioSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	if ts := CompareRips(tempoed, want); tempoed.BPM != bpm || ts.Total < score.Total-0.05 || other.BPM != 100 || len(other.Bars) < bars*100/bpm-1 || len(other.Bars) > bars*100/bpm+1 {
		t.Errorf("tempo pinned: bpm %d score %+v; at 100: bpm %d bars %d", tempoed.BPM, ts, other.BPM, len(other.Bars))
	}
	if r.Meter != (Meter{}) || score.Tempo < 1 || score.Bars < 0.85 || score.Drums < 0.9 || score.Melody < 0.8 || score.Bass < 0.8 {
		t.Errorf("in %s, score %+v", r.Meter, score)
	}
	if n := len(r.Patterns("loop", 4)); n < 3 {
		t.Errorf("%d patterns written, want a beat, a melody and a bass at least", n)
	}
	if len(r.Notes) < 16 || r.Notes[0].Start > r.Notes[len(r.Notes)-1].Start {
		t.Errorf("%d notes in the transcript, in order %v", len(r.Notes), len(r.Notes) > 1)
	}
	// a timeline that empties every slot after the first bar falls near silent
	// there, and plays on when the third bar fills them again
	timeline := [][MusicSlots]PatternID{slots, {}, slots}
	tl, err := ae.RenderTimeline(timeline, bpm, Meter{}, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	n := SamplesPerBar(bpm, Meter{})
	full, empty, again := energy(tl[:n]), energy(tl[n:2*n]), energy(tl[2*n:])
	if empty > full/10 || again < full/2 {
		t.Errorf("timeline bars carry %g, %g and %g", full, empty, again)
	}
}

// TestRipHearsTheMeter: a waltz rips in three and a jig, its beats dotted
// quarters, in 6/8 at three halves of the tempo they are tracked at, each on
// the bar lines a perfect listener's rip of the same patterns holds
func TestRipHearsTheMeter(t *testing.T) {
	ae, _ := startLoopEngine(t)
	define := func(p *Pattern) PatternID {
		id, err := ae.DefinePattern(p)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	hit := func(vel float64, pos ...int) []Step {
		var s []Step
		for _, p := range pos {
			s = append(s, Step{Pos: p, Vel: vel})
		}
		return s
	}
	three, six := Meter{3, 4}, Meter{6, 8}
	waltz := [MusicSlots]PatternID{
		define(&Pattern{Name: "waltz", Steps: 12, Meter: three, Tracks: []Track{
			{Instr: InstrKick, Events: hit(1, 0)}, {Instr: InstrSnare, Events: hit(0.8, 4, 8)}, {Instr: InstrHihat, Events: hit(0.5, 2, 6, 10)}}}),
		define(&Pattern{Name: "waltz_tune", Steps: 12, Meter: three, Tracks: []Track{{Instr: InstrLead, Events: []Step{
			{Pos: 0, Vel: 0.9, Oct: 2, Dur: 4}, {Pos: 4, Vel: 0.8, Deg: 2, Oct: 2, Dur: 2}, {Pos: 6, Vel: 0.8, Deg: 4, Oct: 2, Dur: 2}, {Pos: 8, Vel: 0.8, Deg: 3, Oct: 2, Dur: 4}}}}}),
		PatternSilence,
		define(&Pattern{Name: "waltz_low", Steps: 12, Meter: three, Tracks: []Track{{Instr: InstrBass, Events: []Step{{Pos: 0, Vel: 0.9, Dur: 8}}}}}),
	}
	jig := [MusicSlots]PatternID{
		define(&Pattern{Name: "jig", Steps: 12, Meter: six, Tracks: []Track{
			{Instr: InstrKick, Events: hit(1, 0)}, {Instr: InstrSnare, Events: hit(0.9, 6)}, {Instr: InstrHihat, Events: hit(0.5, 0, 2, 4, 6, 8, 10)}}}),
		define(&Pattern{Name: "jig_tune", Steps: 12, Meter: six, Tracks: []Track{{Instr: InstrLead, Events: []Step{
			{Pos: 0, Vel: 0.9, Oct: 2, Dur: 2}, {Pos: 2, Vel: 0.7, Deg: 1, Oct: 2, Dur: 2}, {Pos: 4, Vel: 0.7, Deg: 2, Oct: 2, Dur: 2}, {Pos: 6, Vel: 0.9, Deg: 4, Oct: 2, Dur: 4}, {Pos: 10, Vel: 0.7, Deg: 2, Oct: 2, Dur: 2}}}}}),
		PatternSilence,
		define(&Pattern{Name: "jig_low", Steps: 12, Meter: six, Tracks: []Track{{Instr: InstrBass, Events: []Step{{Pos: 0, Vel: 0.9, Dur: 6}, {Pos: 6, Vel: 0.8, Deg: 4, Dur: 6}}}}}),
	}
	const bars = 16
	for _, c := range []struct {
		slots [MusicSlots]PatternID
		bpm   int
		m     Meter
	}{{waltz, 130, three}, {jig, 150, six}} {
		buf, err := ae.RenderMusic(c.slots, c.bpm, c.m, bars, DefaultRootNote, ScalePhrygian, nil, 1)
		if err != nil {
			t.Fatal(err)
		}
		r, err := RipSamples(buf, AudioSampleRate, DefaultRipParams())
		if err != nil {
			t.Fatal(err)
		}
		score := CompareRips(r, ExpectedRip(c.slots, c.bpm, c.m, bars, DefaultRootNote, ScalePhrygian, nil))
		if r.Meter != c.m || r.BPM != c.bpm || score.Bars < 0.85 || score.Drums < 0.8 {
			t.Errorf("%s at %d: ripped %s at %d bpm, %d bar(s), score %+v", c.m, c.bpm, r.Meter, r.BPM, len(r.Bars), score)
		}
	}
}

// TestRipHearsABarByItsRepetition: a waltz whose kick and bass strike every
// quarter gives its bar lines no cue over 4/4's, and rips in three by how its
// tune repeats at its own bars
func TestRipHearsABarByItsRepetition(t *testing.T) {
	ae, _ := startLoopEngine(t)
	define := func(p *Pattern) PatternID {
		id, err := ae.DefinePattern(p)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	three := Meter{3, 4}
	slots := [MusicSlots]PatternID{
		define(&Pattern{Name: "even", Steps: 12, Meter: three, Tracks: []Track{
			{Instr: InstrKick, Events: []Step{{Pos: 0, Vel: 1}, {Pos: 4, Vel: 1}, {Pos: 8, Vel: 1}}},
			{Instr: InstrHihat, Events: []Step{{Pos: 2, Vel: 0.5}, {Pos: 6, Vel: 0.5}, {Pos: 10, Vel: 0.5}}}}}),
		define(&Pattern{Name: "even_tune", Steps: 24, Meter: three, Tracks: []Track{{Instr: InstrLead, Events: []Step{
			{Pos: 0, Vel: 0.8, Oct: 2, Dur: 4}, {Pos: 4, Vel: 0.8, Deg: 2, Oct: 2, Dur: 2}, {Pos: 6, Vel: 0.8, Deg: 4, Oct: 2, Dur: 2}, {Pos: 8, Vel: 0.8, Deg: 2, Oct: 2, Dur: 4},
			{Pos: 12, Vel: 0.8, Deg: 3, Oct: 2, Dur: 4}, {Pos: 16, Vel: 0.8, Deg: 1, Oct: 2, Dur: 2}, {Pos: 18, Vel: 0.8, Deg: 2, Oct: 2, Dur: 2}, {Pos: 20, Vel: 0.8, Deg: 1, Oct: 2, Dur: 4}}}}}),
		PatternSilence,
		define(&Pattern{Name: "even_low", Steps: 12, Meter: three, Tracks: []Track{{Instr: InstrBass, Events: []Step{
			{Pos: 0, Vel: 0.9, Dur: 4}, {Pos: 4, Vel: 0.9, Dur: 4}, {Pos: 8, Vel: 0.9, Dur: 4}}}}}),
	}
	buf, err := ae.RenderMusic(slots, 96, three, 16, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	r, err := RipSamples(buf, AudioSampleRate, DefaultRipParams())
	if err != nil {
		t.Fatal(err)
	}
	if r.Meter != three || r.BPM != 96 {
		t.Errorf("ripped %s at %d bpm, %d bar(s), want 3/4 at 96", r.Meter, r.BPM, len(r.Bars))
	}
}

// TestASongInThreeEightChangesPatternsOnItsBarLines: a song in 3/8 swaps its
// melody every six steps, past the 64 steps the step counter used to wrap
// at, each bar's note sounding on its own bar line at its own pattern's pitch
func TestASongInThreeEightChangesPatternsOnItsBarLines(t *testing.T) {
	ae, _ := startLoopEngine(t)
	m := Meter{3, 8}
	var ids [2]PatternID
	for i, deg := range []int{0, 4} {
		id, err := ae.DefinePattern(&Pattern{Name: fmt.Sprint("note", deg), Steps: 6, Meter: m, Tracks: []Track{{Instr: InstrLead, Events: []Step{{Pos: 0, Vel: 0.9, Deg: deg, Oct: 2, Dur: 3}}}}})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	order := "011010011100101101110010" // 24 bars, 144 steps
	timeline := make([][MusicSlots]PatternID, len(order))
	for b, c := range order {
		timeline[b][1] = ids[c-'0']
	}
	const bpm = 120
	buf, err := ae.RenderTimeline(timeline, bpm, m, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	r, err := RipSamples(buf, AudioSampleRate, DefaultRipParams())
	if err != nil {
		t.Fatal(err)
	}
	bar := float64(SamplesPerBar(bpm, m)) / AudioSampleRate
	pitch := [2]int{-1, -1}
	for b, c := range order {
		i := slices.IndexFunc(r.Notes, func(n RipNote) bool { return !n.Bass && math.Abs(n.Start-float64(b)*bar) < 0.04 })
		if i < 0 {
			t.Fatalf("bar %d: no note within 40 ms of its line at %.2fs", b, float64(b)*bar)
		}
		if k := c - '0'; pitch[k] < 0 {
			pitch[k] = r.Notes[i].MIDI
		} else if r.Notes[i].MIDI != pitch[k] {
			t.Errorf("bar %d plays %d, its pattern played %d before", b, r.Notes[i].MIDI, pitch[k])
		}
	}
	melody := slices.DeleteFunc(slices.Clone(r.Notes), func(n RipNote) bool { return n.Bass })
	if pitch[1] <= pitch[0] || len(melody) != len(order) {
		t.Errorf("pitches %v, %d melody notes for %d bars", pitch, len(melody), len(order))
	}
}

func energy(x []float64) float64 {
	var e float64
	for _, v := range x {
		e += v * v
	}
	return e / float64(max(len(x), 1))
}

// TestRipNoteBrightIsTheFundamentalsShare: the loop's melody played on a sine
// reads near one and brighter than on a saw, whose partials share the sum. Its
// root and fifth sound two octaves over the bass's, the bass's fourth partial
// covering their fundamentals, and are unmeasured rather than read through it;
// every other melody-register note is measured, and no bass note.
func TestRipNoteBrightIsTheFundamentalsShare(t *testing.T) {
	const bpm, bars = 140, 4
	ae, slots := startLoopEngine(t)
	covered := map[int]bool{DefaultRootNote + 24: true, StepPitch(DefaultRootNote, ScalePhrygian, 4, 0, 0) + 24: true}
	bright := func(wave string) float64 {
		if _, err := ae.DefineVoice(&VoiceDef{Name: "lead", Wave: wave, Cutoff: 2500, Q: 0.5, Attack: 0.005, Decay: 0.1, Sustain: 0.7, Release: 0.08}); err != nil {
			t.Fatal(err)
		}
		buf, err := ae.RenderMusic(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
		if err != nil {
			t.Fatal(err)
		}
		r, err := Ripper{Params: DefaultRipParams(), Grid: &RipGrid{BPM: bpm}}.Samples(buf, AudioSampleRate)
		if err != nil {
			t.Fatal(err)
		}
		var sum float64
		var measured int
		for _, n := range r.Notes {
			unmeasured := n.Bright == RipUnmeasured
			if unmeasured != (n.Bass || covered[n.MIDI]) {
				t.Errorf("%s: MIDI %d at %.2fs, bass %v, reads %.2f", wave, n.MIDI, n.Start, n.Bass, n.Bright)
			}
			if !unmeasured {
				sum, measured = sum+n.Bright, measured+1
			}
		}
		if measured == 0 {
			t.Fatalf("%s: no note measured", wave)
		}
		return sum / float64(measured)
	}
	saw, sine := bright("saw"), bright("sine")
	t.Logf("mean bright: saw %.2f, sine %.2f", saw, sine)
	if sine < 0.9 || saw > sine-0.2 {
		t.Errorf("saw %.2f, sine %.2f", saw, sine)
	}
}

// TestNoteRenderAndDistances: a rendered note sounds and dies, mel frames of the
// same note are no distance apart and of another voice some, and bars are as far
// apart as the steps they do not share.
func TestNoteRenderAndDistances(t *testing.T) {
	saw := &VoiceDef{Name: "a", Wave: "saw", Cutoff: 2000, Q: 0.5, Attack: 0.005, Decay: 0.1, Sustain: 0.7, Release: 0.1}
	sine := &VoiceDef{Name: "b", Wave: "sine", Attack: 0.005, Decay: 0.1, Sustain: 0.7, Release: 0.1}
	note := RenderNote(saw, 60, 0.8, 0.3, 0.5)
	if len(note) < int(0.3*AudioSampleRate) || len(note) > int(0.8*AudioSampleRate) || energy(note[:1000]) == 0 {
		t.Fatalf("note of %d samples, head energy %g", len(note), energy(note[:1000]))
	}
	if tail := note[len(note)-100:]; energy(tail) > energy(note[:4410])/100 {
		t.Errorf("the note did not die away: %g vs %g", energy(tail), energy(note[:4410]))
	}
	a := MelFrames(note, AudioSampleRate)
	b := MelFrames(RenderNote(sine, 60, 0.8, 0.3, 0.5), AudioSampleRate)
	if MelDistance(a, a) != 0 || MelDistance(a, b) <= 0 {
		t.Errorf("distances %g and %g", MelDistance(a, a), MelDistance(a, b))
	}
	x := RipBar{Melody: []Step{{Pos: 0, Deg: 0, Oct: 2}, {Pos: 4, Deg: 2, Oct: 2}}}
	x.Drums[InstrKick] = []Step{{Pos: 0}, {Pos: 8}}
	y := x
	y.Melody = []Step{{Pos: 0, Deg: 0, Oct: 2}, {Pos: 4, Deg: 3, Oct: 2}}
	if d := BarDistance(&x, &x); d != 0 {
		t.Errorf("a bar is %g from itself", d)
	}
	if d := BarDistance(&x, &y); math.Abs(d-0.4) > 1e-9 {
		t.Errorf("one note of four changed is %g apart, want 0.4", d)
	}
	if d := BarDistance(&x, &RipBar{}); d != 1 {
		t.Errorf("a bar is %g from an empty one", d)
	}
}

// TestRipHearsASecondVoice: a sustained line under the melody, in its register,
// is heard by the second pass and lost without it, and the first pass's own
// notes are not traded for it.
func TestRipHearsASecondVoice(t *testing.T) {
	const bpm, bars = 140, 8
	ae, slots := startLoopEngine(t)
	pad, err := ae.DefinePattern(&Pattern{Name: "pad", Steps: 16, Role: RoleFill, Tracks: []Track{{Instr: InstrLead, Events: []Step{
		{Pos: 0, Vel: 0.7, Deg: 2, Oct: 1, Dur: 8}, {Pos: 8, Vel: 0.7, Deg: 4, Oct: 1, Dur: 8}}}}})
	if err != nil {
		t.Fatal(err)
	}
	slots[2] = pad
	buf, err := ae.RenderMusic(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := ExpectedRip(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil)
	// on the render's own grid: the pad's change on the third beat moves the
	// bar-line decoder, which is not the rule under test
	one := DefaultRipParams()
	one.SecondVoice = 0
	solo, err := Ripper{Params: one, Grid: &RipGrid{BPM: bpm}}.Samples(buf, AudioSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	both, err := Ripper{Params: DefaultRipParams(), Grid: &RipGrid{BPM: bpm}}.Samples(buf, AudioSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	a, b := CompareRips(solo, want), CompareRips(both, want)
	// the pad's notes found: its two a bar, by step and absolute pitch, since
	// the pad moves the key the rip reads its degrees in
	padNotes := func(r *Rip) int {
		n := 0
		for _, bar := range r.Bars {
			for _, st := range append(slices.Clone(bar.Melody), bar.Under...) {
				midi := StepPitch(r.Root, r.Scale, st.Deg, st.Oct, st.Acc)
				if st.Pos == 0 && midi == DefaultRootNote+15 || st.Pos == 8 && midi == DefaultRootNote+19 {
					n++
				}
			}
		}
		return n
	}
	t.Logf("one voice %+v, %d pad notes; two voices %+v, %d pad notes of %d", a, padNotes(solo), b, padNotes(both), 2*bars)
	// the second line's fragments may cost the melody a little, not more
	if padNotes(both) <= padNotes(solo) || b.Melody < a.Melody-0.05 {
		t.Errorf("the second pass found %d pad notes against %d, melody %.2f against %.2f", padNotes(both), padNotes(solo), b.Melody, a.Melody)
	}
	// the second line is a track of its own, through the lead until a voice is fitted
	under := 0
	for _, bar := range both.Bars {
		under += len(bar.Under)
	}
	pats := both.Patterns("x", 8)
	i := slices.IndexFunc(pats, func(p *PatternDef) bool { return p.Name == "x_melody_1" })
	if under == 0 || i < 0 || len(pats[i].Track) != 2 || pats[i].Track[1].Instr != "lead" {
		t.Fatalf("%d under notes; melody pattern tracks %v", under, pats)
	}
	both.UnderVoice = &VoiceDef{Name: "u", Wave: "sine"}
	if vs := both.Voices("x"); len(vs) != 1 || vs[0].Name != "x_under_voice" || both.Patterns("x", 8)[i].Track[1].Instr != "x_under_voice" {
		t.Errorf("the under voice names %v", vs)
	}
}

// twoLines renders two lines in the melody register on one voice, the lead's
// events in the melody slot and the counter's in the free slot, and rips the
// render with the lead rule given; it returns the rip and the two pitch sets,
// which are disjoint, so a step tells its line by pitch.
func twoLines(t *testing.T, leadEvs, counterEvs []Step, lead LeadRule) (*Rip, map[int]bool, map[int]bool) {
	t.Helper()
	const bpm, bars = 120, 8
	ae, slots := startLoopEngine(t)
	l, err := ae.DefinePattern(&Pattern{Name: "lead", Steps: 32, Role: RoleMelody, Tracks: []Track{{Instr: InstrLead, Events: leadEvs}}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := ae.DefinePattern(&Pattern{Name: "counter", Steps: 32, Role: RoleFill, Tracks: []Track{{Instr: InstrLead, Events: counterEvs}}})
	if err != nil {
		t.Fatal(err)
	}
	slots[1], slots[2] = l, c
	buf, err := ae.RenderMusic(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Ripper{Params: DefaultRipParams(), Grid: &RipGrid{BPM: bpm}, Lead: lead}.Samples(buf, AudioSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	pitches := func(evs []Step) map[int]bool {
		m := map[int]bool{}
		for _, e := range evs {
			m[StepPitch(DefaultRootNote, ScalePhrygian, e.Deg, e.Oct, 0)] = true
		}
		return m
	}
	return r, pitches(leadEvs), pitches(counterEvs)
}

// linesHeard counts the lead's and the counter's pitches in the melody line
// and in the under line of a rip
func linesHeard(r *Rip, leadSet, counterSet map[int]bool) (leadIn, counterIn [2]int) {
	for _, bar := range r.Bars {
		for i, line := range [2][]Step{bar.Melody, bar.Under} {
			for _, st := range line {
				switch m := StepPitch(r.Root, r.Scale, st.Deg, st.Oct, st.Acc); {
				case leadSet[m]:
					leadIn[i]++
				case counterSet[m]:
					counterIn[i]++
				}
			}
		}
	}
	return
}

// TestRipKeepsTheLeadThroughACrossing: a loud lead in quarters and a quieter
// counter line in eighths on one voice, a fourth under the lead one bar and a
// fifth over it the next. Told the lead is the louder, the rip keeps each line's
// notes on its side through the crossing, where a line told by pitch order or by
// the louder frame changes hands.
func TestRipKeepsTheLeadThroughACrossing(t *testing.T) {
	const bars = 8
	var leadEvs, counterEvs []Step
	for i, d := range [...]int{4, 5, 6, 5, 4, 5, 6, 5} {
		leadEvs = append(leadEvs, Step{Pos: 4 * i, Vel: 1, Deg: d, Oct: 2, Dur: 4})
	}
	for i, d := range [...]int{1, 2, 3, 2, 1, 2, 3, 2, 1, 2, 3, 2, 1, 2, 3, 2} {
		counterEvs = append(counterEvs, Step{Pos: 2 * i, Vel: 0.65, Deg: d, Oct: 2 + i/8, Dur: 2})
	}
	r, leadSet, counterSet := twoLines(t, leadEvs, counterEvs, LeadLoud)
	leadIn, counterIn := linesHeard(r, leadSet, counterSet)
	t.Logf("melody line: %d lead notes, %d counter notes; under line: %d lead, %d counter; %d lead and %d counter notes played", leadIn[0], counterIn[0], leadIn[1], counterIn[1], 4*bars, 8*bars)
	if leadIn[0] < 3*bars || counterIn[0] > bars || counterIn[1] < 3*bars {
		t.Errorf("the lead did not hold its line: melody %d lead %d counter, under %d lead %d counter", leadIn[0], counterIn[0], leadIn[1], counterIn[1])
	}
}

// TestRipTellsTheLeadByItsCharacter: a softer lead in eighths under a louder
// counter line in half notes, a fourth under the lead one bar and a fifth over
// it the next, on one voice. The decoder follows the louder line, which is the
// counter here, so the notes are parted into lines by what each keeps, its
// pitch, its lengths and its strength, and the busier line is the melody.
func TestRipTellsTheLeadByItsCharacter(t *testing.T) {
	const bars = 8
	var leadEvs, counterEvs []Step
	for i, d := range [...]int{4, 5, 6, 5, 4, 5, 6, 5, 4, 5, 6, 5, 4, 5, 6, 5} {
		leadEvs = append(leadEvs, Step{Pos: 2 * i, Vel: 0.6, Deg: d, Oct: 2, Dur: 2})
	}
	for i, d := range [...]int{1, 2, 1, 2} {
		counterEvs = append(counterEvs, Step{Pos: 8 * i, Vel: 1, Deg: d, Oct: 2 + i/2, Dur: 8})
	}
	r, leadSet, counterSet := twoLines(t, leadEvs, counterEvs, LeadBusy)
	leadIn, counterIn := linesHeard(r, leadSet, counterSet)
	t.Logf("melody line: %d lead notes, %d counter notes; under line: %d lead, %d counter; %d lead and %d counter notes played", leadIn[0], counterIn[0], leadIn[1], counterIn[1], 8*bars, 2*bars)
	if leadIn[0] < 6*bars || counterIn[0] > bars || counterIn[1] < bars {
		t.Errorf("the softer lead did not hold its line: melody %d lead %d counter, under %d lead %d counter", leadIn[0], counterIn[0], leadIn[1], counterIn[1])
	}
}

// TestATieInThePartingGoesToTheEarlierOtherLine: after a dyad, a note three
// semitones from one and six from the other joins either at one cost, the
// nearer dearer by a change of line, last or under a note it sounds with. Read
// from the last note back it stays off the line whose last note came earlier,
// every run, though a walk of the states as made meets the rival first.
func TestATieInThePartingGoesToTheEarlierOtherLine(t *testing.T) {
	note := func(start, end, midi int) ripNote { return ripNote{start: start, end: end, midi: midi, strength: 0.5} }
	held, sixth, third := note(0, 16, 60), note(12, 16, 69), note(16, 80, 63)
	for _, notes := range [][]ripNote{{held, sixth, third}, {held, sixth, third, note(17, 81, 48)}} {
		for range 20 {
			line := map[int]bool{}
			for _, n := range streams(slices.Clone(notes), RipFrameSec, LeadBusy) {
				line[n.midi] = n.second
			}
			if line[63] != line[69] || line[60] == line[69] || len(notes) == 4 && line[48] != line[60] {
				t.Fatalf("%d notes: the line of each pitch, second or not: %v", len(notes), line)
			}
		}
	}
}

// TestRipKeepsABassLineThatClimbs: a bass line on F3 and G3, above the E3 the
// bass register's rows end at, stays the bass line, heard by the bass decoder
// where it reaches over the melody's rows; and a melody that dips to G3 over a
// low bass keeps its note, since the note continues the melody, not the bass.
func TestRipKeepsABassLineThatClimbs(t *testing.T) {
	const bpm, bars = 120, 4
	f3, g3 := StepPitch(DefaultRootNote, ScalePhrygian, 1, 1, 0), StepPitch(DefaultRootNote, ScalePhrygian, 2, 1, 0)
	count := func(r *Rip) (inBass, inMelody int) {
		for _, bar := range r.Bars {
			for _, st := range bar.Bass {
				if m := StepPitch(r.Root, r.Scale, st.Deg, st.Oct, st.Acc); m == f3 || m == g3 {
					inBass++
				}
			}
			for _, st := range append(slices.Clone(bar.Melody), bar.Under...) {
				if m := StepPitch(r.Root, r.Scale, st.Deg, st.Oct, st.Acc); m == f3 || m == g3 {
					inMelody++
				}
			}
		}
		return
	}
	ae, slots := startLoopEngine(t)
	high, err := ae.DefinePattern(&Pattern{Name: "high", Steps: 16, Role: RoleBass, Tracks: []Track{{Instr: InstrBass, Events: []Step{
		{Pos: 0, Vel: 0.9, Deg: 1, Oct: 1, Dur: 4}, {Pos: 8, Vel: 0.9, Deg: 2, Oct: 1, Dur: 4}}}}})
	if err != nil {
		t.Fatal(err)
	}
	// a melody that dips: the tune's run with its third note a G3, over the low bass
	dip, err := ae.DefinePattern(&Pattern{Name: "dip", Steps: 16, Role: RoleMelody, Tracks: []Track{{Instr: InstrLead, Events: []Step{
		{Pos: 0, Vel: 0.9, Deg: 4, Oct: 1, Dur: 2}, {Pos: 2, Vel: 0.8, Deg: 3, Oct: 1, Dur: 2}, {Pos: 4, Vel: 0.9, Deg: 2, Oct: 1, Dur: 2}, {Pos: 6, Vel: 0.8, Deg: 3, Oct: 1, Dur: 2},
		{Pos: 8, Vel: 0.9, Deg: 4, Oct: 1, Dur: 2}, {Pos: 10, Vel: 0.8, Deg: 5, Oct: 1, Dur: 2}, {Pos: 12, Vel: 0.8, Deg: 6, Oct: 1, Dur: 2}, {Pos: 14, Vel: 0.9, Deg: 0, Oct: 2, Dur: 2}}}}})
	if err != nil {
		t.Fatal(err)
	}
	climb := slots
	climb[3] = high
	buf, err := ae.RenderMusic(climb, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Ripper{Params: DefaultRipParams(), Grid: &RipGrid{BPM: bpm}}.Samples(buf, AudioSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	if inBass, inMelody := count(r); inBass < bars || inMelody > 1 {
		t.Errorf("the climbing bass line: %d notes in the bass line, %d in the melody register, of %d played", inBass, inMelody, 2*bars)
	}
	dipped := slots
	dipped[1] = dip
	buf, err = ae.RenderMusic(dipped, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	r, err = Ripper{Params: DefaultRipParams(), Grid: &RipGrid{BPM: bpm}}.Samples(buf, AudioSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	if inBass, inMelody := count(r); inMelody < bars/2 || inBass > 1 {
		t.Errorf("the dipping melody: %d G3 notes in the melody register, %d in the bass line, of %d played", inMelody, inBass, bars)
	}
}

// TestAReleaseEndsANoteStillSounding: a front end that hears each pitch let go
// ends a note there though a pedal keeps it sounding, the note as hard as the front
// end heard it struck; while it rings on, only a strike over StrikeAgain starts another.
func TestAReleaseEndsANoteStillSounding(t *testing.T) {
	const frames = 200
	f := &RipFrames{Hop: RipFrameSec, Flux: make([]float64, frames), Salience: make([]float32, frames*RipPitches),
		Strike: make([]float32, frames*RipPitches), Release: make([]float32, frames*RipPitches), Velocity: make([]float32, frames*RipPitches)}
	at := func(t, midi int) int { return t*RipPitches + midi - RipMinMIDI }
	for t := 10; t < frames; t++ {
		f.Salience[at(t, 72)] = 0.9
	}
	f.Strike[at(10, 72)], f.Velocity[at(10, 72)], f.Release[at(120, 72)] = 0.99, 0.4, 0.95
	f.Strike[at(150, 72)], f.Strike[at(170, 72)] = 0.9, 0.99
	got := f.notes(RipMelodyLow, RipMaxMIDI, DefaultRipParams())
	if len(got) != 2 || got[0].start != 10 || got[0].end != 120 || got[0].vel != float64(float32(0.4)) || got[1].start != 170 {
		t.Errorf("notes %+v, want one from 10 to 120 at velocity 0.4 and one from 170", got)
	}
}

// TestStrikesStartNotesWhileOthersRing: under a pedal, three pitches struck in turn
// ring on together and one struck again while it rings is two notes. A strike whose
// pitch never sounds, a weaker one an octave over a note struck, one again within
// StrikeGap, and one of a ringing pitch under StrikeAgain or under a neighbour's are
// none; a bass strike two octaves under hides no melody note.
func TestStrikesStartNotesWhileOthersRing(t *testing.T) {
	const frames = 300
	f := &RipFrames{Hop: RipFrameSec, Flux: make([]float64, frames), Salience: make([]float32, frames*RipPitches), Strike: make([]float32, frames*RipPitches)}
	for _, n := range []struct {
		midi, at, end int
		strike        float32
	}{{72, 0, 250, 0.99}, {76, 40, 250, 0.99}, {79, 80, 250, 0.99}, {72, 150, 250, 0.99}, {84, 0, 100, 0.95}, {67, 200, 200, 0.99}, {76, 45, 45, 0.99},
		{74, 260, 266, 0.99}, {74, 268, 290, 0.99}, {79, 120, 120, 0.9}, {45, 220, 220, 0.99}, {69, 220, 240, 0.97},
		{79, 180, 180, 0.97}, {80, 180, 200, 0.99}} {
		f.Strike[n.at*RipPitches+n.midi-RipMinMIDI] = n.strike
		for t := n.at; t < n.end; t++ {
			f.Salience[t*RipPitches+n.midi-RipMinMIDI] = 0.9
		}
	}
	var got [][3]int
	for _, n := range f.notes(RipMelodyLow, RipMaxMIDI, DefaultRipParams()) {
		got = append(got, [3]int{n.midi, n.start, n.end})
	}
	want := [][3]int{{72, 0, 150}, {76, 40, 250}, {79, 80, 250}, {72, 150, 250}, {80, 180, 200}, {69, 220, 240}, {74, 260, 266}}
	if !slices.Equal(got, want) {
		t.Errorf("notes (midi, start, end) %v, want %v", got, want)
	}
}

// glideFrames is the frames a front end that hears each pitch's cents gives of one
// sound at at[t] cents from MIDI 0, none where it is 0: the nearest pitch's cell
// sounding, each cell within a semitone naming it, every reading to err cents
func glideFrames(at []float64, err float32) *RipFrames {
	f := &RipFrames{Hop: RipFrameSec, Flux: make([]float64, len(at))}
	for _, h := range [...]*[]float32{&f.Salience, &f.Strike, &f.Cents, &f.CentsError} {
		*h = make([]float32, len(at)*RipPitches)
	}
	for t, c := range at {
		row := t*RipPitches - RipMinMIDI
		for m := RipMinMIDI; m < RipMaxMIDI; m++ {
			f.CentsError[row+m] = err
		}
		if c == 0 {
			continue
		}
		near := int(math.Round(c / 100))
		f.Salience[row+near] = 0.9
		for m := near - 1; m <= near+1; m++ {
			if off := c - 100*float64(m); math.Abs(off) <= 100 {
				f.Cents[row+m] = float32(off)
			}
		}
	}
	return f
}

// sung is a sound's frequency a frame over n frames: level cents from frame from to
// frame to, then each further (level, to) reached in a straight line from the last
func sung(n, from int, level float64, to int, then ...float64) []float64 {
	at := make([]float64, n)
	for t := from; t < to; t++ {
		at[t] = level
	}
	for i := 0; i+1 < len(then); i += 2 {
		for t := to; t < int(then[i+1]); t++ {
			at[t] = level + (then[i]-level)*float64(t-to)/(then[i+1]-float64(to))
		}
		level, to = then[i], int(then[i+1])
	}
	return at
}

// heardNotes is the melody register's notes as strikes reads them: pitch, start, end
func heardNotes(f *RipFrames) (got [][3]int, notes []ripNote) {
	notes = f.strikes(RipBassTop, RipMaxMIDI, DefaultRipParams())
	for _, n := range notes {
		got = append(got, [3]int{n.midi, n.start, n.end})
	}
	return got, notes
}

// TestAGlideAcrossABoundaryIsOneBentNote: a note that glides a semitone up at its
// end is one note at the pitch it rested on, bent by what it moved from where it
// left, the cell it passed into struck or not, and the note surely struck where
// it lands is its own. Readings the front end is unsure of join nothing.
func TestAGlideAcrossABoundaryIsOneBentNote(t *testing.T) {
	at := sung(120, 10, 6000, 60, 6100, 80, 6100, 110)
	for _, struck := range []float32{0, 0.85} {
		f := glideFrames(at, 5)
		f.Strike[10*RipPitches+60-RipMinMIDI], f.Strike[70*RipPitches+61-RipMinMIDI], f.Strike[80*RipPitches+61-RipMinMIDI] = 0.99, struck, 0.99
		got, notes := heardNotes(f)
		if want := [][3]int{{60, 10, 80}, {61, 80, 110}}; !slices.Equal(got, want) {
			t.Fatalf("the cell passed into struck at %v: notes (midi, start, end) %v, want %v", struck, got, want)
		}
		if n := notes[0]; math.Abs(n.bend-0.95) > 0.05 || math.Abs(n.bendAt-50.0/70) > 0.05 || notes[1].bend != 0 {
			t.Errorf("the cell passed into struck at %v: bent %v from %v of its length, want a semitone from 0.71; the next bent %v", struck, n.bend, n.bendAt, notes[1].bend)
		}
		f = glideFrames(at, 60)
		f.Strike[10*RipPitches+60-RipMinMIDI], f.Strike[70*RipPitches+61-RipMinMIDI], f.Strike[80*RipPitches+61-RipMinMIDI] = 0.99, struck, 0.99
		got, notes = heardNotes(f)
		want := [][3]int{{60, 10, 70}, {61, 70, 80}, {61, 80, 110}}
		if struck == 0 {
			want = slices.Delete(want, 1, 2)
		}
		if !slices.Equal(got, want) || notes[0].bend != 0 {
			t.Errorf("readings unsure, the cell passed into struck at %v: notes %v, the first bent %v, want %v unbent", struck, got, notes[0].bend, want)
		}
	}
}

// TestAStepThatLandsAndStaysIsTwoNotes: a note struck softly a semitone over the
// last as it ends is its own, the last ending there though its cell sounds on,
// naming the new one; and a sound that slides a tone up with no strike and stays
// is two notes, the first bent by its slide.
func TestAStepThatLandsAndStaysIsTwoNotes(t *testing.T) {
	f := glideFrames(sung(120, 10, 6000, 50, 6100, 50, 6100, 100), 5)
	f.Strike[10*RipPitches+60-RipMinMIDI], f.Strike[50*RipPitches+61-RipMinMIDI] = 0.99, 0.9
	for u := 50; u < 59; u++ {
		f.Salience[u*RipPitches+60-RipMinMIDI] = 0.5
	}
	if got, _ := heardNotes(f); !slices.Equal(got, [][3]int{{60, 10, 50}, {61, 50, 100}}) {
		t.Errorf("a struck step: notes (midi, start, end) %v, want 60 to frame 50 and 61 from it", got)
	}
	f = glideFrames(sung(120, 10, 6000, 50, 6200, 56, 6200, 100), 5)
	f.Strike[10*RipPitches+60-RipMinMIDI] = 0.99
	got, notes := heardNotes(f)
	if !slices.Equal(got, [][3]int{{60, 10, 55}, {62, 55, 100}}) || notes[0].bend < 1 || notes[0].bendAt < 0.85 || notes[1].bend != 0 {
		t.Errorf("a slide that lands: notes %v, the first bent %v from %v, want 60 bent over a semitone late and 62 from frame 55", got, notes[0].bend, notes[0].bendAt)
	}
}

// TestAScoopTakesThePitchItSettlesOn: a note struck two semitones under the pitch
// it slides up to and rests on is one note of that pitch from its strike, unbent.
func TestAScoopTakesThePitchItSettlesOn(t *testing.T) {
	f := glideFrames(sung(120, 10, 5810, 10, 6000, 22, 6000, 80), 5)
	f.Strike[10*RipPitches+58-RipMinMIDI], f.Velocity = 0.99, make([]float32, 120*RipPitches)
	f.Velocity[10*RipPitches+58-RipMinMIDI] = 0.7
	got, notes := heardNotes(f)
	if !slices.Equal(got, [][3]int{{60, 10, 80}}) || notes[0].bend != 0 || notes[0].vel != float64(float32(0.7)) {
		t.Errorf("notes (midi, start, end) %v bent %v at velocity %v, want one of 60 from its strike, unbent, as hard as struck", got, notes[0].bend, notes[0].vel)
	}
}

// TestANoteStruckAgainWithItsFrequencyUnbrokenIsOne: a note that sways into the
// next cell and back, each cell struck as the sound enters it, is one note; a
// sure strike as it comes back starts another.
func TestANoteStruckAgainWithItsFrequencyUnbrokenIsOne(t *testing.T) {
	for _, again := range []float32{0.9, 0.99} {
		f := glideFrames(sung(120, 10, 6045, 30, 6062, 30, 6062, 40, 6045, 40, 6045, 80), 5)
		f.Strike[10*RipPitches+60-RipMinMIDI], f.Strike[30*RipPitches+61-RipMinMIDI], f.Strike[40*RipPitches+60-RipMinMIDI] = 0.99, 0.85, again
		want := [][3]int{{60, 10, 80}}
		if again > 0.95 {
			want = [][3]int{{60, 10, 40}, {60, 40, 80}}
		}
		if got, notes := heardNotes(f); !slices.Equal(got, want) || notes[0].bend != 0 {
			t.Errorf("struck again at %v: notes (midi, start, end) %v, the first bent %v, want %v unbent", again, got, notes[0].bend, want)
		}
	}
}

// TestACellNamingTheSoundBesideItIsNoNote: a sound near the edge of its cell
// lights the next one too, and a note struck softly there, naming the frequency
// the sound's own cell names, is no note.
func TestACellNamingTheSoundBesideItIsNoNote(t *testing.T) {
	f := glideFrames(sung(120, 10, 6048, 80), 5)
	f.Strike[10*RipPitches+60-RipMinMIDI], f.Strike[30*RipPitches+61-RipMinMIDI] = 0.99, 0.85
	for u := 30; u < 50; u++ {
		f.Salience[u*RipPitches+61-RipMinMIDI] = 0.85
	}
	if got, _ := heardNotes(f); !slices.Equal(got, [][3]int{{60, 10, 80}}) {
		t.Errorf("notes (midi, start, end) %v, want the one of 60", got)
	}
	for u := 30; u < 50; u++ { // its own frequency through its length
		f.Cents[u*RipPitches+61-RipMinMIDI] = 0
	}
	if got, _ := heardNotes(f); !slices.Equal(got, [][3]int{{60, 10, 80}, {61, 30, 50}}) {
		t.Errorf("a note of its own beside it: notes %v, want both", got)
	}
}

// TestAFrontEndsDoubtsAreNoDrums: a drum a front end is never sure of plays nothing,
// though its loudest doubt would clear a bar set against itself, and one it is sure
// of too rarely for the recording's length is no part; the analysis's strengths have
// no such scale, and keep their bar against their own loudest.
func TestAFrontEndsDoubtsAreNoDrums(t *testing.T) {
	const frames = 10000
	f := &RipFrames{Hop: RipFrameSec, Strike: make([]float32, frames*RipPitches)}
	for d := range f.Onset {
		f.Onset[d] = make([]float64, frames)
	}
	for i := 100; i < frames; i += 100 {
		f.Onset[0][i], f.Onset[1][i] = 0.05, 0.9
	}
	f.Onset[2][500] = 0.97
	if h := f.drumHits(DefaultRipParams()); len(h[0]) != 0 || len(h[1]) != 99 || len(h[2]) != 0 {
		t.Errorf("hits %d %d %d, want 0, 99 and 0", len(h[0]), len(h[1]), len(h[2]))
	}
	f.Strike = nil
	if h := f.drumHits(DefaultRipParams()); len(h[0]) != 99 || len(h[2]) != 1 {
		t.Errorf("the analysis's hits %d and %d, want 99 and 1", len(h[0]), len(h[2]))
	}
}

// TestANotesVelocityFollowsItsPassage: the same velocity heard in a loud passage
// and a soft one comes out apart by their level, and a passage far softer stops at the bound.
func TestANotesVelocityFollowsItsPassage(t *testing.T) {
	hop := float64(RipHop) / RipRate
	x := make([]float64, 6*RipRate)
	for i := range x {
		a := 0.5
		switch {
		case i >= 4*RipRate:
			a = 0.5 / 1000
		case i >= 2*RipRate:
			a = 0.5 / 4
		}
		x[i] = a * math.Sin(2*math.Pi*440*float64(i)/RipRate)
	}
	frame := func(sec float64) int { return int(sec / hop) }
	notes := []ripNote{{start: frame(1), vel: 0.2}, {start: frame(3), vel: 0.2}, {start: frame(5), vel: 0.2}}
	dynamics(x, hop, notes)
	if up := 20 * math.Log10(notes[0].vel/notes[1].vel); math.Abs(up-12) > 0.5 {
		t.Errorf("the loud passage's note %.1f dB over the soft one's, want 12", up)
	}
	if notes[1].vel != 0.2 || notes[2].vel != 0.1 {
		t.Errorf("velocities %v, want the median's kept and the far softer one at the floor", notes)
	}
}

// TestALowNoteNeedsASurerStrike: a strike a melody note would start on starts no
// low note, as every attack rings a body's or a room's low mode.
func TestALowNoteNeedsASurerStrike(t *testing.T) {
	const frames = 120
	f := &RipFrames{Hop: RipFrameSec, Flux: make([]float64, frames), Salience: make([]float32, frames*RipPitches), Strike: make([]float32, frames*RipPitches)}
	for _, midi := range []int{44, 64} {
		f.Strike[10*RipPitches+midi-RipMinMIDI] = 0.85
		for t := 10; t < 100; t++ {
			f.Salience[t*RipPitches+midi-RipMinMIDI] = 0.9
		}
	}
	p := DefaultRipParams()
	if low, high := f.strikes(RipMinMIDI, RipBassTop, p), f.strikes(RipBassTop, RipMaxMIDI, p); len(low) != 0 || len(high) != 1 {
		t.Errorf("%d low notes and %d high, want none and one", len(low), len(high))
	}
}

// TestLowNotesAreReadByStrikes: two low notes struck together both sound, as hard as
// the front end heard them, and the bass line's own note stands where no strike sounds.
func TestLowNotesAreReadByStrikes(t *testing.T) {
	const frames = 300
	f := &RipFrames{Hop: RipFrameSec, Flux: make([]float64, frames), Salience: make([]float32, frames*RipPitches),
		Strike: make([]float32, frames*RipPitches), Velocity: make([]float32, frames*RipPitches)}
	at := func(t, midi int) int { return t*RipPitches + midi - RipMinMIDI }
	x := make([]float64, frames*RipHop)
	for _, n := range []struct {
		midi, from, to int
		strike         float32
	}{{40, 10, 100, 0.99}, {47, 10, 100, 0.99}, {36, 150, 200, 0}} {
		f.Strike[at(n.from, n.midi)] = n.strike
		for t := n.from; t < n.to; t++ {
			f.Salience[at(t, n.midi)] = 0.9
			f.Velocity[at(t, n.midi)] = 0.4
		}
		for i := n.from * RipHop; i < n.to*RipHop; i++ {
			x[i] += math.Sin(2 * math.Pi * 440 * math.Pow(2, float64(n.midi-69)/12) * float64(i) / RipRate)
		}
	}
	bass, melody := f.lines(x, DefaultRipParams())
	var got []int
	for _, n := range bass {
		got = append(got, n.midi)
		if n.vel != float64(float32(0.4)) {
			t.Errorf("note %d at velocity %v, want the 0.4 heard", n.midi, n.vel)
		}
	}
	if want := []int{40, 47, 36}; !slices.Equal(got, want) || len(melody) != 0 {
		t.Errorf("bass %v and %d melody notes, want %v and none", got, len(melody), want)
	}
}

// TestALowNoteNeedsItsOwnPitch: a low strike stands where its pitch sounds and goes
// where only the note an octave over it does, whose partials it shares.
func TestALowNoteNeedsItsOwnPitch(t *testing.T) {
	x := make([]float64, RipRate)
	for i := range x {
		x[i] = math.Sin(2 * math.Pi * 220 * float64(i) / RipRate)
	}
	if real, shadow := ownPitch(x, ripNote{midi: 57}, RipFrameSec), ownPitch(x, ripNote{midi: 45}, RipFrameSec); real < ripOwnPitch || shadow >= ripOwnPitch {
		t.Errorf("its own pitch %.1f dB, its shadow %.1f dB, against a bar of %v", real, shadow, float64(ripOwnPitch))
	}
}

// TestASteadyToneIsNoBassNote: a mains hum never rises over its own steady level
// and goes, while a bass note a phone has thinned to its upper partials rises at
// them and stands.
func TestASteadyToneIsNoBassNote(t *testing.T) {
	x := make([]float64, 4*RipRate)
	for i := range x {
		s := float64(i) / RipRate
		x[i] = 0.05 * math.Sin(2*math.Pi*50*s)
		if s >= 2 {
			x[i] += 0.3 * math.Exp(-(s-2)/0.6) * (math.Sin(2*math.Pi*2*65.41*s) + math.Sin(2*math.Pi*3*65.41*s))
		}
	}
	steady := map[[2]int]ripSteady{}
	hum := steadyRise(x, ripNote{midi: 31, start: RipRate / RipHop}, RipFrameSec, steady)
	note := steadyRise(x, ripNote{midi: 36, start: 2 * RipRate / RipHop}, RipFrameSec, steady)
	if hum >= ripSteadyRise || note < ripSteadyRise {
		t.Errorf("the hum rises %.1f dB, the note %.1f dB, against a bar of %v", hum, note, float64(ripSteadyRise))
	}
}

// TestARipsVoiceLastsItsLongNotes: heads' genes play on a modal bank, with one
// oscillator, whose mean of two would beat, and a fade that keeps a line's long notes
// within 12 dB to their end.
func TestARipsVoiceLastsItsLongNotes(t *testing.T) {
	notes := make([]ripNote, 10)
	for i := range notes {
		notes[i] = ripNote{start: 200 * i, end: 200*i + 100, midi: 60, voice: make([]float64, VoiceGeneCount)}
	}
	v := voiceOf(notes)
	long := 100 * float64(RipHop) / float64(RipRate)
	tau := v.DecayTime * math.Pow(keyRefHz/(440*math.Pow(2, -9.0/12)), v.DecayTrack)
	if v.Wave != "modal" || v.Unison != 1 || tau < long/1.45 {
		t.Errorf("wave %s, unison %d, the fade's time %.3f s for notes of %.3f s", v.Wave, v.Unison, tau, long)
	}
}

// TestANoteCarriesTheVoiceItsPitchWasTold: a note's voice is its pitch's say on
// the frames it sounds and is told, each by its salience there, in every cell a
// note that moved sounded in; its family is named only past the front end's
// measure, and a note told on no frame carries none.
func TestANoteCarriesTheVoiceItsPitchWasTold(t *testing.T) {
	const n = 60
	f := &RipFrames{Hop: RipFrameSec, Flux: make([]float64, n), Salience: make([]float32, n*RipPitches),
		VoiceAt: slices.Repeat([]uint8{255}, n*RipVoiceCells), Voices: make([]uint8, n*RipVoiceCells*RipVoiceSize)}
	names, waves := len(RipFamilies)-1, len(VoiceWaves)
	// tell says at frames a to b, in slot k of those told, what plays midi: a family, a wave and the first gene
	tell := func(a, b, k, midi int, salience float32, family, wave int, gene uint8) {
		for t := a; t < b; t++ {
			f.Salience[t*RipPitches+midi-RipMinMIDI], f.VoiceAt[t*RipVoiceCells+k] = salience, uint8(midi-RipMinMIDI)
			row := f.Voices[(t*RipVoiceCells+k)*RipVoiceSize:][:RipVoiceSize]
			row[family], row[names+wave], row[names+waves] = 255, 255, gene
		}
	}
	tell(10, 20, 0, 60, 0.9, 0, 4, 51) // a piano on a modal bank, loud
	tell(20, 30, 3, 60, 0.3, 1, 3, 204)
	tell(30, 40, 1, 62, 0.5, 2, 1, 51) // a sound that moves up a cell
	tell(40, 50, 1, 63, 0.5, 2, 1, 153)
	tell(40, 50, 2, 62, 0.5, 6, 0, 255) // another's, in the cell it left
	f.Salience[15*RipPitches+70-RipMinMIDI] = 0.9
	notes := []ripNote{{start: 10, end: 30, midi: 60}, {start: 30, end: 50, midi: 62, cells: []ripVisit{{midi: 62, start: 30, end: 40}, {midi: 63, start: 40, end: 50}}},
		{start: 10, end: 30, midi: 70}}
	f.tell(notes)
	for _, c := range []struct {
		sure   float64
		family string
	}{{0.8, ""}, {0, ""}, {0.7, "piano"}} {
		f.Sure = c.sure
		if v := f.voice(notes[0].said); v == nil || v.Family != c.family || v.Wave != "modal" || math.Abs(v.Genes[0]-0.35) > 1e-6 {
			t.Errorf("a name sure past %v: the note's voice %+v, want %q on a modal bank, its first gene 0.35", c.sure, v, c.family)
		}
	}
	if v := f.voice(notes[1].said); v == nil || v.Family != "voice" || v.Wave != "saw" || math.Abs(v.Genes[0]-0.4) > 1e-6 {
		t.Errorf("the note that moved: %+v, want a voice's saw, its first gene 0.4 over both cells", v)
	}
	if v := f.voice(notes[2].said); v != nil {
		t.Errorf("a note told on no frame carries %+v", v)
	}
}

// TestANoteWrittenBesideItsCellIsToldFromIt: a sound in cell 60 that the cents
// head reads 70 cents sharp is written as a 61; it is told from cell 60, where it
// sounds, a piano on a modal bank, not from the silent 61 beside it, a guitar's saw
func TestANoteWrittenBesideItsCellIsToldFromIt(t *testing.T) {
	const n = 120
	f := &RipFrames{Hop: RipFrameSec, Flux: make([]float64, n), Sure: 0.5,
		VoiceAt: slices.Repeat([]uint8{255}, n*RipVoiceCells), Voices: make([]uint8, n*RipVoiceCells*RipVoiceSize)}
	for _, h := range [...]*[]float32{&f.Salience, &f.Strike, &f.Cents, &f.CentsError} {
		*h = make([]float32, n*RipPitches)
	}
	names := len(RipFamilies) - 1
	for u := range n {
		for m := range RipPitches {
			f.CentsError[u*RipPitches+m] = 5
		}
		for k, c := range [...]struct{ midi, family, wave int }{{60, 0, 4}, {61, 1, 1}} {
			f.VoiceAt[u*RipVoiceCells+k] = uint8(c.midi - RipMinMIDI)
			row := f.Voices[(u*RipVoiceCells+k)*RipVoiceSize:][:RipVoiceSize]
			row[c.family], row[names+c.wave] = 255, 255
		}
	}
	for u := 10; u < 80; u++ {
		f.Salience[u*RipPitches+60-RipMinMIDI], f.Cents[u*RipPitches+60-RipMinMIDI] = 0.9, 70
		f.Salience[u*RipPitches+61-RipMinMIDI] = 0.01
	}
	f.Strike[10*RipPitches+60-RipMinMIDI] = 0.99
	notes := f.strikes(RipBassTop, RipMaxMIDI, DefaultRipParams())
	if len(notes) != 1 || notes[0].midi != 61 {
		t.Fatalf("notes %+v, want one written at 61", notes)
	}
	f.tell(notes)
	if v := f.voice(notes[0].said); v == nil || v.Family != "piano" || v.Wave != "modal" {
		t.Errorf("the note's voice %+v, want the piano on a modal bank its own cell says", v)
	}
}

// TestAFamilyIsNamedWhereItsShareSurelyPassesTheMeasure: a piano's share of
// 0.9985 kept as the byte 255 reads back whole, where the head's lay anywhere
// from half a byte under; under a measure of 0.999 it names nothing, as the
// unrounded share would not, and under 0.99 the piano
func TestAFamilyIsNamedWhereItsShareSurelyPassesTheMeasure(t *testing.T) {
	said := make([]float64, RipVoiceSize)
	said[slices.Index(RipFamilies[:], "piano")] = float64(uint8(math.Round(255*0.9985))) / 255
	for _, c := range []struct {
		sure   float64
		family string
	}{{0.999, ""}, {0.99, "piano"}} {
		if v := (&RipFrames{Sure: c.sure}).voice(said); v.Family != c.family {
			t.Errorf("a share kept as %.5f under a measure of %v names %q, want %q", said[0], c.sure, v.Family, c.family)
		}
	}
}

// TestALinesVoiceIsItsNotesMeanVoice: where the front end told a line's notes
// their voices the line plays their mean by length, on the wave of the highest
// share, and not the frames' genes on a modal bank; a note told none adds none.
func TestALinesVoiceIsItsNotesMeanVoice(t *testing.T) {
	names, waves := len(RipFamilies)-1, len(VoiceWaves)
	bright := slices.IndexFunc(voiceGenes, func(g voiceGene) bool { return g.name == "bright" })
	said := func(wave int, b float64) []float64 {
		s := make([]float64, RipVoiceSize)
		s[names+wave], s[names+waves+bright] = 1, b
		return s
	}
	frame := slices.Repeat([]float64{1}, VoiceGeneCount)
	notes := []ripNote{{start: 0, end: 300, midi: 60, voice: frame, said: said(3, 0.2)}, {start: 300, end: 400, midi: 62, voice: frame, said: said(1, 0.8)},
		{start: 400, end: 900, midi: 64, voice: frame}}
	if v := voiceOf(notes); v.Wave != "string" || math.Abs(v.Bright-0.35) > 1e-9 {
		t.Errorf("the line plays a %s of brightness %.3f, want its notes' string of 0.35", v.Wave, v.Bright)
	}
}

// TestMatchNotesCountsANoteOnlyNearItsPlace: under a tempo that bends, every
// note in its place matches, and a note of the right pitch half a second off
// does not, where pitches in order alone would take it.
func TestMatchNotesCountsANoteOnlyNearItsPlace(t *testing.T) {
	var a, b []RipNote
	at := 0.0
	for i := range 60 {
		m := 60 + i%7
		b = append(b, RipNote{Start: 0.25 * float64(i), End: 0.25*float64(i) + 0.2, MIDI: m})
		a = append(a, RipNote{Start: at, End: at + 0.2, MIDI: m})
		at += 0.25 + 0.05*math.Sin(float64(i)/6) // up to a fifth either way, some 40 notes a cycle
	}
	const off = 30
	a[off].Start += 0.5
	slices.SortFunc(a, func(x, y RipNote) int { return cmpFloat(x.Start, y.Start) })
	ag := MatchNotes(a, b, false)
	if l := ag.Lines[0]; ag.Shift != 0 || l.Matched != 59 || len(l.OnlyA) != 1 || len(l.OnlyB) != 1 || l.OnlyB[0].Start != b[off].Start {
		t.Errorf("shift %d, %d matched, only a %v, only b %v", ag.Shift, l.Matched, l.OnlyA, l.OnlyB)
	}
}

// TestMatchNotesAlignsTwoTranscripts: a transcript against itself transposed,
// stretched, one note dropped and one added matches every note but those,
// finds the transposition and the time map, and parses back from the text
// form a listener corrects.
func TestMatchNotesAlignsTwoTranscripts(t *testing.T) {
	var a []RipNote
	for i, m := range [...]int{67, 69, 69, 74, 75, 70, 72, 75, 74, 81, 74, 67} {
		a = append(a, RipNote{Start: 0.3 * float64(i), End: 0.3*float64(i) + 0.2, MIDI: m, Vel: 0.7, Second: i%4 == 3})
	}
	for i, m := range [...]int{43, 41, 43, 46} {
		a = append(a, RipNote{Start: 1.2 * float64(i), End: 1.2*float64(i) + 1, MIDI: m, Vel: 0.9, Bass: true})
	}
	var b []RipNote
	for i, n := range a {
		if i == 5 { // one note the second witness lacks
			continue
		}
		n.Start, n.End, n.MIDI = 1.15*n.Start+0.4, 1.15*n.End+0.4, n.MIDI+3
		b = append(b, n)
	}
	b = append(b, RipNote{Start: 2.05, End: 2.2, MIDI: 72, Vel: 0.5}) // and one it alone hears
	ag := MatchNotes(a, b, false)
	m, u, bass := ag.Lines[0], ag.Lines[1], ag.Lines[2]
	if ag.Shift != -3 || m.Matched != 8 || len(m.OnlyA) != 1 || len(m.OnlyB) != 1 || u.Matched != 3 || bass.Matched != 4 || ag.Register.Matched != 11 {
		t.Errorf("shift %d, melody %d matched of %d and %d (only a %d, only b %d), under %d, bass %d, register %d", ag.Shift, m.Matched, m.A, m.B, len(m.OnlyA), len(m.OnlyB), u.Matched, bass.Matched, ag.Register.Matched)
	}
	if math.Abs(ag.Stretch-1/1.15) > 0.02 || math.Abs(ag.Offset+0.4/1.15) > 0.05 {
		t.Errorf("time map a = %.3f b %+.3f, want %.3f and %+.3f", ag.Stretch, ag.Offset, 1/1.15, -0.4/1.15)
	}
	text := "x: 16 note(s) at 120 bpm in G2 minor\nmelody   0.300s bar 1 step  3        A4   degree 1 octave 2, 1.6 step(s) at 0.70, support 1.00\nunder 0.9s F5 0.25s\nbass 0s G2 8 steps\n"
	notes, err := ParseTranscript(text, 0, 0)
	if err != nil || len(notes) != 3 || notes[0].MIDI != 69 || math.Abs(notes[0].End-0.5) > 0.01 || !notes[1].Second || notes[1].MIDI != 77 || !notes[2].Bass || math.Abs(notes[2].End-1) > 0.01 {
		t.Errorf("parsed %+v, %v", notes, err)
	}
	if _, err := ParseTranscript("melody 0.1s 3 steps\n", 0, 0); err == nil {
		t.Error("a line without a pitch parsed")
	}
	// a rhythm tapped out: eighths at 120 bpm from one second, a group twice
	tab, err := ParseTranscript("unit eighth\nstart 1s\nmelody (1-2)x2 3.4\nunder 11\n", 120, 0)
	if err != nil || len(tab) != 8 || tab[0].MIDI != -1 || math.Abs(tab[0].Start-1) > 1e-9 || math.Abs(tab[0].End-1.5) > 1e-9 || math.Abs(tab[1].Start-1.5) > 1e-9 || math.Abs(tab[4].Start-2.5) > 1e-9 || math.Abs(tab[5].Start-3) > 1e-9 || !tab[6].Second || math.Abs(tab[7].Start-1.25) > 1e-9 {
		t.Errorf("tab parsed %+v, %v", tab, err)
	}
	// heard against it by onset: every note found but one, and one cut in two
	var heard []RipNote
	for i, n := range tab[:5] {
		if i == 3 {
			continue
		}
		heard = append(heard, RipNote{Start: n.Start + 0.02, End: n.End, MIDI: 60 + i})
		if i == 0 {
			heard = append(heard, RipNote{Start: n.Start + 0.3, End: n.End, MIDI: 60})
		}
	}
	rhythm := MatchNotes(heard, tab[:5], false)
	if l := rhythm.Lines[0]; !l.Rhythm || l.Matched != 4 || len(l.OnlyB) != 1 || l.Split != 1 || len(l.OnlyA) != 1 {
		t.Errorf("rhythm: matched %d of %d given, only given %d, only heard %d, split %d", l.Matched, l.B, len(l.OnlyB), len(l.OnlyA), l.Split)
	}
	if _, err := ParseTranscript("melody (1-2\n", 0, 0); err == nil {
		t.Error("a rhythm without a unit parsed")
	}
}

// TestAgreementCountsEveryNoteWhateverItsLine: a truth that files a guitar's
// notes on one line, against a rip that put its low strings on a bass line and
// some of the rest on a second, matches every note in All, where the lines and
// the register count only what the two filed alike.
func TestAgreementCountsEveryNoteWhateverItsLine(t *testing.T) {
	var a, b []RipNote
	for i, m := range [...]int{40, 59, 47, 64, 43, 67, 52, 60, 45, 71, 50, 62, 41, 65, 48, 69} {
		n := RipNote{Start: 0.25 * float64(i), End: 0.25*float64(i) + 0.2, MIDI: m}
		b = append(b, n)
		n.Bass, n.Second = m < RipBassTop, m >= RipBassTop && i%3 == 0
		a = append(a, n)
	}
	ag := MatchNotes(a, b, false)
	if all := ag.All; all.A != 16 || all.B != 16 || all.Matched != 16 || len(all.OnlyA)+len(all.OnlyB) != 0 {
		t.Errorf("every note as one line: %d of a's %d and b's %d matched, only a %v, only b %v", all.Matched, all.A, all.B, all.OnlyA, all.OnlyB)
	}
	if ag.Lines[0].Matched != 5 || ag.Lines[1].Matched != 0 || ag.Lines[2].Matched != 0 || ag.Register.Matched != 9 {
		t.Errorf("by line %d, %d and %d matched, the register %d: want 5, 0, 0 and 9, what the two filed alike", ag.Lines[0].Matched, ag.Lines[1].Matched, ag.Lines[2].Matched, ag.Register.Matched)
	}
}

// TestAPitchedLineMatchesByPitchBesideATappedOne: a transcript of a tapped melody
// and a bass with pitches, against a rip whose bass is all another pitch: every
// note as one line counts the taps by onset and the bass by pitch, timed and
// fitted, so the bass's wrong notes are not heard.
func TestAPitchedLineMatchesByPitchBesideATappedOne(t *testing.T) {
	text := "at 120 bpm\nunit quarter\nstart 0s\nmelody aaaaaaaa\n"
	var rip []RipNote
	for i := range 8 {
		at := 0.5 * float64(i)
		text += fmt.Sprintf("bass %.2fs %s 0.2s\n", at+0.25, []string{"G2", "A2", "B2", "C3"}[i%4])
		rip = append(rip, RipNote{Start: at, End: at + 0.4, MIDI: 72}, RipNote{Start: at + 0.25, End: at + 0.45, MIDI: 40, Bass: true})
	}
	truth, err := ParseTranscript(text, 120, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, timed := range []bool{true, false} {
		ag := MatchNotes(rip, truth, timed)
		melody, bass := ag.Lines[0], ag.Lines[2]
		if ag.All.Matched != melody.Matched+bass.Matched || ag.All.Rhythm || melody.Matched != 8 || timed && bass.Matched != 0 {
			t.Errorf("timed %v, shift %d: melody %d of %d by onset, bass %d of %d by pitch, All %d of %d (rhythm %v)",
				timed, ag.Shift, melody.Matched, melody.B, bass.Matched, bass.B, ag.All.Matched, ag.All.B, ag.All.Rhythm)
		}
	}
}

// TestEveryNoteWithinTheWindowIsPaired: two notes of one pitch 150 ms apart,
// heard 80 and 90 ms early, both pair though the second heard is nearer the first
// given, and a note heard twice pairs with the nearer; filed on two lines, every
// note as one line counts no fewer than the lines.
func TestEveryNoteWithinTheWindowIsPaired(t *testing.T) {
	note := func(at float64) RipNote { return RipNote{Start: at, End: at + 0.1, MIDI: 60} }
	truth := []RipNote{note(1), note(1.15), note(2)}
	rip := []RipNote{note(0.92), note(1.06), note(1.93), note(2.01)}
	for _, apart := range []bool{false, true} {
		truth[1].Second, rip[1].Second = apart, apart
		ag := MatchNotes(rip, truth, true)
		if lines := ag.Lines[0].Matched + ag.Lines[1].Matched; ag.All.Matched != 3 || lines != 3 || len(ag.All.OnlyA) != 1 || ag.All.OnlyA[0].Start != 1.93 {
			t.Errorf("second notes on another line %v: All %d of %d, the lines %d, only heard %v", apart, ag.All.Matched, ag.All.B, lines, ag.All.OnlyA)
		}
	}
}

// TestANotePerfectRipMatchesWhateverItsTempo: a sparse line played at nine
// tenths of its seconds, and played slowing from its tempo to 0.7 of it,
// matches every note: the map lies on a steady tempo, however far from the
// given, and follows one that drifts.
func TestANotePerfectRipMatchesWhateverItsTempo(t *testing.T) {
	for name, played := range map[string]func(float64) float64{
		"steady":     func(s float64) float64 { return 0.9 * s },
		"ritardando": func(s float64) float64 { return s + (1/0.7-1)*s*s/30 },
	} {
		var truth, rip []RipNote
		for i, at := range [...]float64{0, 1, 2, 3, 5, 6, 8, 9, 10, 12, 13, 15} {
			m := 60 + []int{0, 2, 4, 5, 7, 9, 11, 12, 14, 16, 17, 19}[i]
			truth = append(truth, RipNote{Start: at, End: at + 0.5, MIDI: m})
			rip = append(rip, RipNote{Start: played(at), End: played(at) + 0.45, MIDI: m})
		}
		if ag := MatchNotes(rip, truth, false); ag.All.Matched != len(truth) {
			t.Errorf("%s: stretch %.3f, All %d of %d, only given %v", name, ag.Stretch, ag.All.Matched, len(truth), ag.All.OnlyB)
		}
	}
}

// TestTheRegisterLeadsTheMapWhereItHasThePairs: sixty bass notes and three melody
// notes given, against a rip of them all with a chord a second besides, match all
// 63 fitted, the bass's map placing more; and an ensemble's rip on the truth's
// clock, with misses, octave errors and extras filed on lines of its own, holds
// the timed count within two notes under the register's map.
func TestTheRegisterLeadsTheMapWhereItHasThePairs(t *testing.T) {
	var truth, rip []RipNote
	for i := range 60 {
		at := float64(i)
		n := RipNote{Start: at, End: at + 0.8, MIDI: 36 + (i*i+3*i)%11, Bass: true}
		truth, rip = append(truth, n), append(rip, n)
		for k, m := range [...]int{72, 76, 79} {
			rip = append(rip, RipNote{Start: at + 0.3*float64(k), End: at + 0.3*float64(k) + 0.25, MIDI: m})
		}
	}
	for k, at := range [...]float64{20, 40.3, 50.6} {
		truth = append(truth, RipNote{Start: at, End: at + 0.25, MIDI: 72 + []int{0, 4, 7}[k]})
	}
	if ag := MatchNotes(rip, truth, false); ag.Shift != 0 || ag.All.Matched != len(truth) {
		t.Errorf("a few melody notes: shift %d, bass %d of %d, melody %d of %d, All %d of %d",
			ag.Shift, ag.Lines[2].Matched, ag.Lines[2].B, ag.Lines[0].Matched, ag.Lines[0].B, ag.All.Matched, ag.All.B)
	}
	scale := [...]int{0, 2, 4, 5, 7, 9, 11}
	for seed := range int64(3) {
		rng := rand.New(rand.NewSource(seed + 1))
		truth, rip = nil, nil
		for at := 0.0; at < 30; at += 0.5 { // a bass a beat, a chord a bar, a melody in eighths
			root := [...]int{0, 9, 5, 7}[int(at/2)%4]
			truth = append(truth, RipNote{Start: at, End: at + 0.45, MIDI: 36 + root, Bass: true})
			for _, iv := range [...]int{0, 4, 7} {
				if int(at*2)%2 == 0 {
					truth = append(truth, RipNote{Start: at, End: at + 0.9, MIDI: 60 + (root+iv)%12, Second: true})
				}
			}
			for _, off := range [...]float64{0, 0.25} {
				truth = append(truth, RipNote{Start: at + off, End: at + off + 0.2, MIDI: 72 + scale[rng.Intn(7)]})
			}
		}
		heard := func(n RipNote, sd float64, up int) RipNote {
			n.Start, n.MIDI = n.Start+rng.NormFloat64()*sd, n.MIDI+up
			n.Bass, n.Second = n.MIDI < RipBassTop, n.MIDI >= RipBassTop && rng.Intn(3) == 0
			return n
		}
		for _, n := range truth { // a note in five missed, one in six an octave off, an extra in four
			if rng.Float64() < 0.8 {
				rip = append(rip, heard(n, 0.025, [...]int{0, 0, 0, 0, 0, 12}[rng.Intn(6)]))
			}
			if rng.Float64() < 0.25 {
				rip = append(rip, heard(n, 0.03, [...]int{-12, 12, 7, 19}[rng.Intn(4)]))
			}
		}
		if timed, fitted := MatchNotes(rip, truth, true).All.Matched, MatchNotes(rip, truth, false).All.Matched; fitted < timed-2 || fitted > timed+2 {
			t.Errorf("ensemble %d: fitted All %d, timed %d, of %d given", seed+1, fitted, timed, len(truth))
		}
	}
}

// TestATimedTruthFitsNoMap: a transcript 0.15 s late and a tone up matches
// every note under the map and the transposition the search finds, and none
// timed, its seconds and pitches read as given, where one on the same clock
// matches every note.
func TestATimedTruthFitsNoMap(t *testing.T) {
	var a, late, on []RipNote
	for i, m := range [...]int{67, 69, 62, 74, 75, 70, 72, 65, 76, 81, 73, 60} {
		at := 0.3 * float64(i)
		a = append(a, RipNote{Start: at, End: at + 0.2, MIDI: m})
		late = append(late, RipNote{Start: at + 0.15, End: at + 0.35, MIDI: m + 2})
		on = append(on, RipNote{Start: at + 0.02, End: at + 0.22, MIDI: m})
	}
	if ag := MatchNotes(a, late, false); ag.Shift != -2 || math.Abs(ag.Offset+0.15) > 0.02 || ag.All.Matched != len(a) {
		t.Errorf("fitted: shift %d, offset %+.3f, %d of %d matched", ag.Shift, ag.Offset, ag.All.Matched, len(a))
	}
	if ag := MatchNotes(a, late, true); ag.Shift != 0 || ag.Stretch != 1 || ag.Offset != 0 || ag.At(3) != 3 || ag.All.Matched != 0 {
		t.Errorf("timed: shift %d, map %.3f × b %+.3f, a second 3 placed at %.3f, %d matched; want no map and none", ag.Shift, ag.Stretch, ag.Offset, ag.At(3), ag.All.Matched)
	}
	if ag := MatchNotes(a, on, true); ag.All.Matched != len(a) || ag.Lines[0].Matched != len(a) {
		t.Errorf("timed, on the clock: %d of %d matched, %d on the line", ag.All.Matched, len(a), ag.Lines[0].Matched)
	}
}

// TestMatchHitsPlacesARipsDrumsOnTheGivenOnes: a rip's drum steps, placed on its
// beats at 117 bpm, match a beat given at 120 bpm two seconds later under the
// map the search finds, drum by drum within half a step: a kick the rip missed
// is only given, and a snare it added only the rip's.
func TestMatchHitsPlacesARipsDrumsOnTheGivenOnes(t *testing.T) {
	const bars, beat = 8, 60.0 / 117
	r := &Rip{BPM: 117, Bars: make([]RipBar, bars)}
	for i := range BeatsPerBar*bars + 1 {
		r.Beats = append(r.Beats, 0.3+float64(i)*beat)
	}
	var given []RipHit
	for b := range bars {
		for s := 0; s < StepsPerBar; s += 2 {
			drums := []InstrumentType{InstrHihat}
			if s%8 == 0 {
				drums = append(drums, InstrKick)
			} else if s%4 == 0 {
				drums = append(drums, InstrSnare)
			}
			for _, d := range drums {
				given = append(given, RipHit{Start: 2 + float64(b*StepsPerBar+s)*0.125, Drum: d})
				if d != InstrKick || b != 3 || s != 8 {
					r.Bars[b].Drums[d] = append(r.Bars[b].Drums[d], Step{Pos: s})
				}
			}
		}
	}
	r.Bars[5].Drums[InstrSnare] = append(r.Bars[5].Drums[InstrSnare], Step{Pos: 6})
	ag := MatchHits(r.Hits(), given, beat/StepsPerBeat/2)
	want := [InstrClap][3]int{InstrKick: {15, 0, 1}, InstrSnare: {16, 1, 0}, InstrHihat: {64, 0, 0}}
	for d, l := range ag.Drums {
		if got := [3]int{l.Matched, len(l.OnlyA), len(l.OnlyB)}; got != want[d] {
			t.Errorf("drum %d: matched, only the rip's, only given %v, want %v", d, got, want[d])
		}
	}
	if math.Abs(ag.Stretch-beat/0.5) > 0.005 || math.Abs(ag.Offset-(0.3-2*beat/0.5)) > 0.05 {
		t.Errorf("map %.3f × given %+.3f, want %.3f × given %+.3f", ag.Stretch, ag.Offset, beat/0.5, 0.3-2*beat/0.5)
	}
}

// barGrid is a 4/4 rip of bars bars, beat seconds a beat from 0.3 s, and its
// beats every step-th from the first
func barGrid(bars int, beat float64, step int) (*Rip, []float64) {
	r := &Rip{}
	var every []float64
	for i := range BeatsPerBar*bars + 1 {
		r.Beats = append(r.Beats, 0.3+float64(i)*beat)
		if i%step == 0 {
			every = append(every, r.Beats[i])
		}
	}
	return r, every
}

// TestMatchBarsScoresTheSameDownbeatsOne: a 4/4 rip grid against its own beats
// and bar lines has every line on a downbeat and every beat on a beat, at phase 0
func TestMatchBarsScoresTheSameDownbeatsOne(t *testing.T) {
	r, downbeats := barGrid(8, 0.5, BeatsPerBar)
	if s := MatchBars(r, r.Beats, downbeats); s != (BarScore{Ratio: 1, Same: true, Downbeats: 1, Beats: 1}) {
		t.Errorf("%+v", s)
	}
}

// TestMatchBarsReadsDownbeatsABeatEarlyAsPhaseAQuarter: against downbeats a beat
// before its lines, on the same beats, a 4/4 grid has no line on a downbeat and
// its lines a quarter of a bar late
func TestMatchBarsReadsDownbeatsABeatEarlyAsPhaseAQuarter(t *testing.T) {
	r, _ := barGrid(8, 0.5, 1)
	var downbeats []float64
	for i := BeatsPerBar - 1; i < len(r.Beats); i += BeatsPerBar {
		downbeats = append(downbeats, r.Beats[i])
	}
	if s := MatchBars(r, r.Beats, downbeats); !s.Same || s.Downbeats != 0 || math.Abs(s.Phase-0.25) > 1e-9 || s.Beats != 1 {
		t.Errorf("%+v", s)
	}
}

// TestMatchBarsCallsAHalfLengthGridNotSame: a 4/4 grid at twice the truth's
// tempo has a line on every truth downbeat, yet its bar is half the truth's, so
// no downbeat counts
func TestMatchBarsCallsAHalfLengthGridNotSame(t *testing.T) {
	r, _ := barGrid(16, 0.25, 1)
	_, beats := barGrid(8, 0.5, 1)
	_, downbeats := barGrid(8, 0.5, BeatsPerBar)
	if s := MatchBars(r, beats, downbeats); s.Same || math.Abs(s.Ratio-0.5) > 1e-9 || s.Downbeats != 0 {
		t.Errorf("%+v", s)
	}
}

// TestReadJAMSBeatsReadsBeatsAndDownbeats: a JAMS file in GuitarSet's form, its
// pitch contour's data arrays by field, reads its beat_position times as beats
// and those at position 1 as downbeats
func TestReadJAMSBeatsReadsBeatsAndDownbeats(t *testing.T) {
	const jams = `{"annotations": [
	{"namespace": "pitch_contour", "data": {"time": [0.1], "duration": [0.0], "value": [{"index": 0, "frequency": 196.0, "voiced": true}], "confidence": [null]}},
	{"namespace": "beat_position", "data": [
		{"time": 0.5, "duration": 0.0, "value": {"position": 3, "measure": 0, "num_beats": 3, "beat_units": 4}, "confidence": null},
		{"time": 1.0, "duration": 0.0, "value": {"position": 1, "measure": 1, "num_beats": 3, "beat_units": 4}, "confidence": null},
		{"time": 1.5, "duration": 0.0, "value": {"position": 2, "measure": 1, "num_beats": 3, "beat_units": 4}, "confidence": null},
		{"time": 2.0, "duration": 0.0, "value": {"position": 3, "measure": 1, "num_beats": 3, "beat_units": 4}, "confidence": null},
		{"time": 2.5, "duration": 0.0, "value": {"position": 1, "measure": 2, "num_beats": 3, "beat_units": 4}, "confidence": null}]}],
	"file_metadata": {"duration": 3.0, "jams_version": "0.3.1"}, "sandbox": {}}`
	beats, downbeats, err := ReadJAMSBeats(strings.NewReader(jams))
	if err != nil || !slices.Equal(beats, []float64{0.5, 1, 1.5, 2, 2.5}) || !slices.Equal(downbeats, []float64{1, 2.5}) {
		t.Errorf("beats %v, downbeats %v, %v", beats, downbeats, err)
	}
}

// TestNoteLevelsReadARecordingsLoudness: of two notes a recording plays 20 dB
// apart, under a map that places the given seconds on it, the softer reads
// 20 dB lower, and a note it does not play, two octaves up, reads far under both.
func TestNoteLevelsReadARecordingsLoudness(t *testing.T) {
	const rate = 22050
	x := make([]float64, 3*rate)
	for i := range rate / 2 {
		ph := 2 * math.Pi * NoteFreq(69) * float64(i) / rate
		x[rate/2+i], x[2*rate+i] = 0.5*math.Sin(ph), 0.05*math.Sin(ph)
	}
	notes := []RipNote{{Start: 0, MIDI: 69}, {Start: 1.5, MIDI: 69}, {Start: 1.5, MIDI: 93}}
	lv := NoteLevels(x, rate, notes, func(s float64) float64 { return s + 0.5 }, 0)
	if d := lv[0] - lv[1]; math.Abs(d-20) > 1 {
		t.Errorf("the softer note reads %.1f dB under the louder, want 20", d)
	}
	if lv[2] > lv[1]-40 {
		t.Errorf("A6 over a recording of A4 reads %.1f dB against A4's %.1f", lv[2], lv[1])
	}
}

// TestMIDIRoundTrip: a transcript written as a MIDI file reads back note for
// note and hit for hit within half a tick, each line from its named track and
// each drum from the drum channel, a note that ends as the next of its pitch
// starts kept apart from it, each glide as written and every note unmeasured.
func TestMIDIRoundTrip(t *testing.T) {
	in := []RipNote{
		{Start: 0, End: 1.2, MIDI: 40, Vel: 1, Bass: true, Bend: -12, BendAt: 0.5},
		{Start: 0.1, End: 0.4, MIDI: 72, Vel: 0.8, Bright: 0.9},
		{Start: 0.4, End: 0.73, MIDI: 72, Vel: 0.5, Bend: 1.46, BendAt: 0.6},
		{Start: 0.5, End: 1.1, MIDI: 67, Vel: 0.6, Second: true},
		{Start: 0.77, End: 1.234, MIDI: 76, Vel: 0.3, Bend: -0.5},
	}
	hits := []RipHit{{Start: 0, Drum: InstrKick, Vel: 1}, {Start: 0.22, Drum: InstrHihat, Vel: 0.5}, {Start: 0.44, Drum: InstrSnare, Vel: 0.8}}
	var file bytes.Buffer
	if err := WriteMIDI(&file, in, hits, 137, Meter{}); err != nil {
		t.Fatal(err)
	}
	m, err := ReadMIDI(&file)
	if err != nil {
		t.Fatal(err)
	}
	out, outHits := m.Notes, m.Hits
	if len(out) != len(in) || len(outHits) != len(hits) {
		t.Fatalf("read %d note(s) of %d, %d hit(s) of %d", len(out), len(in), len(outHits), len(hits))
	}
	half := 0.5 / (137.0 / 60 * midiDivision)
	for i, n := range out {
		w := in[i]
		if n.MIDI != w.MIDI || n.Bass != w.Bass || n.Second != w.Second || n.Bright != RipUnmeasured || n.Bend != w.Bend || n.BendAt != w.BendAt ||
			math.Abs(n.Start-w.Start) > half || math.Abs(n.End-w.End) > half || math.Abs(n.Vel-w.Vel) > 0.5/127 {
			t.Errorf("note %d: read %+v, wrote %+v", i, n, w)
		}
	}
	for i, h := range outHits {
		if w := hits[i]; h.Drum != w.Drum || math.Abs(h.Start-w.Start) > half || math.Abs(h.Vel-w.Vel) > 0.5/127 {
			t.Errorf("hit %d: read %+v, wrote %+v", i, h, w)
		}
	}
}

// smf is a type-1 file's header for tracks tracks, 480 ticks a quarter
func smf(tracks byte) []byte {
	return []byte{'M', 'T', 'h', 'd', 0, 0, 0, 6, 0, 1, 0, tracks, 0x01, 0xE0}
}

// track is a track chunk of the events given
func track(events ...byte) []byte {
	return append(binary.BigEndian.AppendUint32([]byte("MTrk"), uint32(len(events))), events...)
}

// TestReadMIDITempoMap: a type-1 file whose first track halves the tempo two
// beats in reads in seconds under that map; of its unnamed tracks, the one
// centred under E3 is the bass and the other the melody, and the drum channel's
// key 36 is a kick. Its notes use running status and a note-on at velocity 0.
func TestReadMIDITempoMap(t *testing.T) {
	file := smf(4)
	file = append(file, track(
		0x00, 0xFF, 0x51, 3, 0x07, 0xA1, 0x20, // 120 bpm
		0x87, 0x40, 0xFF, 0x51, 3, 0x0F, 0x42, 0x40, // 960 ticks on, 60 bpm
		0x00, 0xFF, 0x2F, 0)...)
	file = append(file, track(
		0x00, 0xFF, 0x03, 4, 'L', 'e', 'a', 'd',
		0x00, 0x90, 72, 100, // C5 at 0s
		0x83, 0x60, 72, 0, // its end at 480, 0.5s
		0x00, 76, 90, // E5 at 0.5s
		0x87, 0x40, 0x80, 76, 0, // its end at 1440, 2s
		0x00, 0xFF, 0x2F, 0)...)
	file = append(file, track(
		0x87, 0x40, 0x91, 36, 80, // C2 at 960, 1s
		0x87, 0x40, 0x81, 36, 0, // its end at 1920, 3s
		0x00, 0xFF, 0x2F, 0)...)
	file = append(file, track(
		0x00, 0x99, 36, 100,
		0x83, 0x60, 0x89, 36, 0,
		0x00, 0xFF, 0x2F, 0)...)
	m, err := ReadMIDI(bytes.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	notes, hits := m.Notes, m.Hits
	if len(hits) != 1 || hits[0].Drum != InstrKick || hits[0].Start != 0 {
		t.Errorf("drums read %+v, want a kick at 0s", hits)
	}
	want := []RipNote{
		{Start: 0, End: 0.5, MIDI: 72},
		{Start: 0.5, End: 2, MIDI: 76},
		{Start: 1, End: 3, MIDI: 36, Bass: true},
	}
	if len(notes) != len(want) {
		t.Fatalf("read %+v", notes)
	}
	for i, n := range notes {
		w := want[i]
		if n.MIDI != w.MIDI || n.Bass != w.Bass || n.Second || math.Abs(n.Start-w.Start) > 1e-9 || math.Abs(n.End-w.End) > 1e-9 {
			t.Errorf("note %d: %+v, want %+v", i, n, w)
		}
	}
}

// TestMIDIScoreRipsAsTheLoop: the events a rendered loop plays, written as a
// MIDI file and read back as a rip, give the loop's own patterns, in its key,
// at its tempo, its first bar from the file's start.
func TestMIDIScoreRipsAsTheLoop(t *testing.T) {
	const bpm, bars = 140, 8
	_, slots := startLoopEngine(t)
	step := 60.0 / bpm / StepsPerBeat
	var notes []RipNote
	var hits []RipHit
	for _, e := range ExpectedEvents(slots, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil) {
		at := float64(e.Bar*StepsPerBar+e.Step) * step
		if e.Instr.IsDrum() {
			hits = append(hits, RipHit{Start: at, Drum: e.Instr, Vel: e.Vel})
			continue
		}
		notes = append(notes, RipNote{Start: at, End: at + float64(e.Dur)*step, MIDI: e.MIDI, Vel: e.Vel, Bass: e.MIDI < RipBassTop})
	}
	var file bytes.Buffer
	if err := WriteMIDI(&file, notes, hits, bpm, Meter{}); err != nil {
		t.Fatal(err)
	}
	m, err := ReadMIDI(&file)
	if err != nil {
		t.Fatal(err)
	}
	r, dropped := m.Rip()
	if r.BPM != bpm || r.Downbeat != 0 || len(r.Bars) != bars || r.Root != DefaultRootNote || r.Scale != ScalePhrygian || dropped != 0 {
		t.Fatalf("%d bpm from %.2fs, %d bars, root %d scale %d, %d dropped", r.BPM, r.Downbeat, len(r.Bars), r.Root, r.Scale, dropped)
	}
	// a velocity travels as one of 127 steps
	rounded := func(pats []*PatternDef) []*PatternDef {
		for _, p := range pats {
			for _, tr := range p.Track {
				for i := range tr.Event {
					tr.Event[i].Vel = math.Round(tr.Event[i].Vel*127) / 127
				}
			}
		}
		return pats
	}
	want := ExpectedRip(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil)
	if got, want := rounded(r.Patterns("loop", 4)), rounded(want.Patterns("loop", 4)); !reflect.DeepEqual(got, want) {
		t.Errorf("patterns\n%+v\nwant\n%+v", got, want)
	}
}

// TestMIDIScoreKeepsItsFirstTempo: a score in 4/4 that starts at 60 bpm and
// speeds to 120 two quarters in rips at its first tempo, 60 being in the
// sequencer's range, its bar its own; its beats and its notes follow its ticks
// under the tempo map, the change counted.
func TestMIDIScoreKeepsItsFirstTempo(t *testing.T) {
	file := append(smf(2), track(
		0x00, 0xFF, 0x51, 3, 0x0F, 0x42, 0x40, // 60 bpm
		0x87, 0x40, 0xFF, 0x51, 3, 0x07, 0xA1, 0x20, // 960 ticks on, 120 bpm
		0x00, 0xFF, 0x2F, 0)...)
	file = append(file, track(
		0x00, 0x90, 72, 100, // C5 for a quarter, 1s
		0x83, 0x60, 0x80, 72, 0,
		0x87, 0x40, 0x90, 74, 100, // D5 at 1440, 2.5s, for a quarter, 0.5s
		0x83, 0x60, 0x80, 74, 0,
		0x00, 0xFF, 0x2F, 0)...)
	m, err := ReadMIDI(bytes.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := m.Rip()
	if m.BPM != 60 || m.Changes != 1 || r.BPM != 60 || r.Meter != (Meter{}) || len(r.Bars) != 1 {
		t.Fatalf("file at %.1f bpm with %d change(s); rip at %d bpm in %s, %d bar(s)", m.BPM, m.Changes, r.BPM, r.Meter, len(r.Bars))
	}
	for i, want := range []float64{0, 1, 2, 2.5, 3} {
		if math.Abs(r.Beats[i]-want) > 1e-6 {
			t.Errorf("beat %d at %.3fs, want %.3fs", i, r.Beats[i], want)
		}
	}
	// a crotchet is a beat, four steps, before the change and after it
	if mel := r.Bars[0].Melody; len(mel) != 2 || mel[0].Dur != 4 || mel[1].Pos != 12 || mel[1].Dur != 4 {
		t.Errorf("melody %+v", mel)
	}
}

// TestALineKeepsEveryPitchStruckAtAStep: a chord of three in one line rips as
// three steps at its step, and a pitch struck twice there as its louder one
func TestALineKeepsEveryPitchStruckAtAStep(t *testing.T) {
	m, err := ReadMIDI(bytes.NewReader(append(smf(1), track(
		0x00, 0x90, 60, 100, 0x00, 0x90, 64, 100, 0x00, 0x90, 67, 100, 0x00, 0x90, 67, 60, // C4 E4 G4, and G4 again
		0x83, 0x60, 0x80, 60, 0, 0x00, 0x80, 64, 0, 0x00, 0x80, 67, 0, 0x00, 0x80, 67, 0,
		0x00, 0xFF, 0x2F, 0)...)))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := m.Rip()
	var at0 []Step
	for _, s := range r.Bars[0].Melody {
		if s.Pos == 0 {
			at0 = append(at0, s)
		}
	}
	if len(at0) != 3 || at0[2].Vel < 0.7 {
		t.Errorf("steps at the chord's step %+v, want C4, E4 and G4 once each", at0)
	}
}

// TestMIDIScoreBarsFromItsTimeSignature: a file that names no time signature is
// in 4/4 from its start; one whose first bar is shorter than its signature's,
// a pickup, rips in that signature with its first bar line where the full bars
// begin and a bar before it holding the pickup.
func TestMIDIScoreBarsFromItsTimeSignature(t *testing.T) {
	notes := []byte{
		0x00, 0x90, 72, 100, // C5 at 0 for a quarter
		0x83, 0x60, 0x80, 72, 0,
		0x00, 0x90, 74, 100, // D5 at 480 for a whole bar
		0x8F, 0x00, 0x80, 74, 0,
		0x00, 0xFF, 0x2F, 0}
	plain, err := ReadMIDI(bytes.NewReader(append(smf(1), track(notes...)...)))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := plain.Rip()
	if plain.Meter != (Meter{}) || r.Downbeat != 0 || len(r.Bars) != 2 || r.Bars[0].Melody[1].Pos != 4 {
		t.Errorf("no signature: %v, first bar from %.2fs, %d bar(s), melody %+v", plain.Meter, r.Downbeat, len(r.Bars), r.Bars[0].Melody)
	}
	meters := []byte{
		0x00, 0xFF, 0x58, 4, 1, 2, 24, 8, // 1/4 at 0
		0x83, 0x60, 0xFF, 0x58, 4, 3, 2, 24, 8, // 3/4 at 480
		0x00, 0xFF, 0x2F, 0}
	picked, err := ReadMIDI(bytes.NewReader(append(append(smf(2), track(meters...)...), track(notes...)...)))
	if err != nil {
		t.Fatal(err)
	}
	r, _ = picked.Rip()
	if picked.Meter != (Meter{3, 4}) || r.Meter != picked.Meter || math.Abs(r.Downbeat+1) > 1e-6 || len(r.Bars) != 3 {
		t.Fatalf("a pickup: %v ripped in %v, first bar from %.2fs, %d bar(s)", picked.Meter, r.Meter, r.Downbeat, len(r.Bars))
	}
	if mel := r.Bars[0].Melody; len(mel) != 1 || mel[0].Pos != 8 || len(r.Bars[1].Melody) != 1 || r.Bars[1].Melody[0].Pos != 0 {
		t.Errorf("pickup bar %+v, first full bar %+v", mel, r.Bars[1].Melody)
	}
}

// TestMIDIScoreDropsNotesOutOfRange: a note under E1 or over C7 is no note a
// rip holds, so a score's is dropped and counted, the rest kept.
func TestMIDIScoreDropsNotesOutOfRange(t *testing.T) {
	m := &MIDIScore{BPM: 120, division: 480, end: 1920, clock: []midiTempo{{perTick: 0.5 / 480}}, Notes: []RipNote{
		{Start: 0, End: 0.5, MIDI: RipMinMIDI - 1}, {Start: 0.5, End: 1, MIDI: 60, Vel: 1}, {Start: 1, End: 1.5, MIDI: RipMaxMIDI}}}
	r, dropped := m.Rip()
	if dropped != 2 || len(r.Notes) != 1 || r.Notes[0].MIDI != 60 || len(r.Bars[0].Melody) != 1 {
		t.Errorf("%d dropped, transcript %+v, melody %+v", dropped, r.Notes, r.Bars[0].Melody)
	}
}

// TestRenderLinesPlaysOnlyTheInstrumentsNamed: the loop rendered with only the
// bass is quieter than with everything and not silent, and with no instrument
// named it is silence.
func TestRenderLinesPlaysOnlyTheInstrumentsNamed(t *testing.T) {
	ae, slots := startLoopEngine(t)
	tl := [][MusicSlots]PatternID{slots, slots}
	render := func(only []InstrumentType) float64 {
		buf, err := ae.RenderLines(tl, 120, Meter{}, DefaultRootNote, ScalePhrygian, nil, 1, only)
		if err != nil {
			t.Fatal(err)
		}
		var e float64
		for _, v := range buf {
			e += v * v
		}
		return math.Sqrt(e / float64(len(buf)))
	}
	all, bass, none := render(nil), render([]InstrumentType{InstrBass}), render([]InstrumentType{})
	if none != 0 || bass == 0 || bass >= all {
		t.Errorf("rms all %.4f, bass alone %.4f, nothing %.4f", all, bass, none)
	}
}

// TestNotesRingOnAcrossAChangedBarLine: a note held over a song's bar line into
// another pattern sounds as it does into its own pattern's next bar, sample for
// sample, until the new pattern's first note.
func TestNotesRingOnAcrossAChangedBarLine(t *testing.T) {
	ae, _ := startLoopEngine(t)
	held := RegisterPattern(&Pattern{Name: "held", Steps: 16, Tracks: []Track{{Instr: InstrBass, Events: []Step{{Pos: 8, Vel: 0.9, Dur: 16}}}}})
	late := RegisterPattern(&Pattern{Name: "late", Steps: 16, Tracks: []Track{{Instr: InstrBass, Events: []Step{{Pos: 8, Vel: 0.9, Deg: 4, Dur: 4}}}}})
	render := func(second PatternID) []float64 {
		buf, err := ae.RenderTimeline([][MusicSlots]PatternID{{slotBass: held}, {slotBass: second}}, 120, Meter{}, DefaultRootNote, ScalePhrygian, nil, 1)
		if err != nil {
			t.Fatal(err)
		}
		return buf
	}
	bar := SamplesPerBar(120, Meter{})
	ring := bar + 8*SamplesPerStep(120)
	same, other := render(held)[bar:ring], render(late)[bar:ring]
	if !slices.Equal(same, other) || slices.Max(same) <= 0 {
		t.Errorf("the held note under the next pattern differs from it under its own, peak %.3f", slices.Max(same))
	}
}

// TestRipParamsAreData is the rule the parameters live by: a [rip] document
// round-trips, a key outside its range or unknown is refused, and the gene vector
// is the parameters again.
func TestRipParamsAreData(t *testing.T) {
	p := DefaultRipParams()
	data, err := MarshalRipParams(p)
	if err != nil {
		t.Fatal(err)
	}
	back, err := LoadRipParams(data)
	if err != nil || back != p {
		t.Fatalf("round trip: %v\n%s", err, data)
	}
	if _, err := LoadRipParams([]byte("[rip]\nkick_share = 5\n")); err == nil {
		t.Error("a share of five was accepted")
	}
	if _, err := LoadRipParams([]byte("[rip]\nkick_shares = 0.1\n")); err == nil {
		t.Error("an unknown key was accepted")
	}
	partial, err := LoadRipParams([]byte("[rip]\nharmonics = 4\n"))
	if err != nil || partial.Harmonics != 4 || partial.Jump != p.Jump {
		t.Errorf("a partial document: %v, %+v", err, partial)
	}
	if again := RipParamsFromGenes(p.Genes()); again != p {
		t.Errorf("genes do not round-trip: %+v", again)
	}
	if len(RipGeneRanges()) != len(p.Genes()) {
		t.Error("ranges and genes differ in length")
	}
}

// TestRipPatternsWindowTheBars is the rule Patterns writes by: a pattern per part
// per window of up to four bars, the last window as long as what is left.
func TestRipPatternsWindowTheBars(t *testing.T) {
	r := &Rip{Root: 40, Scale: ScalePhrygian, Bars: make([]RipBar, 6)}
	for b := range r.Bars {
		r.Bars[b].Drums[InstrKick] = []Step{{Pos: 0, Vel: 1}}
		r.Bars[b].Melody = []Step{{Pos: 8, Vel: 0.5, Deg: b % 7, Oct: 2, Dur: 2}}
	}
	pats := r.Patterns("x", 4)
	if len(pats) != 4 {
		t.Fatalf("%d patterns, want two windows of a beat and a melody", len(pats))
	}
	if pats[0].Name != "x_beat_1" || pats[0].Steps != 64 || pats[2].Name != "x_beat_2" || pats[2].Steps != 32 {
		t.Errorf("windows %s/%d and %s/%d", pats[0].Name, pats[0].Steps, pats[2].Name, pats[2].Steps)
	}
	if ev := pats[3].Track[0].Event; len(ev) != 2 || ev[1].Pos != 24 || ev[1].Deg != 5 {
		t.Errorf("second melody window: %+v", ev)
	}
	for _, p := range pats {
		if _, err := p.Pattern(); err != nil {
			t.Errorf("%s: %v", p.Name, err)
		}
	}
}

// TestRipSongIsADictionaryAndATimeline: bars within the threshold of one another
// share a pattern, each group's medoid standing for it, the song plays them in
// the rip's order at its tempo and key, and the document carries it with the
// patterns, validated, resolvable to a timeline and back to the same song.
func TestRipSongIsADictionaryAndATimeline(t *testing.T) {
	r := &Rip{BPM: 120, Root: 40, Scale: ScaleMinor, Progression: []int{0, 5}, Bars: make([]RipBar, 6)}
	for b := range r.Bars {
		r.Bars[b].Drums[InstrKick] = []Step{{Pos: 0, Vel: 1}, {Pos: 8, Vel: 1}}
		if b%3 == 2 { // every third bar a snare joins: one hit in three differs
			r.Bars[b].Drums[InstrSnare] = []Step{{Pos: 4, Vel: 0.9}}
		}
		if b < 4 {
			r.Bars[b].Melody = []Step{{Pos: 0, Vel: 0.8, Deg: b % 2, Oct: 2, Dur: 4}}
		}
	}
	// four items a tenth apart: the pairs join at 0.1 and the pairs at 0.2 by
	// their mean, so a threshold between keeps two groups and one above makes one
	assign, medoids := Cluster(4, func(i, j int) float64 { return math.Abs(float64(i-j)) / 10 }, 0.15)
	if !slices.Equal(assign, []int{0, 0, 1, 1}) || len(medoids) != 2 {
		t.Errorf("items a tenth apart clustered %v with medoids %v under 0.15", assign, medoids)
	}
	if assign, medoids = Cluster(4, func(i, j int) float64 { return math.Abs(float64(i-j)) / 10 }, 0.25); !slices.Equal(assign, []int{0, 0, 0, 0}) || (medoids[0] != 1 && medoids[0] != 2) {
		t.Errorf("items a tenth apart clustered %v with medoids %v under 0.25", assign, medoids)
	}
	beat := r.Cluster(RipBeat, 0.05)
	if !slices.Equal(beat, []int{0, 0, 2, 0, 0, 2}) {
		t.Errorf("beat groups %v, want the snare bars apart", beat)
	}
	if loose := r.Cluster(RipBeat, 0.5); !slices.Equal(loose, []int{0, 0, 0, 0, 0, 0}) {
		t.Errorf("within a half every bar is one group: %v", loose)
	}
	melody := r.Cluster(RipMelody, 0.05)
	if !slices.Equal(melody, []int{0, 1, 0, 1, -1, -1}) {
		t.Errorf("melody groups %v, want two alternating and the silent bars out", melody)
	}
	pats, song := r.Song("x", [RipParts][]int{beat, melody, r.Cluster(RipBass, 0.05)})
	if len(pats) != 4 || pats[0].Name != "x_beat_a" || pats[1].Name != "x_beat_b" || pats[2].Name != "x_melody_a" || pats[3].Name != "x_melody_b" {
		names := make([]string, len(pats))
		for i, p := range pats {
			names[i] = p.Name
		}
		t.Fatalf("dictionary %v", names)
	}
	if song.BPM != 120 || song.Scale != "minor" || len(song.Bars) != 6 || song.Bars[2][0] != "x_beat_b" || song.Bars[4][1] != "" || song.Bars[3][1] != "x_melody_b" {
		t.Errorf("song %+v", song)
	}
	if err := ValidateSong(song); err != nil {
		t.Fatal(err)
	}
	data, err := MarshalPatternFile(&PatternSpecFile{Pattern: pats, Song: []*SongDef{song}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := LoadPatternFile(data)
	if err != nil || len(f.Pattern) != 4 || len(f.Song) != 1 || !reflect.DeepEqual(f.Song[0], song) {
		t.Fatalf("the document came back as %v, %+v", err, f)
	}
	ids := map[string]PatternID{"x_beat_a": 3, "x_beat_b": 4, "x_melody_a": 5, "x_melody_b": 6}
	tl, err := song.Timeline(func(n string) PatternID { return ids[n] })
	if err != nil || len(tl) != 6 || tl[2][0] != 4 || tl[1][1] != 6 || tl[5][1] != PatternSilence {
		t.Errorf("timeline %v: %v", tl, err)
	}
	if _, err := song.Timeline(func(string) PatternID { return PatternSilence }); err == nil {
		t.Error("an unregistered name resolved")
	}
	if letters(0) != "a" || letters(25) != "z" || letters(26) != "aa" || letters(27) != "ab" {
		t.Errorf("letters %s %s %s %s", letters(0), letters(25), letters(26), letters(27))
	}
}

// TestReadWAVReadsWhatWriteWAVWrote is the round trip of the two, and the other
// widths a recording comes in.
func TestReadWAVReadsWhatWriteWAVWrote(t *testing.T) {
	src := make([]float64, 1000)
	for i := range src {
		src[i] = 0.5 * math.Sin(float64(i)/10)
	}
	var b bytes.Buffer
	if err := WriteWAV(&b, src); err != nil {
		t.Fatal(err)
	}
	got, rate, err := ReadWAV(&b)
	if err != nil || rate != AudioSampleRate || len(got) != len(src) {
		t.Fatalf("read: %v, %d Hz, %d samples", err, rate, len(got))
	}
	for i := range src {
		if math.Abs(got[i]-src[i]) > 2.0/32768 { // the writer scales by 32767 and truncates
			t.Fatalf("sample %d: %g, wrote %g", i, got[i], src[i])
		}
	}
	// a 24-bit mono file at 48 kHz with a chunk the reader does not know
	var w bytes.Buffer
	le := binary.LittleEndian
	w.WriteString("RIFF")
	binary.Write(&w, le, uint32(0))
	w.WriteString("WAVE")
	w.WriteString("LIST")
	binary.Write(&w, le, uint32(3))
	w.Write([]byte{1, 2, 3, 0}) // padded to even
	w.WriteString("fmt ")
	binary.Write(&w, le, uint32(16))
	binary.Write(&w, le, uint16(1))
	binary.Write(&w, le, uint16(1))
	binary.Write(&w, le, uint32(48000))
	binary.Write(&w, le, uint32(48000*3))
	binary.Write(&w, le, uint16(3))
	binary.Write(&w, le, uint16(24))
	w.WriteString("data")
	binary.Write(&w, le, uint32(6))
	w.Write([]byte{0, 0, 0x40, 0, 0, 0xC0}) // +0.5, -0.5
	got, rate, err = ReadWAV(&w)
	if err != nil || rate != 48000 || len(got) != 2 || got[0] != 0.5 || got[1] != -0.5 {
		t.Fatalf("24-bit: %v, %d Hz, %v", err, rate, got)
	}
}

// TestStepAccidentalKeepsAPitchOffTheKey: a step's accidental is the semitones
// its pitch sits off the nearest degree, so every pitch reads back as itself
// through a key, a document carries it, and a rip tells it from the degree alone.
func TestStepAccidentalKeepsAPitchOffTheKey(t *testing.T) {
	for midi := RipMinMIDI; midi < RipMaxMIDI; midi++ {
		deg, oct, acc := PitchStep(40, ScalePhrygian, midi)
		if back := StepPitch(40, ScalePhrygian, deg, oct, acc); back != midi || acc < -1 || acc > 1 {
			t.Fatalf("midi %d reads as degree %d octave %d accidental %d, back %d", midi, deg, oct, acc, back)
		}
	}
	pats, err := LoadPatternsTOML([]byte("[[pattern]]\nname = \"sharp\"\nsteps = 16\n[[pattern.track]]\ninstr = \"lead\"\nevent = [{pos = 0, vel = 0.9, deg = 1, oct = 1, acc = 1}]\n"))
	if err != nil || len(pats) != 1 || len(pats[0].Tracks) != 1 || pats[0].Tracks[0].Events[0].Acc != 1 {
		t.Fatalf("a document's acc: %v %+v", err, pats)
	}
	plain := &RipBar{Melody: []Step{{Pos: 0, Vel: 0.9, Deg: 1, Oct: 1}}}
	sharp := &RipBar{Melody: []Step{{Pos: 0, Vel: 0.9, Deg: 1, Oct: 1, Acc: 1}}}
	a := &Rip{BPM: 120, Root: 40, Scale: ScalePhrygian, Bars: []RipBar{*plain}}
	b := &Rip{BPM: 120, Root: 40, Scale: ScalePhrygian, Bars: []RipBar{*sharp}}
	if d := BarDistance(plain, sharp); d <= 0 || CompareRips(a, b).Melody >= 1 || CompareRips(b, b).Melody != 1 {
		t.Errorf("a sharp is another note: distance %.2f, melody %.2f", d, CompareRips(a, b).Melody)
	}
}

// TestRipReadsADetunedRecording: a recording tuned forty cents flat of A440 is
// heard on its own tuning, so its notes read as they do in tune rather than
// wobbling between the two semitones each sits between.
func TestRipReadsADetunedRecording(t *testing.T) {
	const bpm, bars = 140, 8
	ae, slots := startLoopEngine(t)
	buf, err := ae.RenderMusic(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := ExpectedRip(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil)
	inTune, err := Ripper{Params: DefaultRipParams(), Grid: &RipGrid{BPM: bpm}}.Samples(buf, AudioSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	// the same render read as if sampled slower: every pitch forty cents flat,
	// the tempo slower by the same ratio
	flat := math.Pow(2, -40.0/1200)
	rate := int(math.Round(AudioSampleRate * flat))
	detuned, err := Ripper{Params: DefaultRipParams(), Grid: &RipGrid{BPM: int(math.Round(bpm * flat))}}.Samples(buf, rate)
	if err != nil {
		t.Fatal(err)
	}
	a, b := CompareRips(inTune, want), CompareRips(detuned, want)
	t.Logf("in tune %+.1f cents %+v; detuned %+.1f cents %+v", inTune.Tuning, a, detuned.Tuning, b)
	if inTune.Tuning < -8 || inTune.Tuning > 8 || detuned.Tuning < -48 || detuned.Tuning > -32 {
		t.Errorf("tuning heard %+.1f and %+.1f cents, want about 0 and -40", inTune.Tuning, detuned.Tuning)
	}
	if b.Melody < a.Melody-0.1 || b.Bass < a.Bass-0.1 {
		t.Errorf("detuned melody %.2f bass %.2f against %.2f and %.2f in tune", b.Melody, b.Bass, a.Melody, a.Bass)
	}
	// the engine's own tuning: the loop rendered forty cents flat, as a corpus
	// loop standing in for such a recording is, reads so and keeps its notes
	ae.SetTuning(-40)
	low, err := ae.RenderMusic(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	ae.SetTuning(0)
	if err != nil {
		t.Fatal(err)
	}
	lowRip, err := Ripper{Params: DefaultRipParams(), Grid: &RipGrid{BPM: bpm}}.Samples(low, AudioSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	c := CompareRips(lowRip, want)
	t.Logf("rendered flat %+.1f cents %+v", lowRip.Tuning, c)
	if lowRip.Tuning < -48 || lowRip.Tuning > -32 || c.Melody < a.Melody-0.1 || c.Bass < a.Bass-0.1 || ae.Tuning() != 0 {
		t.Errorf("rendered flat: tuning %+.1f, melody %.2f bass %.2f, engine back at %+.0f", lowRip.Tuning, c.Melody, c.Bass, ae.Tuning())
	}
}

// TestRipOpensAPickupBar: a recording that begins inside a bar, notes leading
// into the first full one, gets a bar before it holding them, opening before
// the recording starts; the comparison skips it against a truth without one,
// and the notes' lag against the beats is taken out before they are quantized.
func TestRipOpensAPickupBar(t *testing.T) {
	const bpm, bars = 140, 8
	ae, slots := startLoopEngine(t)
	buf, err := ae.RenderMusic(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := ExpectedRip(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil)
	cut := 10 * SamplesPerBar(bpm, Meter{}) / StepsPerBar // the recording starts on the tenth step
	r, err := RipSamples(buf[cut:], AudioSampleRate, DefaultRipParams())
	if err != nil {
		t.Fatal(err)
	}
	score := CompareRips(r, want)
	t.Logf("downbeat %.2fs bars %d score %+v, pickup melody %v", r.Downbeat, len(r.Bars), score, r.Bars[0].Melody)
	if r.Downbeat >= 0 || len(r.Bars) != bars || len(r.Bars[0].Melody) < 2 {
		t.Fatalf("downbeat %.2fs, %d bars, pickup melody %v", r.Downbeat, len(r.Bars), r.Bars[0].Melody)
	}
	for _, st := range r.Bars[0].Melody {
		if st.Pos < 8 {
			t.Errorf("a note at step %d of the pickup bar, before the recording starts", st.Pos)
		}
	}
	if score.Melody < 0.7 || score.Bass < 0.7 {
		t.Errorf("score %+v", score)
	}
	// a truth with the same pickup bar grades itself perfect
	with := &Rip{BPM: bpm, Root: want.Root, Scale: want.Scale, Downbeat: -float64(BeatsPerBar*60) / bpm, Bars: append([]RipBar{{}}, want.Bars...)}
	if s := CompareRips(with, want); s.Total != 1 {
		t.Errorf("a pickup bar against none grades %+v", s)
	}
	g := ripGrid{lines: []int{0, 64}}
	if lag := g.lag([]ripNote{{start: 1}, {start: 17}, {start: 33}, {start: 49}}); lag != 0.25 {
		t.Errorf("lag %.2f steps, want 0.25", lag)
	}
	if _, ok := g.phase(70); ok {
		t.Error("a frame past the last beat has no phase")
	}
}

// TestRipHearsNothingBeforeTheMusic: what a front end hears in the silence before
// the music goes, and the music starts at its soft opening, not at its loudest.
func TestRipHearsNothingBeforeTheMusic(t *testing.T) {
	x := make([]float64, 100*RipHop)
	for i := range x {
		if i >= 40*RipHop {
			x[i] = 0.01 * math.Sin(0.1*float64(i)) // a soft opening, 40 dB down
		}
		if i >= 60*RipHop {
			x[i] = math.Sin(0.1 * float64(i))
		}
	}
	start := musicStart(x)
	if start != 40 {
		t.Fatalf("the music starts at frame %d, want 40", start)
	}
	hits, bass, melody := heardFrom(start, [InstrClap][]ripHit{InstrKick: {{frame: 12}, {frame: 38}, {frame: 50}}},
		[]ripNote{{start: 20}, {start: 41}}, []ripNote{{start: 39}})
	if len(hits[InstrKick]) != 2 || hits[InstrKick][0].frame != 38 || len(bass) != 1 || bass[0].start != 41 || len(melody) != 1 {
		t.Errorf("kept hits %v, bass %v, melody %v", hits[InstrKick], bass, melody)
	}
}

// TestMendPickupGivesAPartialFirstBarItsBeat: a first bar that starts partway
// takes the beat of the bar most like it over the steps it holds, its false hits
// gone, when one lies within the threshold; a first bar from its downbeat keeps
// its own.
func TestMendPickupGivesAPartialFirstBarItsBeat(t *testing.T) {
	at := func(pos ...int) []Step {
		out := make([]Step, len(pos))
		for i, p := range pos {
			out[i] = Step{Pos: p, Vel: 1}
		}
		return out
	}
	var first, full RipBar
	first.Drums = [InstrClap][]Step{at(8), at(8, 12), at(6, 8, 10, 12, 14)}
	full.Drums = [InstrClap][]Step{at(0, 8), at(4, 12), at(0, 2, 4, 8, 10, 12, 14)}
	r := &Rip{Bars: []RipBar{first, full, full}}
	got := r.MendPickup(0.4).Bars[0].Drums
	if want := [InstrClap][]Step{at(8), at(12), at(8, 10, 12, 14)}; !reflect.DeepEqual(got, want) {
		t.Errorf("mended to %v, want %v", got, want)
	}
	if !reflect.DeepEqual(r.Bars[0], first) || r.MendPickup(0.2) != r {
		t.Error("the rip changed, or mended past the threshold")
	}
	r.Bars[0].Drums[InstrKick] = at(0, 8)
	if r.MendPickup(0.4) != r {
		t.Error("a first bar from its downbeat was mended")
	}
}

// kitRip registers stand-in drums and returns a rip of eight bars of a beat on
// them at 120 bpm, a kick on one and three, a snare on two and four, and eighth
// hats, half of them where no other drum lands
func kitRip(t *testing.T) *Rip {
	t.Cleanup(ResetRegistries)
	ResetRegistries()
	for _, d := range []*SoundDef{
		{Name: "kick", Duration: 0.4, Norm: 0.95, Layer: []Layer{
			{Source: Source{Kind: "sweep", Wave: "sine", Freq: 160, FreqEnd: 50, Curve: "exp"}, Chain: []Proc{{Kind: "decay", Tau: 0.12}}},
			{Gain: 0.3, Length: 0.02, Source: Source{Kind: "noise"}, Chain: []Proc{{Kind: "decay", Tau: 0.004}}}}},
		{Name: "snare", Duration: 0.3, Norm: 0.75, Layer: []Layer{
			{Gain: 0.6, Source: Source{Kind: "osc", Wave: "sine", Freq: 190}, Chain: []Proc{{Kind: "decay", Tau: 0.05}}},
			{Source: Source{Kind: "noise"}, Chain: []Proc{{Kind: "bp", Freq: 2500, Q: 0.4}, {Kind: "decay", Tau: 0.07}}}}},
		{Name: "hihat", Duration: 0.15, Norm: 0.55, Layer: []Layer{
			{Source: Source{Kind: "noise"}, Chain: []Proc{{Kind: "hp", Freq: 5000}, {Kind: "decay", Tau: 0.02}}}}},
	} {
		if _, err := RegisterSound(d); err != nil {
			t.Fatal(err)
		}
	}
	at := func(vel float64, pos ...int) []Step {
		out := make([]Step, len(pos))
		for i, p := range pos {
			out[i] = Step{Pos: p, Vel: vel}
		}
		return out
	}
	r := &Rip{BPM: 120, Bars: make([]RipBar, 8), Beats: make([]float64, 33)}
	for i := range r.Beats {
		r.Beats[i] = 0.5 + 0.5*float64(i)
	}
	for b := range r.Bars {
		r.Bars[b].Drums = [InstrClap][]Step{at(1, 0, 8), at(0.9, 4, 12), at(0.7, 0, 2, 4, 6, 8, 10, 12, 14)}
	}
	return r
}

// toneOfHits reads the tone of y, a render of the rip's hits at AudioSampleRate,
// at the hits placed early seconds before they sound
func toneOfHits(r *Rip, y []float64, early float64) [InstrClap]RipTone {
	var at [InstrClap][]float64
	for _, h := range r.Hits() {
		at[h.Drum] = append(at[h.Drum], h.Start-early)
	}
	x := resample(y, AudioSampleRate, RipRate)
	return drumTones(x, y, AudioSampleRate, hitStarts(x, at))
}

// TestToneReadsBackAnEQ: a kit's hits rendered through a known curve, the eq a
// matched drum plays through, read back as that curve by the tone a rip reads at
// hits a front end placed three frames early, within 2 dB in each band a drum
// fills, within 15 dB of its strongest; a weaker band reads the window's leakage.
func TestToneReadsBackAnEQ(t *testing.T) {
	r := kitRip(t)
	var drums [InstrClap]*SoundDef
	for d := range drums {
		drums[d] = SoundDefByName(drumSoundNames[d])
	}
	for b := range r.Bars { // a drum a beat, each alone
		r.Bars[b].Drums = [InstrClap][]Step{}
		for beat := range 4 {
			d := (4*b + beat) % 3
			r.Bars[b].Drums[d] = append(r.Bars[b].Drums[d], Step{Pos: 4 * beat, Vel: 1})
		}
	}
	plain := beat(r.Hits(), drums)
	curve := make([]Band, ToneBands)
	for b := range curve {
		curve[b] = Band{Freq: toneCentre(b), DB: 6 * math.Sin(float64(b)/2)}
	}
	eqd := slices.Clone(plain)
	equalize(eqd, curve)
	own, got := toneOfHits(r, plain, 0), toneOfHits(r, eqd, 3*RipFrameSec)
	for d := range own {
		if own[d].Hits == 0 || got[d].Hits != own[d].Hits {
			t.Fatalf("%s read over %d and %d hits", drumSoundNames[d], own[d].Hits, got[d].Hits)
		}
		loudest := slices.Max(own[d].Bands[:])
		for b, c := range curve {
			if read := got[d].Bands[b] - own[d].Bands[b]; own[d].Bands[b] >= loudest-15 && math.Abs(read-c.DB) > 2 {
				t.Errorf("%s at %.0f Hz reads %+.1f dB through %+.1f", drumSoundNames[d], c.Freq, read, c.DB)
			}
		}
	}
}

// TestKitStandsAsTheRecordingsDrums: a rip's kit is the registered drums each with
// an eq, under the names its song plays them by and through a sound document, at
// the recording's balance against the kick within the bounds.
func TestKitStandsAsTheRecordingsDrums(t *testing.T) {
	r := kitRip(t)
	var drums [InstrClap]*SoundDef
	for d := range drums {
		drums[d] = SoundDefByName(drumSoundNames[d])
	}
	gain := [InstrClap]float64{1, 2, 0.25} // the recording's snare 6 dB up and hats 12 dB down
	var rec []float64
	for d := range drums {
		var one [InstrClap]*SoundDef
		one[d] = drums[d]
		y := beat(r.Hits(), one)
		rec = append(rec, make([]float64, max(len(y)-len(rec), 0))...)
		for i, v := range y {
			rec[i] += gain[d] * v
		}
	}
	r.Tone = toneOfHits(r, rec, 0)
	kit := r.Kit("x")
	if len(kit) != 3 || kit[0].Name != "x_kick" || kit[1].Name != "x_snare" || kit[2].Name != "x_hihat" {
		t.Fatalf("kit %v", kit)
	}
	var matched [InstrClap]*SoundDef
	for d, s := range kit {
		if last := s.Chain[len(s.Chain)-1]; last.Kind != "eq" || len(last.Bands) != ToneBands {
			t.Errorf("%s ends its chain with %+v", s.Name, last)
		}
		matched[d] = s
	}
	lv := toneOfHits(r, beat(r.Hits(), matched), 0)
	for d := InstrSnare; d < InstrClap; d++ {
		want := min(max(r.Tone[d].Level-r.Tone[InstrKick].Level, kitMatch[d].lo), kitMatch[d].hi)
		if got := lv[d].Level - lv[InstrKick].Level; math.Abs(got-want) > 0.5 {
			t.Errorf("%s stands %+.1f dB against the kick, the recording %+.1f", drumSoundNames[d], got, want)
		}
	}
	data, err := MarshalSounds(kit)
	if err != nil {
		t.Fatal(err)
	}
	back, err := LoadSoundsTOML(data)
	if err != nil || !reflect.DeepEqual(back.Sound, kit) {
		t.Errorf("the kit's document came back as %v", err)
	}
	_, song := r.Song("x", [RipParts][]int{r.Cluster(RipBeat, 0.4)})
	if want := map[string]string{"kick": "x_kick", "snare": "x_snare", "hihat": "x_hihat"}; !reflect.DeepEqual(song.Kit, want) || ValidateSong(song) != nil {
		t.Errorf("the song plays the kit %v", song.Kit)
	}
}

// TestFidelityHearsTheDifference: a render heard beside itself is wholly alike;
// the same bars with every melody note a semitone sharp are less alike in pitch
// while their onsets still are; a sound on a rip's beats reads as one on a grid;
// a sound needs one or the other; a Ripper of zero parameters rips with the
// defaults.
func TestFidelityHearsTheDifference(t *testing.T) {
	const bpm, bars = 140, 4
	ae, slots := startLoopEngine(t)
	same, err := ae.RenderMusic(slots, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	sharp, err := ae.DefinePattern(&Pattern{Name: "tune_sharp", Steps: 16, Tracks: []Track{{Instr: InstrLead, Events: []Step{
		{Pos: 0, Vel: 0.9, Deg: 0, Oct: 2, Acc: 1, Dur: 2}, {Pos: 2, Vel: 0.8, Deg: 1, Oct: 2, Acc: 1, Dur: 2}, {Pos: 4, Vel: 0.8, Deg: 2, Oct: 2, Acc: 1, Dur: 2}, {Pos: 6, Vel: 0.8, Deg: 3, Oct: 2, Acc: 1, Dur: 2},
		{Pos: 8, Vel: 0.9, Deg: 4, Oct: 2, Acc: 1, Dur: 2}, {Pos: 10, Vel: 0.8, Deg: 5, Oct: 2, Acc: 1, Dur: 2}, {Pos: 12, Vel: 0.8, Deg: 6, Oct: 2, Acc: 1, Dur: 2}, {Pos: 14, Vel: 0.9, Deg: 0, Oct: 3, Acc: 1, Dur: 2}}}}})
	if err != nil {
		t.Fatal(err)
	}
	moved := slots
	moved[1] = sharp
	other, err := ae.RenderMusic(moved, bpm, Meter{}, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	rp := Ripper{Params: DefaultRipParams(), Grid: &RipGrid{BPM: bpm}}
	a, err := rp.Sound(same, AudioSampleRate, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := rp.Sound(other, AudioSampleRate, nil)
	if err != nil {
		t.Fatal(err)
	}
	self, diff := Fidelity(a, a), Fidelity(b, a)
	t.Logf("itself %+v; a semitone sharp %+v", self, diff)
	if self.Total < 0.99 || diff.Pitch > self.Pitch-0.1 || diff.Onset < 0.8 {
		t.Errorf("itself %+v, a semitone sharp %+v", self, diff)
	}
	r, err := rp.Samples(same, AudioSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Ripper{Params: DefaultRipParams()}.Sound(same, AudioSampleRate, r.Beats)
	if err != nil {
		t.Fatal(err)
	}
	if f := Fidelity(c, a); f.Total < 0.99 {
		t.Errorf("on the rip's beats %+v", f)
	}
	if _, err := (Ripper{}).Sound(same, AudioSampleRate, nil); err == nil {
		t.Error("a sound with neither beats nor a grid was heard")
	}
	if z, err := (Ripper{Grid: &RipGrid{BPM: bpm}}).Samples(same, AudioSampleRate); err != nil || len(z.Bars) != bars || len(z.Notes) == 0 {
		t.Errorf("zero parameters: %v, %d bars", err, len(z.Bars))
	}
}

// TestABarPlaysInTheMeterAndTempoItNames: a song's bar that names 2/8 renders
// a quarter of a 4/4 bar and one naming 60bpm half as long again as at 120,
// the words holding until another bar names, and they survive its MIDI file
// TestARenderedBarStartsOnItsHead: a bar's first step sounds where its head
// puts it, after a slower and a faster bar alike
func TestARenderedBarStartsOnItsHead(t *testing.T) {
	ae, _ := startLoopEngine(t)
	id, err := ae.DefinePattern(&Pattern{Name: "tick", Steps: 16, Tracks: []Track{{Instr: InstrKick, Events: []Step{{Pos: 0, Vel: 1}}}}})
	if err != nil {
		t.Fatal(err)
	}
	heads := []BarHead{{Meter{}, 120}, {Meter{}, 60}, {Meter{}, 60}, {Meter{}, 180}, {Meter{}, 90}}
	timeline := make([][MusicSlots]PatternID, len(heads))
	for b := range timeline {
		timeline[b] = [MusicSlots]PatternID{id, PatternSilence, PatternSilence, PatternSilence}
	}
	buf, err := ae.RenderBars(timeline, heads, DefaultRootNote, ScaleMajor, nil, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	at := 0
	for b, h := range heads {
		n := SamplesPerBar(h.BPM, h.Meter)
		first := -1
		for i := at; i < at+n; i++ {
			if math.Abs(buf[i]) > 0.05 {
				first = i
				break
			}
		}
		if first < 0 || first-at > AudioSampleRate/100 {
			t.Errorf("bar %d at %d bpm: its kick sounds %d samples after its line", b, h.BPM, first-at)
		}
		at += n
	}
}

func TestABarPlaysInTheMeterAndTempoItNames(t *testing.T) {
	ae, _ := startLoopEngine(t)
	bass := &PatternDef{Name: "wbass", Steps: 16, Role: "bass", Track: []TrackDef{{Instr: "bass", Event: []StepDef{{Pos: 0, Vel: 1, Dur: 4}, {Pos: 8, Vel: 0.8, Deg: 4, Dur: 4}}}}}
	if p, err := bass.Pattern(); err != nil {
		t.Fatal(err)
	} else if _, err := ae.DefinePattern(p); err != nil {
		t.Fatal(err)
	}
	d := &SongDef{Name: "words", BPM: 120, Root: DefaultRootNote, Scale: "phrygian", Bars: [][]string{
		{"", "", "", bass.Name}, {"2/8", "", "", "", bass.Name}, {"4/4", "60bpm", "", "", "", bass.Name}, {"", "", "", bass.Name}}}
	if err := ValidateSong(d); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSong(&SongDef{Name: "x", BPM: 120, Scale: "major", Bars: [][]string{{"999bpm", bass.Name}}}); err == nil {
		t.Error("a tempo out of range validates")
	}
	tl, err := d.Timeline(PatternIDByName)
	if err != nil {
		t.Fatal(err)
	}
	buf, err := ae.RenderBars(tl, d.Heads(), d.Root, d.ScaleID(), nil, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := SamplesPerBar(120, Meter{}) + SamplesPerBar(120, Meter{2, 8}) + 2*SamplesPerBar(60, Meter{})
	if len(buf) != want {
		t.Errorf("rendered %d samples, want %d", len(buf), want)
	}
	var file bytes.Buffer
	if err := WriteSongMIDI(&file, d, lookup(bass)); err != nil {
		t.Fatal(err)
	}
	m, err := ReadMIDI(bytes.NewReader(file.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	_, back, _ := m.Song("back")
	var words []string
	for _, bar := range back.Bars {
		words = append(words, strings.Join(bar[:len(bar)-MusicSlots], " "))
	}
	if back.BPM != 120 || !slices.Equal(words, []string{"", "2/8", "4/4 60bpm", ""}) {
		t.Errorf("read back at %d bpm with bar words %q", back.BPM, words)
	}
}

// TestARipsSongNamesTheTempoWhereItsBarsMove: a rip whose bars slow by a
// tenth names the slowed bar's tempo and the recovered one's, and none for a
// bar within three per cent
func TestARipsSongNamesTheTempoWhereItsBarsMove(t *testing.T) {
	r := &Rip{BPM: 120, Root: DefaultRootNote, Scale: ScaleMajor, Bars: make([]RipBar, 4), Beats: make([]float64, 17)}
	secs := []float64{2, 2.02, 2.2, 2}
	for b, s := range secs {
		for j := range 4 {
			r.Beats[4*b+j+1] = r.Beats[4*b+j] + s/4
		}
		r.Bars[b].Bass = []Step{{Pos: 0, Vel: 1}}
	}
	_, song := r.Song("rub", [RipParts][]int{nil, nil, {0, 0, 0, 0}})
	var words []string
	for _, bar := range song.Bars {
		words = append(words, strings.Join(bar[:len(bar)-MusicSlots], " "))
	}
	if !slices.Equal(words, []string{"", "", "109bpm", "120bpm"}) {
		t.Errorf("bar words %q", words)
	}
}

// TestABarIsNamedByItsBeats: a bar of eight tracked beats is 4/4 at half the
// tempo and of six 3/4, of two beats 4/4 at double where that fits, of nine
// tracked triplets at a fast tempo 3/4 at two thirds; a bar of three beats with
// no drum part and no note on a sixteenth is 3/8 at half the tempo, a 6/8 so
// too, with a drum part or a sixteenth as read; four beats
// keep their quarter
func TestABarIsNamedByItsBeats(t *testing.T) {
	for _, c := range []struct {
		m                 Meter
		tempo             float64
		k                 int
		drums, sixteenths bool
		want              Meter
		wantTempo         float64
		heard             bool
	}{
		{Meter{4, 2}, 140, 2, true, true, Meter{}, 70, false},
		{Meter{3, 2}, 130, 2, false, false, Meter{3, 4}, 65, false},
		{Meter{2, 4}, 90, 1, true, false, Meter{}, 180, false},
		{Meter{2, 4}, 120, 1, true, false, Meter{2, 4}, 120, false},
		{Meter{9, 8}, 180, 1, true, false, Meter{3, 4}, 120, false},
		{Meter{3, 4}, 147, 2, false, false, Meter{3, 8}, 73.5, false},
		{Meter{3, 4}, 147, 2, true, false, Meter{3, 4}, 147, false},
		{Meter{3, 4}, 147, 2, false, true, Meter{3, 4}, 147, false},
		{Meter{}, 147, 2, false, false, Meter{}, 147, false},
		{Meter{6, 8}, 150, 3, false, false, Meter{3, 8}, 75, false},
		{Meter{6, 8}, 150, 3, true, false, Meter{6, 8}, 150, false},
		// beats that were heard keep their count: six stay 6/8, two stay 2/4, and
		// a drumless three with no sixteenth is still written in eighths
		{Meter{6, 8}, 150, 3, false, false, Meter{6, 8}, 150, true},
		{Meter{6, 8}, 180, 1, true, false, Meter{6, 8}, 180, true},
		{Meter{2, 4}, 90, 1, true, false, Meter{2, 4}, 90, true},
		{Meter{3, 4}, 147, 1, false, false, Meter{3, 8}, 73.5, true},
		{Meter{3, 4}, 147, 1, true, false, Meter{3, 4}, 147, true},
	} {
		if m, tempo := name(c.m, c.tempo, c.k, c.drums, c.sixteenths, c.heard); m != c.want || tempo != c.wantTempo {
			t.Errorf("%s at %.0f, %d a beat, drums %v sixteenths %v heard %v: named %s at %.1f, want %s at %.1f", c.m, c.tempo, c.k, c.drums, c.sixteenths, c.heard, m, tempo, c.want, c.wantTempo)
		}
	}
}

// TestRipHearsThreeEight: a piece in 3/8 whose melody runs in sixteenths over
// a bass note on each bar, no drums, rips in 3/8 at its own tempo, the bar
// lines on the render's
func TestRipHearsThreeEight(t *testing.T) {
	ae, _ := startLoopEngine(t)
	define := func(p *Pattern) PatternID {
		id, err := ae.DefinePattern(p)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	three := Meter{3, 8}
	tune := []Step{{Pos: 0, Vel: 0.9, Oct: 2, Dur: 1}, {Pos: 1, Vel: 0.7, Deg: 1, Oct: 2, Dur: 1}, {Pos: 2, Vel: 0.8, Deg: 2, Oct: 2, Dur: 1}, {Pos: 3, Vel: 0.7, Deg: 4, Oct: 2, Dur: 1}, {Pos: 4, Vel: 0.8, Deg: 3, Oct: 2, Dur: 1}, {Pos: 5, Vel: 0.7, Deg: 1, Oct: 2, Dur: 1}}
	slots := [MusicSlots]PatternID{PatternSilence,
		define(&Pattern{Name: "run", Steps: 6, Meter: three, Tracks: []Track{{Instr: InstrLead, Events: tune}}}),
		PatternSilence,
		define(&Pattern{Name: "run_low", Steps: 12, Meter: three, Tracks: []Track{{Instr: InstrBass, Events: []Step{{Pos: 0, Vel: 0.9, Dur: 3}, {Pos: 6, Vel: 0.9, Deg: 4, Dur: 3}}}}}),
	}
	const bpm, bars = 75, 24
	buf, err := ae.RenderMusic(slots, bpm, three, bars, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	r, err := RipSamples(buf, AudioSampleRate, DefaultRipParams())
	if err != nil {
		t.Fatal(err)
	}
	score := CompareRips(r, ExpectedRip(slots, bpm, three, bars, DefaultRootNote, ScalePhrygian, nil))
	if r.Meter != three || r.BPM < bpm-2 || r.BPM > bpm+2 || score.Bars < 0.85 {
		t.Errorf("ripped %s at %d bpm, %d bar(s), score %+v", r.Meter, r.BPM, len(r.Bars), score)
	}
}

// TestRipFollowsABarThatSlows: a waltz rendered with two bars a fifth slower
// in its middle keeps its bar lines on the render's through them, within a
// step, and its song names the slowed bars' tempo
func TestRipFollowsABarThatSlows(t *testing.T) {
	ae, _ := startLoopEngine(t)
	define := func(p *Pattern) PatternID {
		id, err := ae.DefinePattern(p)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	three := Meter{3, 4}
	slots := [MusicSlots]PatternID{
		define(&Pattern{Name: "slow", Steps: 12, Meter: three, Tracks: []Track{
			{Instr: InstrKick, Events: []Step{{Pos: 0, Vel: 1}}}, {Instr: InstrSnare, Events: []Step{{Pos: 4, Vel: 0.8}, {Pos: 8, Vel: 0.8}}}, {Instr: InstrHihat, Events: []Step{{Pos: 2, Vel: 0.5}, {Pos: 6, Vel: 0.5}, {Pos: 10, Vel: 0.5}}}}}),
		define(&Pattern{Name: "slow_tune", Steps: 12, Meter: three, Tracks: []Track{{Instr: InstrLead, Events: []Step{
			{Pos: 0, Vel: 0.9, Oct: 2, Dur: 4}, {Pos: 4, Vel: 0.8, Deg: 2, Oct: 2, Dur: 2}, {Pos: 6, Vel: 0.8, Deg: 4, Oct: 2, Dur: 2}, {Pos: 8, Vel: 0.8, Deg: 3, Oct: 2, Dur: 4}}}}}),
		PatternSilence,
		define(&Pattern{Name: "slow_low", Steps: 12, Meter: three, Tracks: []Track{{Instr: InstrBass, Events: []Step{{Pos: 0, Vel: 0.9, Dur: 8}}}}}),
	}
	const bars = 20
	timeline := make([][MusicSlots]PatternID, bars)
	heads := make([]BarHead, bars)
	for b := range timeline {
		timeline[b], heads[b] = slots, BarHead{three, 130}
		if b == 9 || b == 10 {
			heads[b].BPM = 104
		}
	}
	buf, err := ae.RenderBars(timeline, heads, DefaultRootNote, ScalePhrygian, nil, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := RipSamples(buf, AudioSampleRate, DefaultRipParams())
	if err != nil {
		t.Fatal(err)
	}
	lines := []float64{0}
	for _, h := range heads {
		lines = append(lines, lines[len(lines)-1]+float64(SamplesPerBar(h.BPM, h.Meter))/AudioSampleRate)
	}
	off := 0
	for b := range r.Bars {
		at := r.Beats[b*r.Meter.Beats()]
		i, _ := slices.BinarySearch(lines, at)
		near := math.Inf(1)
		for _, k := range []int{i - 1, i} {
			if k >= 0 && k < len(lines) {
				near = math.Min(near, math.Abs(at-lines[k]))
			}
		}
		if near > 60.0/130/StepsPerBeat {
			off++
		}
	}
	if r.Meter != three || off > 1 {
		t.Errorf("ripped %s, %d of %d bar lines more than a step off the render's", r.Meter, off, len(r.Bars))
	}
	_, song := r.Song("slow", [RipParts][]int{nil, nil, slices.Repeat([]int{0}, len(r.Bars))})
	named := 0
	for _, bar := range song.Bars {
		if strings.HasSuffix(bar[0], "bpm") {
			named++
		}
	}
	if named < 2 {
		t.Errorf("the song names a tempo on %d bar(s), want the slowed bars' and the recovered one's", named)
	}
	// the song's render, read on the grid its bars' tempos lay (RipGrid.Beats),
	// shares the rip's bar lines
	tl2, err := song.Timeline(PatternIDByName)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ae.RenderBars(tl2, song.Heads(), song.Root, song.ScaleID(), nil, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Ripper{Params: DefaultRipParams(), Grid: &RipGrid{BPM: song.BPM, Beats: song.BeatSeconds()}, Meter: &three}.Samples(again, AudioSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	if sc := CompareRips(back, r); back.Meter != three || sc.Bars < 0.9 {
		t.Errorf("the song read on its own beats: %s, score %+v", back.Meter, sc)
	}
}

// A bar line in a recording's first beats hears the change from the silence
// before it, as a line later hears the change from the four beats before.
func TestABarLineAtTheStartHearsTheSilenceBeforeIt(t *testing.T) {
	const n, span = 400, 100
	c := &ripLineCues{kick: make([]float64, n), snare: make([]float64, n), bass: make([]float64, n), bassChroma: make([][12]float64, n+1), chroma: make([][12]float64, n+1), beat: make([]float64, n)}
	for f := range n { // one chord from the first frame
		for pc := range c.chroma[f] {
			c.bassChroma[f+1][pc] = c.bassChroma[f][pc]
			c.chroma[f+1][pc] = c.chroma[f][pc]
		}
		c.chroma[f+1][0] += 1
		c.chroma[f+1][4] += 1
	}
	o := make([]float64, n)
	_, start, _ := c.bar(o, Meter{}, 0, 0, span, span)
	_, later, _ := c.bar(o, Meter{}, span, span, span, span)
	if math.Abs(start-later-1) > 1e-9 {
		t.Fatalf("a line at the start scores %.3f and one after four beats of the same chord %.3f, want the start a whole chroma change over the other", start, later)
	}
}

// TestTempoReadsNoLagFasterThan240BPM: three notes twenty frames apart on every
// beat of a slow pulse repeat most at their own spacing, 258 bpm, which folds to
// 129; the search starts at 240, and reads the pulse's half, 70, as 140. Onsets
// 199 frames apart, past the slowest lag, read a tempo in the tracker's range,
// where a search from lag 0 took that lag and its fold never returned.
func TestTempoReadsNoLagFasterThan240BPM(t *testing.T) {
	const figure, pulse = 20, 148
	f := &RipFrames{Hop: RipFrameSec, Flux: make([]float64, 30*pulse)}
	for at := 0; at+2*figure < f.frames(); at += pulse {
		f.Flux[at], f.Flux[at+figure], f.Flux[at+2*figure] = 1, 1, 1
	}
	if bpm, _ := f.tempo(DefaultRipParams()); math.Abs(bpm-140) > 2 {
		t.Errorf("tempo %.1f, want 140, the pulse's half doubled", bpm)
	}
	sparse := &RipFrames{Hop: RipFrameSec, Flux: make([]float64, 30*199)}
	for at := 0; at < sparse.frames(); at += 199 {
		sparse.Flux[at] = 1
	}
	if bpm, _ := sparse.tempo(DefaultRipParams()); bpm < ripTempoMin || bpm > ripTempoMax {
		t.Errorf("onsets 199 frames apart: tempo %.1f, outside %d..%d", bpm, ripTempoMin, ripTempoMax)
	}
}

// TestTempoAtTheSearchsBoundIsReadBetweenItsNeighbours: pairs of onsets 21.2
// frames apart, the only repetition within the search, peak at its first lag,
// 21, and are read between it and the lags either side, at the pair's own
// tempo folded, 121.9, not lag 21's 123.0
func TestTempoAtTheSearchsBoundIsReadBetweenItsNeighbours(t *testing.T) {
	const pair, every = 21.2, 151.0
	f := &RipFrames{Hop: RipFrameSec, Flux: make([]float64, 3000)}
	at := func(x float64) { // an onset between two frames, shared by them
		i := int(x)
		f.Flux[i], f.Flux[i+1] = f.Flux[i]+float64(i+1)-x, f.Flux[i+1]+x-float64(i)
	}
	for k := 0; float64(k)*every+pair+1 < 3000; k++ {
		at(float64(k)*every + 0.3)
		at(float64(k)*every + 0.3 + pair)
	}
	if bpm, _ := f.tempo(DefaultRipParams()); math.Abs(bpm-60/(pair*f.Hop)/2) > 0.5 {
		t.Errorf("tempo %.2f, want the pair's %.2f folded", bpm, 60/(pair*f.Hop)/2)
	}
}

// headFrames is n frames with an onset every on frames and heads that mark a
// beat every beat frames from first and a bar line on every per-th of them from
// the line-th, a mark a frame either side as the trainer's target is
func headFrames(n, on, beat, first, per, line int) *RipFrames {
	f := &RipFrames{Hop: RipFrameSec, Flux: make([]float64, n), Chroma: make([][12]float64, n), BassChroma: make([][12]float64, n),
		Beat: make([]float64, n), Downbeat: make([]float64, n)}
	for t := range n {
		f.Beat[t], f.Downbeat[t] = 0.01, 0.01
		if t%on == 0 {
			f.Flux[t] = 1
		}
	}
	for i, t := 0, first; t < n; i, t = i+1, t+beat {
		for k := max(t-1, 0); k <= t+1 && k < n; k++ {
			if f.Beat[k] = 0.98; i%per == line {
				f.Downbeat[k] = 0.98
			}
		}
	}
	return f
}

// every reports whether each frame of at lies within a frame of first plus a
// whole number of step
func every(at []int, first, step int) bool {
	for _, t := range at {
		if d := ((t-first)%step + step) % step; d > 1 && d < step-1 {
			return false
		}
	}
	return len(at) > 0
}

// TestTheTempoAndTheBeatsAreTheBeatHeads: onsets every twenty frames repeat most
// at forty, 129 bpm; a beat head marking every sixty sets the tempo at 86 and
// the beats on its marks, and the pulses are those beats cut in the three the
// onsets hold to a beat
func TestTheTempoAndTheBeatsAreTheBeatHeads(t *testing.T) {
	f := headFrames(3000, 20, 60, 20, 4, 0)
	bpm, tr := Ripper{}.track(f, DefaultRipParams())
	if math.Abs(bpm-60/(60*f.Hop)) > 1 || tr.k != 3 || !every(tr.pulses, 20, 20) {
		t.Fatalf("tracked %.1f bpm, %d pulses a beat, pulses %v: want 86.1, 3, every 20 frames", bpm, tr.k, tr.pulses[:min(len(tr.pulses), 8)])
	}
	if beats := tr.beats(); !every(beats, 20, 60) || len(beats) < 48 {
		t.Errorf("%d beats %v, want the head's, every 60 frames from 20", len(beats), beats[:min(len(beats), 8)])
	}
}

// beats are the tracked beats among a track's pulses
func (tr ripTrack) beats() (beats []int) {
	for i := 0; i < len(tr.pulses); i += tr.k {
		beats = append(beats, tr.pulses[i])
	}
	return beats
}

// TestTheTrackedBeatIsTheOneTheHeadMarks: a head's beat the prior reads at half
// or a third of its rate is tracked at its own where the head marks the halves
// or thirds between, and one folded to double at its own where the head marks
// every other beat alone, though it marks none over the clip's second half; a
// tempo given stands
func TestTheTrackedBeatIsTheOneTheHeadMarks(t *testing.T) {
	for _, c := range []struct{ beat, first, unmarkedFrom int }{{30, 0, 4000}, {15, 7, 4000}, {74, 30, 4000}, {43, 0, 4000}, {74, 30, 2000}, {30, 0, 2000}} {
		f := headFrames(4000, 20, c.beat, c.first, 4, 0)
		for t := c.unmarkedFrom; t < 4000; t++ {
			f.Beat[t] = 0.01
		}
		bpm, tr := Ripper{}.track(f, DefaultRipParams())
		if math.Abs(tr.period-float64(c.beat)) > 0.5 || math.Abs(bpm-60/(float64(c.beat)*f.Hop)) > 2 || !every(tr.beats(), c.first, c.beat) {
			t.Errorf("a head beat of %d frames, marked to frame %d, tracked at %.1f, %.1f bpm, beats %v", c.beat, c.unmarkedFrom, tr.period, bpm, tr.beats()[:4])
		}
	}
	f := headFrames(4000, 20, 30, 0, 4, 0)
	if bpm, tr := (Ripper{Grid: &RipGrid{BPM: 86, Downbeat: -1}}).track(f, DefaultRipParams()); bpm != 86 || math.Abs(tr.period-60) > 0.5 {
		t.Errorf("a tempo given as 86 tracked at %.1f bpm, a period of %.1f frames", bpm, tr.period)
	}
}

// barsOf decodes the bars of frames with no events but the hits given
func barsOf(t *testing.T, f *RipFrames, hits [InstrClap][]ripHit) (ripGrid, float64) {
	t.Helper()
	g, bpm, _, err := Ripper{}.grid(f, ripEvents{hits: hits}, DefaultRipParams())
	if err != nil {
		t.Fatal(err)
	}
	return g, bpm
}

// hitsEvery is a hit on every per-th beat from the first-th, beats beat frames apart
func hitsEvery(n, beat, per, first int) (hits []ripHit) {
	for i, t := 0, 0; t < n; i, t = i+1, t+beat {
		if i%per == first {
			hits = append(hits, ripHit{frame: t, strength: 1})
		}
	}
	return hits
}

// TestTheBarLineIsWhereTheDownbeatHeadIsLikeliest: a kick on every fourth beat
// and a snare two after it cue the line on the kick; a downbeat head that marks
// the beat after the kick puts every line there
func TestTheBarLineIsWhereTheDownbeatHeadIsLikeliest(t *testing.T) {
	const n, beat = 3000, 40
	f := headFrames(n, beat, beat, 0, 4, 1)
	hits := [InstrClap][]ripHit{InstrKick: hitsEvery(n, beat, 4, 0), InstrSnare: hitsEvery(n, beat, 4, 2)}
	g, bpm := barsOf(t, f, hits)
	if g.m != (Meter{}) || math.Abs(bpm-60/(beat*f.Hop)) > 1 || !every(g.lines, beat, 4*beat) || g.bars() < 16 {
		t.Errorf("%s at %.1f bpm, %d bars, lines %v: want 4/4 on the head's lines, every %d frames from %d", g.m, bpm, g.bars(), g.lines[:min(len(g.lines), 6)], 4*beat, beat)
	}
}

// TestAMeterIsTheBeatsHeardToItsBar: the reading whose lines the downbeat head
// scores highest is taken with no margin, and its bar holds the meter's own
// beats: three heard beats 3/4 and four 4/4 at the beats' tempo, six 6/8 at half
// of it, the quarter two of its eighths
func TestAMeterIsTheBeatsHeardToItsBar(t *testing.T) {
	for _, c := range []struct {
		beat, per int
		want      Meter
		tempo     float64
	}{{40, 4, Meter{}, 129.2}, {30, 3, Meter{3, 4}, 172.3}, {15, 6, Meter{6, 8}, 172.3}, {40, 3, Meter{3, 4}, 129.2}, {20, 6, Meter{6, 8}, 129.2}} {
		const n = 3000
		f := headFrames(n, c.beat, c.beat, 0, c.per, 2)
		g, bpm := barsOf(t, f, [InstrClap][]ripHit{InstrHihat: hitsEvery(n, c.beat, 1, 0)})
		if g.m != c.want || math.Abs(bpm-c.tempo) > 1.5 || !every(g.lines, 2*c.beat, c.per*c.beat) {
			t.Errorf("a line every %d beats of %d frames: %s at %.1f bpm, lines %v, want %s at %.1f", c.per, c.beat, g.m, bpm, g.lines[:min(len(g.lines), 5)], c.want, c.tempo)
		}
	}
}

// TestATempoGivenReadsTheMeterOfTheNoteTheHeadMarks: with the quarter given as
// 129, a head marking the quarters and a line every third reads 3/4, as the
// same frames read free, and one marking the eighths and a line every sixth
// reads 6/8 over the same bar
func TestATempoGivenReadsTheMeterOfTheNoteTheHeadMarks(t *testing.T) {
	const n = 3000
	for _, c := range []struct {
		beat, per int
		want      Meter
	}{{40, 3, Meter{3, 4}}, {20, 6, Meter{6, 8}}} {
		f := headFrames(n, c.beat, c.beat, 0, c.per, 1)
		hits := [InstrClap][]ripHit{InstrHihat: hitsEvery(n, c.beat, 1, 0)}
		g, bpm, _, err := Ripper{Grid: &RipGrid{BPM: 129, Downbeat: -1}}.grid(f, ripEvents{hits: hits}, DefaultRipParams())
		if err != nil || g.m != c.want || bpm != 129 || !every(g.lines, c.beat, c.per*c.beat) {
			t.Errorf("a head marking every %d frames, a line every %d: %s at %.1f, lines %v, %v; want %s at 129, a line every %d frames", c.beat, c.per, g.m, bpm, g.lines[:min(len(g.lines), 4)], err, c.want, c.per*c.beat)
		}
	}
}

// TestFiveHeardBeatsHoldABarWhateverTheOnsetsInsideThem: five beats heard 80
// frames apart, a line on the first and the fifth, are one bar of 4/4 read from
// the heads, the onsets on the beats alone or on their quarters too
func TestFiveHeardBeatsHoldABarWhateverTheOnsetsInsideThem(t *testing.T) {
	for _, on := range []int{80, 20} {
		g, _, tr, err := Ripper{}.grid(headFrames(380, on, 80, 0, 4, 0), ripEvents{}, DefaultRipParams())
		if err != nil || !tr.heard || g.bars() != 1 || !every(g.lines, 0, 320) {
			t.Errorf("an onset every %d frames, %d pulses a beat: %v, heard %v, lines %v, want one bar from the heads, lines 320 frames apart", on, tr.k, err, tr.heard, g.lines)
		}
	}
}

// TestABarIsReadWhereItsLinesTopMostOfItsBars: a waltz's lines marked at 0.55 or
// 0.45 over beats at 0.01, or at 0.8 with the last three tenths of the clip
// unmarked, and a 4/4's at 0.1 over 0.02 top most of their bars however loud the
// head, and are read so, not as twice their beats; a 4/4 marked over its first
// third alone tops no majority, and the cues read it
func TestABarIsReadWhereItsLinesTopMostOfItsBars(t *testing.T) {
	for _, c := range []struct {
		per          int
		line, rest   float64
		unmarkedFrom int
	}{{3, 0.55, 0.01, 3000}, {3, 0.45, 0.01, 3000}, {3, 0.8, 0.01, 2100}, {4, 0.1, 0.02, 3000}, {4, 0.8, 0.02, 1000}} {
		f := headFrames(3000, 40, 40, 0, c.per, 1)
		for t, v := range f.Downbeat {
			if f.Downbeat[t] = c.rest; v > 0.5 && t < c.unmarkedFrom {
				f.Downbeat[t] = c.line
			}
		}
		g, bpm := barsOf(t, f, [InstrClap][]ripHit{})
		if c.unmarkedFrom < 1500 {
			if cues, _ := barsOf(t, f.plain(), [InstrClap][]ripHit{}); !reflect.DeepEqual(g, cues) {
				t.Errorf("lines marked to frame %d alone: %s, lines %v, want the cues' %s, lines %v", c.unmarkedFrom, g.m, g.lines[:5], cues.m, cues.lines[:5])
			}
		} else if g.m.Beats() != c.per || !every(g.lines, 40, c.per*40) {
			t.Errorf("lines marked at %.2f over %.2f to frame %d: %s at %.1f, lines %v, want %d beats a bar, a line every %d frames from 40", c.line, c.rest, c.unmarkedFrom, g.m, bpm, g.lines[:min(len(g.lines), 5)], c.per, c.per*40)
		}
	}
}

// TestFramesWithoutTheHeadsDecodeByTheOnsetsAndTheCues: the frames of the head
// tests with neither head, or with one alone, are read as before there were
// any: the tempo the onsets repeat at, and the line on the kick
func TestFramesWithoutTheHeadsDecodeByTheOnsetsAndTheCues(t *testing.T) {
	for _, keep := range []string{"neither", "the beat head", "the downbeat head"} {
		f := headFrames(3000, 20, 60, 20, 4, 0)
		if keep != "the beat head" {
			f.Beat = nil
		}
		if keep != "the downbeat head" {
			f.Downbeat = nil
		}
		if bpm, tr := (Ripper{}).track(f, DefaultRipParams()); math.Abs(bpm-60/(40*f.Hop)) > 1 || !every(tr.pulses, 0, 20) {
			t.Errorf("with %s: tracked %.1f bpm, %d pulses a beat, want the onsets' 129.2", keep, bpm, tr.k)
		}
	}
	const n, beat = 3000, 40
	f := headFrames(n, beat, beat, 0, 4, 1)
	f.Beat, f.Downbeat = nil, nil
	hits := [InstrClap][]ripHit{InstrKick: hitsEvery(n, beat, 4, 0), InstrSnare: hitsEvery(n, beat, 4, 2)}
	if g, bpm := barsOf(t, f, hits); g.m != (Meter{}) || math.Abs(bpm-60/(beat*f.Hop)) > 1 || !every(g.lines, 0, 4*beat) {
		t.Errorf("%s at %.1f bpm, lines %v: want 4/4 on the kicks, every %d frames from 0", g.m, bpm, g.lines[:min(len(g.lines), 6)], 4*beat)
	}
}
