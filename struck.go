package audio

import (
	"math"
	"math/rand/v2"
)

// The genes by row of struckGenes
const (
	struckPitch = iota
	struckFall
	struckFallRate
	struckDecay
	struckMetal
	struckNoise
	struckNoiseHz
	struckNoiseWidth
	struckNoiseDecay
	struckClick
	struckBursts
	struckGap
	struckDrive
)

// struckGene is one gene of a struck sound: its name, the range its value in
// [0, 1] is read over, and whether on a log scale
type struckGene struct {
	name     string
	min, max float64
	log      bool
}

// struckGenes is a struck sound's genome, a row a gene; doc/audio.md §5 gives
// each range's source
var struckGenes = [...]struckGene{
	struckPitch:      {"pitch", 30, 2800, true},
	struckFall:       {"fall", 0.25, 1, true},
	struckFallRate:   {"fall_rate", 1, 9, true},
	struckDecay:      {"decay", 0.01, 1, true},
	struckMetal:      {"metal", 0, 1, false},
	struckNoise:      {"noise", 0, 1, false},
	struckNoiseHz:    {"noise_hz", 30, 10000, true},
	struckNoiseWidth: {"noise_width", 0.5, 8.5, true},
	struckNoiseDecay: {"noise_decay", 0.01, 1, true},
	struckClick:      {"click", 0, 4, false},
	struckBursts:     {"bursts", 1, 5, false},
	struckGap:        {"gap", 0.002, 0.015, true},
	struckDrive:      {"drive", 0, 4, false},
}

// StruckGeneCount is the length of a struck sound's gene vector
const StruckGeneCount = len(struckGenes)

// StruckGeneNames is the genes' names in gene order
func StruckGeneNames() []string {
	out := make([]string, StruckGeneCount)
	for i, g := range struckGenes {
		out[i] = g.name
	}
	return out
}

// The body's modes over its pitch: a membrane's, the ideal circular membrane's
// first six (Bessel zeros over the first), and a plate's, the bank hihat's six
var (
	membraneModes = [...]float64{1, 1.594, 2.136, 2.296, 2.653, 2.918}
	plateModes    = [...]float64{1, 1.483, 1.800, 2.546, 2.630, 3.897}
)

// A burst's length, the bank clap's, and its fall, the kick's felt's
const (
	struckBurstLen = 0.008
	struckBurstTau = 0.002
)

// StruckFromGenes is the sound genes play, each held to [0, 1] over its row of
// the genome; the sound has no name, and its level is its norm's, set after.
// A part follows the velocity and the stroke as the bank drums' like part does.
func StruckFromGenes(genes []float64) *SoundDef {
	var v [StruckGeneCount]float64
	for i, g := range struckGenes {
		x := 0.0
		if i < len(genes) {
			x = min(max(genes[i], 0), 1)
		}
		if g.log {
			v[i] = g.min * math.Pow(g.max/g.min, x)
		} else {
			v[i] = g.min + x*(g.max-g.min)
		}
	}
	d := &SoundDef{Variants: 6, Velocities: []float64{0.3, 0.6, 1}, PitchWalk: 0.02, LengthWalk: 0.06}
	share, click := v[struckNoise], v[struckClick]
	if body := 1 - share; body > 0 {
		tb, m, fall := v[struckDecay], v[struckMetal], v[struckFall]
		d.Duration = 7 * tb
		// the softest layer starts a fifth of the glide over its end, as the kick's does
		track := math.Log(fall+(1-fall)/5) / math.Log(d.Velocities[0])
		var gains [len(membraneModes)]float64
		var power float64
		for k := range gains {
			gains[k] = (1-m)/float64(k+1) + m
			power += gains[k] * gains[k] / 2
		}
		d.Bus = append(d.Bus, Bus{Name: "body", Chain: []Proc{{Kind: "decay", Tau: tb, VelTrack: 0.3}}})
		for k, g := range gains {
			f := v[struckPitch] * math.Pow(membraneModes[k], 1-m) * math.Pow(plateModes[k], m)
			stroke := 0.2 // an upper mode sounds louder off the centre
			if k == 0 {
				stroke = -0.1
			}
			d.Layer = append(d.Layer, Layer{Bus: "body", Gain: body * g / math.Sqrt(power), Length: d.Duration, Stroke: stroke,
				Source: Source{Kind: "sweep", Wave: "sine", Freq: f, FreqEnd: f * fall, Curve: "exp", CurveK: 7 * v[struckFallRate], VelTrack: track}})
		}
	}
	if share > 0 || click > 0 {
		q := bandQ(v[struckNoiseWidth])
		d.Bus = append(d.Bus, Bus{Name: "noise", Chain: []Proc{
			{Kind: "bp", Freq: v[struckNoiseHz], Q: q, VelTrack: 0.3, Stroke: 0.1},
			{Kind: "gain", Amount: noiseLevel(v[struckNoiseHz], q)},
		}})
	}
	if share > 0 {
		tn := v[struckNoiseDecay]
		d.Duration = max(d.Duration, 7*tn)
		d.Layer = append(d.Layer, Layer{Bus: "noise", Gain: share, Length: 7 * tn, Source: Source{Kind: "noise"},
			Chain: []Proc{{Kind: "decay", Tau: tn, VelTrack: 0.3}}})
	}
	if click > 0 {
		n := int(math.Round(v[struckBursts]))
		span := float64(n)*(struckBurstLen+1.5*v[struckGap]) + 5*struckBurstTau
		d.Duration = max(d.Duration, span)
		d.Layer = append(d.Layer, Layer{Bus: "noise", Gain: click, Length: span, VelGain: 0.8,
			Source: Source{Kind: "burst", Burst: &Burst{Count: n, Len: struckBurstLen, Gap: v[struckGap], Tau: struckBurstTau, Decay: 0.1, Jitter: 0.5, AmpJitter: 0.35}}})
	}
	if v[struckDrive] > 0 {
		d.Chain = []Proc{{Kind: "shape", Drive: v[struckDrive], VelTrack: 0.8}}
	}
	return d
}

