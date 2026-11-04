package ripmodel

import (
	"bytes"
	"math"
	"testing"

	"github.com/lixenwraith/audio"
	"github.com/lixenwraith/transformer"
)

// TestAnUntrainedModelHearsFramesTheRipReads is the contract between the model
// and the decoders: a model of Spec's shape, however weighted, yields frames on
// the rip's grid with every field the rip reads, and the rip runs on them; one
// from before the strike, release or velocity head loads without them, one of
// two channels hears too, and one of three is refused.
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
	fe, err := Load(&b)
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
		len(f.Voice) != n*audio.VoiceGeneCount || len(f.Wave) != n*len(audio.VoiceWaves) || len(f.Onset[2]) != n {
		t.Fatalf("frames %d, salience %d, strikes %d, releases %d, velocities %d, voice %d, wave %d",
			n, len(f.Salience), len(f.Strike), len(f.Release), len(f.Velocity), len(f.Voice), len(f.Wave))
	}
	if _, err := (audio.Ripper{Params: audio.DefaultRipParams(), FrontEnd: fe}).Samples(x, audio.AudioSampleRate); err != nil {
		t.Errorf("the rip refused the model's frames: %v", err)
	}
	for lack := 1; lack <= optionalHeads; lack++ {
		older := cfg
		older.Outputs = older.Outputs[:len(older.Outputs)-lack]
		o, _ := transformer.NewRandom(older, 1)
		ofe, err := New(o)
		if err != nil {
			t.Errorf("a model without its last %d heads: %v", lack, err)
			continue
		}
		of, err := ofe.Hear(x, audio.AudioSampleRate, audio.DefaultRipParams())
		if err != nil || of.Velocity != nil || (of.Release != nil) != (lack < 2) || (of.Strike != nil) != (lack < 3) {
			t.Errorf("a model without its last %d heads heard strikes %v, releases %v, velocities %v: %v", lack, of.Strike != nil, of.Release != nil, of.Velocity != nil, err)
		}
	}
	planes := cfg
	planes.Channels = 2
	p, _ := transformer.NewRandom(planes, 1)
	pfe, err := New(p)
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
	if _, err := New(p); err == nil {
		t.Error("a model of three channels passed")
	}
	wrong := cfg
	wrong.Outputs = wrong.Outputs[:3]
	w, _ := transformer.NewRandom(wrong, 1)
	if _, err := New(w); err == nil {
		t.Error("a model without the voice heads passed")
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
