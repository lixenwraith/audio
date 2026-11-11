package ripmodel

import (
	"bytes"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/lixenwraith/audio"
	"github.com/lixenwraith/transformer"
)

// TestAnUntrainedModelHearsFramesTheRipReads is the contract between the model
// and the decoders: a model of Spec's shape yields frames the rip runs on, with
// every field it reads and a frame's family shares summing to one; one without
// its strike, release, velocity or family head hears without them, one of two
// channels hears, one of three is refused, and a notes model gives the notes.
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
		len(f.Family) != n*len(audio.RipFamilies) {
		t.Fatalf("frames %d, salience %d, strikes %d, releases %d, velocities %d, voice %d, wave %d, family %d",
			n, len(f.Salience), len(f.Strike), len(f.Release), len(f.Velocity), len(f.Voice), len(f.Wave), len(f.Family))
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
		if err != nil || of.Family != nil || (of.Velocity != nil) != (lack < 2) || (of.Release != nil) != (lack < 3) || (of.Strike != nil) != (lack < 4) {
			t.Errorf("a model without its last %d heads heard strikes %v, releases %v, velocities %v, families %v: %v",
				lack, of.Strike != nil, of.Release != nil, of.Velocity != nil, of.Family != nil, err)
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

// TestTheEarHearsAsTheWholeRecording: the ear gives a recording's frames alike in
// 50 ms blocks and in blocks of any size, each with the bands MelStream gives it,
// and once its loudest sample is heard, every frame it holds has the whole's
// features, PCEN included, and the notes model's heads as heard whole, to
// float32's rounding.
func TestTheEarHearsAsTheWholeRecording(t *testing.T) {
	cfg := Spec()
	cfg.Dim, cfg.Heads, cfg.Layers, cfg.FFN, cfg.Window, cfg.Channels = 16, 2, 1, 32, 8, 2
	m, _ := transformer.NewRandom(cfg, 1)
	notes := NotesSpec()
	notes.Cells.Reduce, notes.Cells.Wide, notes.Cells.Cell, notes.Cells.Frame = 2, 3, 4, 5
	notes.Cells.Long = []int{3, 9, 27} // the trainer's, reaching past the half second the ear waits otherwise
	nm, _ := transformer.NewRandom(notes, 2)
	fe := &FrontEnd{Model: m, Notes: nm}
	x := make([]float64, 10*audio.RipRate)
	loudest := 0
	for i := range x { // a struck tone each half second, its level rising for four
		s := float64(i) / audio.RipRate
		f0 := 220 * math.Pow(2, float64(int(2*s)%5)/12)
		x[i] = 0.5 * math.Sin(2*math.Pi*f0*s) * math.Exp(-6*math.Mod(s, 0.5)) * min(0.1+0.225*s, 1)
		if math.Abs(x[i]) > math.Abs(x[loudest]) {
			loudest = i
		}
	}
	want := features(audio.MelFrames(x, audio.RipRate), true)
	whole, _ := fe.Hear(x, audio.RipRate, audio.DefaultRipParams())
	var bands []float32
	ms := audio.NewMelStream()
	for _, row := range append(ms.Write(x), ms.Close()...) {
		for _, v := range row {
			bands = append(bands, float32(v))
		}
	}
	rng := rand.New(rand.NewPCG(1, 2))
	var first [8][]float32
	var firstFlux []float64
	for _, block := range []func() int{func() int { return audio.RipRate / 20 }, func() int { return rng.IntN(audio.RipRate) + 1 }} {
		ea, _ := fe.Ear(audio.DefaultRipParams(), true)
		e := ea.(*ear)
		var flux []float64
		var heard [8][]float32 // the strikes, then each other field of every frame heard
		take := func(f *audio.RipFrames) {
			flux = append(flux, f.Flux...)
			for i, v := range [...][]float32{f.Strike, f.Salience, f.Release, f.Velocity, f.Wave, f.Voice, f.Family, f.Spectrum} {
				heard[i] = append(heard[i], v...)
			}
		}
		var worst float64
		for i := 0; i < len(x); {
			k := min(block(), len(x)-i)
			f, _ := e.Hear(x[i : i+k])
			take(f)
			if i += k; i <= loudest {
				continue
			}
			for k, row := range e.window() {
				for b, v := range row {
					worst = max(worst, math.Abs(float64(v-want[e.e0+k][b]))/max(math.Abs(float64(want[e.e0+k][b])), 1e-3))
				}
			}
		}
		f, _ := e.Close()
		take(f)
		from := 0 // the first frame of the first step heard once the loudest sample is in
		for (from+e.step+e.ahead-1)*audio.RipHop+audio.RipN/2 <= loudest {
			from += e.step
		}
		var off float64
		strikes := heard[0]
		for i := from * audio.RipPitches; i < len(whole.Strike) && i < len(strikes); i++ {
			off = max(off, math.Abs(float64(strikes[i]-whole.Strike[i])))
		}
		if worst > 1e-6 || off > 1e-5 || len(strikes) != len(whole.Strike) {
			t.Errorf("once the loudest is heard, features off by %.3g and strikes by %.3g; %d strikes heard of %d", worst, off, len(strikes), len(whole.Strike))
		}
		if !slices.Equal(heard[7], bands) {
			t.Errorf("the spectrum given, %d bands, is not MelStream's %d", len(heard[7]), len(bands))
		}
		if firstFlux == nil {
			firstFlux, first = flux, heard
		} else if !slices.Equal(firstFlux, flux) || !slices.EqualFunc(first[:], heard[:], slices.Equal) {
			t.Error("the frames heard depend on the blocks")
		}
	}
}
