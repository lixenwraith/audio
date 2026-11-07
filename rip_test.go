package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// startLoopEngine starts a null engine on the test bank with a snare, a hihat,
// saw voices and three patterns, a beat, a melody and a bass line, so a loop the
// engine renders of them has a rip a perfect listener would make
func startLoopEngine(t *testing.T) (*AudioEngine, [MusicSlots]PatternID) {
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

// TestLowNotesAreReadByStrikes: two low notes struck together both sound, as hard as
// the front end heard them, and the bass line's own note stands where no strike sounds.
func TestLowNotesAreReadByStrikes(t *testing.T) {
	const frames = 300
	f := &RipFrames{Hop: RipFrameSec, Flux: make([]float64, frames), Salience: make([]float32, frames*RipPitches),
		Strike: make([]float32, frames*RipPitches), Velocity: make([]float32, frames*RipPitches)}
	at := func(t, midi int) int { return t*RipPitches + midi - RipMinMIDI }
	for _, n := range []struct {
		midi, from, to int
		strike         float32
	}{{40, 10, 100, 0.99}, {47, 10, 100, 0.99}, {36, 150, 200, 0}} {
		f.Strike[at(n.from, n.midi)] = n.strike
		for t := n.from; t < n.to; t++ {
			f.Salience[at(t, n.midi)] = 0.9
			f.Velocity[at(t, n.midi)] = 0.4
		}
	}
	x := make([]float64, frames*RipHop)
	for i := range x {
		for _, m := range []float64{36, 40, 47} {
			x[i] += math.Sin(2 * math.Pi * 440 * math.Pow(2, (m-69)/12) * float64(i) / RipRate)
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
	ag := MatchNotes(a, b)
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
	ag := MatchNotes(a, b)
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
	rhythm := MatchNotes(heard, tab[:5])
	if l := rhythm.Lines[0]; !l.Rhythm || l.Matched != 4 || len(l.OnlyB) != 1 || l.Split != 1 || len(l.OnlyA) != 1 {
		t.Errorf("rhythm: matched %d of %d given, only given %d, only heard %d, split %d", l.Matched, l.B, len(l.OnlyB), len(l.OnlyA), l.Split)
	}
	if _, err := ParseTranscript("melody (1-2\n", 0, 0); err == nil {
		t.Error("a rhythm without a unit parsed")
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
// starts kept apart from it, and every note unmeasured.
func TestMIDIRoundTrip(t *testing.T) {
	in := []RipNote{
		{Start: 0, End: 1.2, MIDI: 40, Vel: 1, Bass: true},
		{Start: 0.1, End: 0.4, MIDI: 72, Vel: 0.8, Bright: 0.9},
		{Start: 0.4, End: 0.73, MIDI: 72, Vel: 0.5},
		{Start: 0.5, End: 1.1, MIDI: 67, Vel: 0.6, Second: true},
		{Start: 0.77, End: 1.234, MIDI: 76, Vel: 0.3},
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
		if n.MIDI != w.MIDI || n.Bass != w.Bass || n.Second != w.Second || n.Bright != RipUnmeasured ||
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
	}{
		{Meter{4, 2}, 140, 2, true, true, Meter{}, 70},
		{Meter{3, 2}, 130, 2, false, false, Meter{3, 4}, 65},
		{Meter{2, 4}, 90, 1, true, false, Meter{}, 180},
		{Meter{2, 4}, 120, 1, true, false, Meter{2, 4}, 120},
		{Meter{9, 8}, 180, 1, true, false, Meter{3, 4}, 120},
		{Meter{3, 4}, 147, 2, false, false, Meter{3, 8}, 73.5},
		{Meter{3, 4}, 147, 2, true, false, Meter{3, 4}, 147},
		{Meter{3, 4}, 147, 2, false, true, Meter{3, 4}, 147},
		{Meter{}, 147, 2, false, false, Meter{}, 147},
		{Meter{6, 8}, 150, 3, false, false, Meter{3, 8}, 75},
		{Meter{6, 8}, 150, 3, true, false, Meter{6, 8}, 150},
	} {
		if m, tempo := name(c.m, c.tempo, c.k, c.drums, c.sixteenths); m != c.want || tempo != c.wantTempo {
			t.Errorf("%s at %.0f, %d a beat, drums %v sixteenths %v: named %s at %.1f, want %s at %.1f", c.m, c.tempo, c.k, c.drums, c.sixteenths, m, tempo, c.want, c.wantTempo)
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