// bandQ is the Q of a band-pass whose half-power points are w octaves apart
func bandQ(w float64) float64 {
	b := math.Pow(2, w)
	return math.Sqrt(b) / (b - 1)
}

// noiseLevel is the gain that brings white noise in [-1, 1] through a band-pass
// at hz and q to an RMS of one: its power is a third of the band's noise
// bandwidth, pi/2 of its half-power width, over the half band
func noiseLevel(hz, q float64) float64 {
	return math.Sqrt(3 * nyquist / (math.Pi / 2 * hz / q))
}

// RenderHit plays take n of a sound struck at velocity vel offline, as a drum
// voice sounds it: the take of the velocity layer nearest vel, at vel's level.
func RenderHit(d *SoundDef, p SoundShape, vel float64, n int) ([]float64, error) {
	if err := ValidateSound(d); err != nil {
		return nil, err
	}
	vels := d.Velocities
	if len(vels) == 0 {
		vels = []float64{1}
	}
	vs := takeVariances(d, p, vels[nearestLayer(vels, vel)])
	i := n % len(vs)
	out := renderSound(d, vs[i], rand.New(rand.NewPCG(fnv64a(d.Name), uint64(i)+1)))
	for j := range out {
		out[j] *= vel
	}
	return out, nil
}

// hitFloor is how far under a hit's loudest band and frame its frames hear, in dB
const hitFloor = 60

// HitFrames is a hit as a fit hears it, two hits' distance their frames'
// MelDistance: x at rate, the hit at its start, as the mel bands of the
// toneReach frames from where its sound rises, each in dB over hitFloor under
// the loudest, so silence is zero and the level is no part of it.
func HitFrames(x []float64, rate int) [][]float32 {
	// four frames of silence ahead, as hitStart looks four either side
	y := append(make([]float64, 4*RipHop), resample(x, rate, RipRate)...)
	at := int(math.Round(hitStart(y, 0, 4*RipFrameSec) * RipRate))
	m := NewMelStream()
	e := append(m.Write(y[at:min(at+(toneReach-1)*RipHop+RipN/2, len(y))]), m.Close()...)
	e = e[:min(len(e), toneReach)]
	var top float64
	for _, f := range e {
		for _, v := range f {
			top = max(top, v)
		}
	}
	out := make([][]float32, len(e))
	for t, f := range e {
		out[t] = make([]float32, len(f))
		for b, v := range f {
			if top > 0 {
				out[t][b] = float32(max(10*math.Log10(v/top+1e-30)+hitFloor, 0))
			}
		}
	}
	return out
}
