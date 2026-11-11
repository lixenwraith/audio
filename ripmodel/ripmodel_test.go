package ripmodel

import (
	"bytes"
	"flag"
	"io"
	"maps"
	"math"
	"math/rand/v2"
	"os"
	"slices"
	"testing"

	"github.com/lixenwraith/audio"
	"github.com/lixenwraith/transformer"
)

// TestAnUntrainedModelHearsFramesTheRipReads is the contract between the model
// and the decoders: a model of Spec's shape yields frames the rip runs on, with
// every field it reads and a frame's family shares summing to one; one without
// its last heads, back to the strike's, hears without them; two channels hear,
// three are refused; a notes model gives the notes, with cents if trained so.
func TestAnUntrainedModelHearsFramesTheRipReads(t *testing.T) {
	cfg := Spec()
	cfg.Dim, cfg.Heads, cfg.Layers, cfg.FFN, cfg.Window = 16, 2, 1, 32, 8 // a small one for the test's sake
	m, err := transformer.NewRandom(cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := m.Save(&b); err != nil {
		t.Fatal(err)
	}
	fe, err := Load(&b, nil)
	if err != nil {
		t.Fatal(err)
	}
	x := make([]float64, 5*audio.AudioSampleRate)
	for i := range x {
		x[i] = 0.3 * math.Sin(float64(i)*2*math.Pi*220/audio.AudioSampleRate)
	}
	f, err := fe.Hear(x, audio.AudioSampleRate, audio.DefaultRipParams())
	if err != nil {
		t.Fatal(err)
	}
	n := len(f.Flux)
	per := n * audio.RipPitches
	if n < 400 || len(f.Salience) != per || len(f.Strike) != per || len(f.Release) != per || len(f.Velocity) != per ||
		len(f.Voice) != n*audio.VoiceGeneCount || len(f.Wave) != n*len(audio.VoiceWaves) || len(f.Onset[2]) != n ||
		len(f.Family) != n*len(audio.RipFamilies) || len(f.Beat) != n || len(f.Downbeat) != n {
		t.Fatalf("frames %d, salience %d, strikes %d, releases %d, velocities %d, voice %d, wave %d, family %d, beat %d, downbeat %d",
			n, len(f.Salience), len(f.Strike), len(f.Release), len(f.Velocity), len(f.Voice), len(f.Wave), len(f.Family), len(f.Beat), len(f.Downbeat))
	}
	if out := m.Forward(fe.planes(features(audio.MelFrames(x, audio.AudioSampleRate), false))); f.Beat[n/2] != sigmoid(m.Slice(out[n/2], HeadBeat)[0]) ||
		f.Downbeat[n/2] != sigmoid(m.Slice(out[n/2], HeadDownbeat)[0]) {
		t.Errorf("frame %d's beat %g and downbeat %g are not the heads' sigmoids", n/2, f.Beat[n/2], f.Downbeat[n/2])
	}
	for at := 0; at < len(f.Family); at += len(audio.RipFamilies) {
		var sum float64
		for _, v := range f.Family[at : at+len(audio.RipFamilies)] {
			sum += float64(v)
		}
		if math.Abs(sum-1) > 1e-5 {
			t.Fatalf("frame %d's family shares sum to %g", at/len(audio.RipFamilies), sum)
		}
	}
	if _, err := (audio.Ripper{Params: audio.DefaultRipParams(), FrontEnd: fe}).Samples(x, audio.AudioSampleRate); err != nil {
		t.Errorf("the rip refused the model's frames: %v", err)
	}
	for lack := 1; lack <= optionalHeads; lack++ {
		older := cfg
		older.Outputs = older.Outputs[:len(older.Outputs)-lack]
		o, _ := transformer.NewRandom(older, 1)
		ofe, err := New(o, nil)
		if err != nil {
			t.Errorf("a model without its last %d heads: %v", lack, err)
			continue
		}
		of, err := ofe.Hear(x, audio.AudioSampleRate, audio.DefaultRipParams())
		has := []bool{of.Downbeat != nil, of.Beat != nil, of.Family != nil, of.Velocity != nil, of.Release != nil, of.Strike != nil}
		for i, h := range has {
			if err != nil || h != (lack <= i) {
				t.Errorf("a model without its last %d heads heard downbeats, beats, families, velocities, releases, strikes %v: %v", lack, has, err)
				break
			}
		}
	}
	planes := cfg
	planes.Channels = 2
	p, _ := transformer.NewRandom(planes, 1)
	pfe, err := New(p, nil)
	if err != nil {
		t.Fatalf("a model of two channels: %v", err)
	}
	if pf, err := pfe.Hear(x, audio.AudioSampleRate, audio.DefaultRipParams()); err != nil {
		t.Errorf("a model of two channels: %v", err)
	} else if len(pf.Flux) != n {
		t.Errorf("a model of two channels heard %d frames of %d", len(pf.Flux), n)
	}
	planes.Channels = 3
	p, _ = transformer.NewRandom(planes, 1)
	if _, err := New(p, nil); err == nil {
		t.Error("a model of three channels passed")
	}
	wrong := cfg
	wrong.Outputs = wrong.Outputs[:3]
	w, _ := transformer.NewRandom(wrong, 1)
	if _, err := New(w, nil); err == nil {
		t.Error("a model without the voice heads passed")
	}

	notes := NotesSpec()
	notes.Cells.Reduce, notes.Cells.Wide, notes.Cells.Cell, notes.Cells.Frame = 2, 3, 4, 5
	nm, _ := transformer.NewRandom(notes, 2)
	var nb bytes.Buffer
	nm.Save(&nb)
	m.Save(&b)
	nfe, err := Load(&b, &nb)
	if err != nil {
		t.Fatalf("a model and a notes model: %v", err)
	}
	nf, err := nfe.Hear(x, audio.AudioSampleRate, audio.DefaultRipParams())
	if err != nil || len(nf.Flux) != n {
		t.Fatalf("a model and a notes model heard %d frames of %d: %v", len(nf.Flux), n, err)
	}
	heard := nm.Forward(stacked(features(audio.MelFrames(x, audio.AudioSampleRate), true)))
	for i, v := range nm.Slice(heard[n/2], HeadStrike) {
		if s := float32(sigmoid(v)); nf.Strike[n/2*audio.RipPitches+i] != s || nf.Salience[n/2*audio.RipPitches+i] == f.Salience[n/2*audio.RipPitches+i] {
			t.Fatalf("pitch %d struck at %g, the notes model says %g", i, nf.Strike[n/2*audio.RipPitches+i], s)
		}
	}
	if !slices.Equal(nf.Voice, f.Voice) || !slices.Equal(nf.Onset[0], f.Onset[0]) || !slices.Equal(nf.Family, f.Family) {
		t.Error("the notes model moved the voice, the kicks or the family")
	}
	at, c := n/2*audio.RipPitches+7, nm.Slice(heard[n/2], HeadCents)
	if f.Cents != nil || len(nf.Cents) != per || len(nf.CentsError) != per || nf.Cents[at] != 100*c[7] ||
		nf.CentsError[at] != float32(100*math.Exp(float64(c[audio.RipPitches+7]))) {
		t.Errorf("%d cents and %d errors of %d, the model alone %d", len(nf.Cents), len(nf.CentsError), per, len(f.Cents))
	}
	before := NotesSpec()
	before.Cells.Reduce, before.Cells.Wide, before.Cells.Cell, before.Cells.Frame, before.Cells.Heads = 2, 3, 4, 5, 4
	before.Outputs = slices.DeleteFunc(before.Outputs, func(h transformer.Head) bool { return h.Name == HeadCents })
	bm, _ := transformer.NewRandom(before, 2)
	if bfe, err := New(m, bm); err != nil {
		t.Errorf("a notes model trained before the cents head: %v", err)
	} else if bf, _ := bfe.Hear(x, audio.AudioSampleRate, audio.DefaultRipParams()); bf.Strike == nil || bf.Cents != nil || bf.CentsError != nil {
		t.Error("a notes model trained before the cents head heard cents, or no strikes")
	}
	older := cfg
	older.Outputs = older.Outputs[:len(older.Outputs)-optionalHeads]
	o, _ := transformer.NewRandom(older, 1)
	if ofe, err := New(o, nm); err != nil {
		t.Errorf("a model without the strike heads beside a notes model: %v", err)
	} else if of, err := ofe.Hear(x, audio.AudioSampleRate, audio.DefaultRipParams()); err != nil || of.Strike == nil || of.Velocity == nil {
		t.Errorf("a model without the strike heads beside a notes model heard no strikes: %v", err)
	}
	if _, err := New(m, m); err == nil {
		t.Error("a notes model that reads no harmonic stack passed")
	}
	notes.Outputs = slices.DeleteFunc(notes.Outputs, func(h transformer.Head) bool { return h.Name == HeadVoicing })
	nv, _ := transformer.NewRandom(notes, 2)
	if _, err := New(m, nv); err == nil {
		t.Error("a notes model without voicing passed")
	}
}

// TestTheStackLaysAPitchsHarmonicsOnItsBin: a plane reading each band's log
// frequency, which the interpolation keeps exact, reads in the channel of
// harmonic h at a pitch's bin that pitch's frequency 2^(shift/36) times, each
// channel ending in its plane's scalar and holding zero past the bands.
func TestTheStackLaysAPitchsHarmonicsOnItsBin(t *testing.T) {
	mel := func(hz float64) float64 { return 2595 * math.Log10(1+hz/700) }
	hz := func(m float64) float64 { return 700 * (math.Pow(10, m/2595) - 1) }
	n := audio.RipMelBands
	frame := make([]float32, 2*(n+1))
	for b := range n {
		c := math.Log(hz(mel(30) + (mel(audio.RipRate/2)-mel(30))*float64(b+1)/float64(n+1)))
		frame[b], frame[n+1+b] = float32(c), float32(2*c)
	}
	frame[n], frame[2*n+1] = 0.25, 0.5
	got := stacked([][]float32{frame})[0]
	lo, hi := math.Log(hz(mel(30)+(mel(audio.RipRate/2)-mel(30))/float64(n+1))), math.Log(hz(mel(30)+(mel(audio.RipRate/2)-mel(30))*float64(n)/float64(n+1)))
	var reached int
	for c := range stackChannels {
		plane, shift := float64(1+c/8), stackShifts[c%8]
		row := got[c*(stackBins+1):][:stackBins+1]
		if row[stackBins] != frame[n+c/8*(n+1)] {
			t.Fatalf("channel %d ends in %g", c, row[stackBins])
		}
		for k, v := range row[:stackBins] {
			f := math.Log(440) + math.Ln2*(float64(stackRootMIDI)+float64(k+shift)/3-69)/12
			want := plane * f
			if f < lo || f >= hi {
				want = 0
			} else {
				reached++
			}
			if math.Abs(float64(v)-want) > 1e-5*math.Abs(want) {
				t.Fatalf("channel %d bin %d reads %g, want %g", c, k, v, want)
			}
		}
	}
	if reached < stackChannels*stackBins*3/4 {
		t.Errorf("the bands reach %d of %d bins", reached, stackChannels*stackBins)
	}
}

// TestAGainMovesOnlyTheLevel: the model's input keeps a frame's bands level-free,
// so the same sound 20 dB softer differs only in the level input, by 20 dB, while
// it stays well over the floor.
func TestAGainMovesOnlyTheLevel(t *testing.T) {
	loud, soft := make([]float32, audio.RipMelBands), make([]float32, audio.RipMelBands)
	for b := range loud {
		e := math.Pow(10, -float64(b%5)) // bands from full scale to 40 dB down
		loud[b], soft[b] = float32(math.Log1p(100*e)), float32(math.Log1p(100*e/100))
	}
	f := features([][]float32{loud, soft}, false)
	for b := range audio.RipMelBands {
		if d := math.Abs(float64(f[0][b] - f[1][b])); d > 2e-3 {
			t.Fatalf("band %d moved by %.4f with the gain", b, d)
		}
	}
	if d := float64(f[0][audio.RipMelBands]-f[1][audio.RipMelBands]) * 80; math.Abs(d-20) > 0.1 {
		t.Errorf("the level input moved %.2f dB for a 20 dB gain", d)
	}
}

// TestPCENHoldsWhatIsSteadyAndLiftsAStrike: a steady band reads nearly the same
// 40 dB softer, a band fading under a pedal falls away, and an attack stands far
// over what it strikes through, even one only 6 dB over a band already sounding.
func TestPCENHoldsWhatIsSteadyAndLiftsAStrike(t *testing.T) {
	const frames, at, band = 200, 150, 7 // 2.3 s, read at 1.7 s
	perSecond := audio.RipRate / audio.RipHop
	// the band's PCEN at frame at, from its energy per frame, the others silent
	pcen := func(energy func(f int) float64) float32 {
		mel := make([][]float32, frames)
		for f := range mel {
			mel[f] = make([]float32, audio.RipMelBands)
			mel[f][band] = float32(math.Log1p(100 * energy(f)))
		}
		return features(mel, true)[at][audio.RipMelBands+1+band]
	}
	step := func(before, after float64) float32 {
		return pcen(func(f int) float64 {
			if f < at {
				return before
			}
			return after
		})
	}
	held := step(1, 1)
	if soft := step(1e-4, 1e-4); soft < 0.8*held || soft > held {
		t.Errorf("a steady band reads %.3f, and %.3f 40 dB softer", held, soft)
	}
	if fade := pcen(func(f int) float64 { return math.Pow(10, -2*float64(max(f-at+perSecond, 0))*audio.RipFrameSec) }); fade > held/4 {
		t.Errorf("a band fading 20 dB a second for a second reads %.3f, held %.3f", fade, held)
	}
	if s, under := step(0.25, 1), step(0.25, 0.25); s < 2.5*under {
		t.Errorf("a strike 6 dB over a sounding band reads %.3f over its %.3f", s, under)
	}
	if s := step(0, 1e-2); s < 10*held {
		t.Errorf("a strike 20 dB under full scale from silence reads %.3f, a full-scale band held %.3f", s, held)
	}
}

var (
	trained      = flag.String("model", "", "a trained model's weight file, for TestTheEarHearsAsTheWholeRecording to read its distances on")
	trainedNotes = flag.String("notes", "", "its notes model's weight file")
	recording    = flag.String("wav", "", "the recording to read them over")
)

// struck is sec seconds of a struck tone each half second, its level rising for
// four and a half and lower from there, and its loudest sample
func struck(sec int) (x []float64, loudest int) {
	x = make([]float64, sec*audio.RipRate)
	for i := range x {
		s := float64(i) / audio.RipRate
		f0, level := 220*math.Pow(2, float64(int(2*s)%5)/12), 0.8
		if s < 4.5 {
			level = min(0.1+0.225*s, 1)
		}
		x[i] = 0.5 * math.Sin(2*math.Pi*f0*s) * math.Exp(-6*math.Mod(s, 0.5)) * level
		if math.Abs(x[i]) > math.Abs(x[loudest]) {
			loudest = i
		}
	}
	return x, loudest
}

// modelHeads are the heads a notes model leaves the model, as the rip reads them
var modelHeads = []string{HeadOnset, HeadDrums, HeadWave, HeadVoice, HeadFamily, HeadBeat, HeadDownbeat}

// byHead appends frames' values to to, by the head each is read from, a frame's
// after another's, and the spectrum under its own name
func byHead(to map[string][]float32, f *audio.RipFrames) {
	for t, v := range f.Flux {
		to[HeadOnset] = append(to[HeadOnset], float32(v))
		to[HeadBeat], to[HeadDownbeat] = append(to[HeadBeat], float32(f.Beat[t])), append(to[HeadDownbeat], float32(f.Downbeat[t]))
		for d := range f.Onset {
			to[HeadDrums] = append(to[HeadDrums], float32(f.Onset[d][t]))
		}
	}
	for head, v := range map[string][]float32{HeadPitch: f.Salience, HeadStrike: f.Strike, HeadRelease: f.Release, HeadVelocity: f.Velocity,
		HeadWave: f.Wave, HeadVoice: f.Voice, HeadFamily: f.Family, "spectrum": f.Spectrum} {
		to[head] = append(to[head], v...)
	}
	// the cents in cells, the head's own scale, where one bar of rounding fits every head
	for head, v := range map[string][]float32{HeadCents: f.Cents, HeadCents + " error": f.CentsError} {
		for _, c := range v {
			to[head] = append(to[head], c/100)
		}
	}
}

// through is x through an ear of fe in blocks of block's sizes: every frame it
// gives, by head, each step given as soon as wait frames follow it
func through(t *testing.T, fe *FrontEnd, x []float64, wait int, block func() int) map[string][]float32 {
	e, _ := fe.Ear(audio.DefaultRipParams(), true)
	heard, step := map[string][]float32{}, int(math.Round(earStep/audio.RipFrameSec))
	for i := 0; i < len(x); {
		k := min(block(), len(x)-i)
		f, _ := e.Hear(x[i : i+k])
		byHead(heard, f)
		i += k
		if want := max((i-audio.RipN/2)/audio.RipHop+1-wait, 0) / step * step; len(heard[HeadOnset]) != want {
			t.Fatalf("%d frames given after %d samples, waiting %d frames: want %d", len(heard[HeadOnset]), i, wait, want)
		}
	}
	f, _ := e.Close()
	byHead(heard, f)
	return heard
}

// apart is how far a's values lie from b's from frame from on, rms and worst,
// and b's rms there
func apart(a, b []float32, frames, from int) (rms, worst, of float64) {
	per := len(b) / frames
	for i := from * per; i < len(b); i++ {
		d := float64(a[i]) - float64(b[i])
		rms, worst, of = rms+d*d, max(worst, math.Abs(d)), of+float64(b[i])*float64(b[i])
	}
	n := float64(len(b) - from*per)
	return math.Sqrt(rms / n), worst, math.Sqrt(of / n)
}

// TestTheEarHearsAsTheWholeRecording: an ear gives a recording's frames alike
// whatever the blocks, each with MelStream's bands, a step once the model's
// reach follows it, and from a model's reach past the loudest sample each of its
// heads as heard whole, to rounding. Waiting half a second it holds the notes
// model's so and logs how far the model's lie; -model, -notes, -wav log both.
func TestTheEarHearsAsTheWholeRecording(t *testing.T) {
	cfg := Spec()
	cfg.Dim, cfg.Heads, cfg.Layers, cfg.FFN, cfg.Window, cfg.Channels = 16, 2, 2, 32, 32, 2
	m, _ := transformer.NewRandom(cfg, 1)
	notes := NotesSpec()
	notes.Cells.Reduce, notes.Cells.Wide, notes.Cells.Cell, notes.Cells.Frame = 2, 3, 4, 5
	notes.Cells.Long = []int{3, 9, 27} // the trainer's
	nm, _ := transformer.NewRandom(notes, 2)
	fe := &FrontEnd{Model: m, Notes: nm}
	x, loudest := struck(10)
	if *trained != "" {
		fe, x, loudest = trainedEar(t)
	}
	whole, _ := fe.Hear(x, audio.RipRate, audio.DefaultRipParams())
	want, frames := map[string][]float32{}, len(whole.Flux)
	byHead(want, whole)
	ms := audio.NewMelStream()
	for _, row := range append(ms.Write(x), ms.Close()...) {
		for _, v := range row {
			want["spectrum"] = append(want["spectrum"], float32(v))
		}
	}
	rng := rand.New(rand.NewPCG(1, 2))
	fixed, random := func() int { return audio.RipHop }, func() int { return rng.IntN(audio.RipRate) + 1 }
	reach, notesReach, half := fe.Model.Reach(), fe.Notes.Reach(), int(math.Round(0.5/audio.RipFrameSec))
	for _, wait := range []int{reach, max(half, notesReach)} {
		fe.Ahead = 0
		if wait < reach {
			fe.Ahead = 0.5
		}
		heard := through(t, fe, x, wait, fixed)
		if !slices.Equal(heard["spectrum"], want["spectrum"]) {
			t.Errorf("waiting %d frames, the spectrum given, %d bands, is not MelStream's %d", wait, len(heard["spectrum"]), len(want["spectrum"]))
		}
		if wait == reach && *trained == "" && !maps.EqualFunc(heard, through(t, fe, x, wait, random), slices.Equal[[]float32]) {
			t.Error("the frames heard depend on the blocks")
		}
		for head, v := range want {
			from, exact := loudest/audio.RipHop+1+notesReach, true
			if slices.Contains(modelHeads, head) {
				from, exact = loudest/audio.RipHop+1+reach, wait == reach
			}
			if head == "spectrum" || len(v) == 0 {
				continue
			}
			if len(heard[head]) != len(v) || from >= frames {
				t.Fatalf("waiting %d frames, %d values of %s heard of %d, read from frame %d of %d", wait, len(heard[head]), head, len(v), from, frames)
			}
			switch rms, worst, of := apart(heard[head], v, frames, from); {
			case !exact || *trained != "":
				t.Logf("waiting %d frames, %s lies %.3g rms and %.3g at worst from the whole's, of rms %.3g", wait, head, rms, worst, of)
			case worst > 1e-5:
				t.Errorf("waiting %d frames, %s lies %.3g from the whole's from frame %d on", wait, head, worst, from)
			}
		}
	}
}

// trainedEar is the front end of the -model and -notes files and the -wav
// recording at RipRate, with its loudest sample
func trainedEar(t *testing.T) (*FrontEnd, []float64, int) {
	var files [2]io.Reader
	for i, path := range []string{*trained, *trainedNotes} {
		if path == "" {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files[i] = bytes.NewReader(b)
	}
	fe, err := Load(files[0], files[1])
	if err != nil || fe.Notes == nil {
		t.Fatalf("-model and -notes: %v", err)
	}
	f, err := os.Open(*recording)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	x, rate, err := audio.ReadWAV(f)
	if err != nil {
		t.Fatal(err)
	}
	x, loudest := audio.Resample(x, rate, audio.RipRate), 0
	for i, v := range x {
		if math.Abs(v) > math.Abs(x[loudest]) {
			loudest = i
		}
	}
	return fe, x, loudest
}

// BenchmarkEar is a core's seconds a second of audio (-cpu 1) through Spec's
// models over two channels, the notes model with the trainer's long
// convolutions: thirty seconds heard whole, and through an ear in 50 ms blocks
// waiting the model's reach and waiting half a second.
func BenchmarkEar(b *testing.B) {
	cfg := Spec()
	cfg.Channels = 2
	m, _ := transformer.NewRandom(cfg, 1)
	notes := NotesSpec()
	notes.Cells.Long = []int{3, 9, 27}
	nm, _ := transformer.NewRandom(notes, 2)
	const sec = 30
	x, _ := struck(sec)
	for _, c := range []struct {
		name  string
		ahead float64
	}{{"whole", 0}, {"reach", 0}, {"half", 0.5}} {
		b.Run(c.name, func(b *testing.B) {
			fe := &FrontEnd{Model: m, Notes: nm, Ahead: c.ahead}
			for b.Loop() {
				if c.name == "whole" {
					fe.Hear(x, audio.RipRate, audio.DefaultRipParams())
					continue
				}
				e, _ := fe.Ear(audio.DefaultRipParams(), false)
				for i, block := 0, audio.RipRate/20; i < len(x); i += block {
					e.Hear(x[i:min(i+block, len(x))])
				}
				e.Close()
			}
			b.ReportMetric(b.Elapsed().Seconds()/float64(b.N)/sec, "s/s")
		})
	}
}
