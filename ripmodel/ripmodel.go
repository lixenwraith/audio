// Package ripmodel is the rip's learned front end: a transformer over log-mel
// frames whose heads say, per frame, whether anything and which drum struck,
// which pitches sound, are struck or let go and how hard, the note's wave and
// genes and the family playing it, and a notes model beside it that may give the
// notes. stapes' rip-train trains both; Spec and NotesSpec are their shapes.
package ripmodel

import (
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"sync"

	"github.com/lixenwraith/audio"
	"github.com/lixenwraith/transformer"
)

// The heads, in the order the output vector lays them
const (
	HeadOnset    = "onset"    // anything struck or started at this frame
	HeadDrums    = "drums"    // kick, snare, hihat
	HeadPitch    = "pitch"    // each of the rip's pitches sounding
	HeadVoicing  = "voicing"  // any note sounding
	HeadWave     = "wave"     // the sounding note's wave, one score per VoiceWaves entry
	HeadVoice    = "voice"    // its voice genes
	HeadStrike   = "strike"   // each of the rip's pitches struck at this frame
	HeadRelease  = "release"  // each of the rip's pitches let go at this frame
	HeadVelocity = "velocity" // how hard each pitch struck at this frame was struck
	HeadFamily   = "family"   // what plays the highest note sounding, one score per RipFamilies entry
)

// Spec is the model the trainer builds and a weight file must match in its
// inputs and heads; its width, depth and window may differ, and it may read a
// second channel, each band's PCEN (features).
func Spec() transformer.Config {
	return transformer.Config{
		Inputs: audio.RipMelBands + 1, Dim: 256, Heads: 8, Layers: 4, FFN: 1024, Window: 64,
		Outputs: []transformer.Head{
			{Name: HeadOnset, Size: 1}, {Name: HeadDrums, Size: int(audio.InstrClap)}, {Name: HeadPitch, Size: audio.RipPitches},
			{Name: HeadVoicing, Size: 1}, {Name: HeadWave, Size: len(audio.VoiceWaves)}, {Name: HeadVoice, Size: audio.VoiceGeneCount},
			{Name: HeadStrike, Size: audio.RipPitches}, {Name: HeadRelease, Size: audio.RipPitches}, {Name: HeadVelocity, Size: audio.RipPitches},
			{Name: HeadFamily, Size: len(audio.RipFamilies)},
		},
	}
}

// NotesSpec is the notes model the trainer builds (PitchNet), a cell net over
// the harmonic stack (stack) whose cells are the rip's pitches. A weight file
// must match it in its inputs and in the heads notesHeads names; its other
// sizes may differ.
func NotesSpec() transformer.Config {
	heads := Spec().Outputs
	return transformer.Config{
		Inputs: stackBins + 1, Channels: stackChannels,
		Outputs: []transformer.Head{heads[2], heads[6], heads[7], heads[8], heads[0], heads[1], heads[3], heads[4], heads[5]},
		Cells: &transformer.Cells{Bins: stackBins, Reduce: 8, Wide: 16, WideTime: 3, WideFreq: 39, Cell: 32, CellTime: 7, CellFreq: 3,
			Stride: 3, Depth: 1, NearTime: 7, NearFreq: 3, First: audio.RipMinMIDI - stackRootMIDI, Read: audio.RipPitches, Heads: 4, Regions: 16,
			Scalars: []int{stackBins, stackChannels/2*(stackBins+1) + stackBins}, Frame: 64, FrameTime: 5, AgainTime: 9, Dilation: 2},
	}
}

// notesHeads are the heads a notes model gives in place of the model's
var notesHeads = []string{HeadPitch, HeadVoicing, HeadStrike, HeadRelease, HeadVelocity}

// FrontEnd hears with a model, and takes the notes from Notes when it has one
type FrontEnd struct {
	Model *transformer.Model
	Notes *transformer.Model
}

// Load reads a weight file, and a notes model's from notes when it is not nil,
// and checks they hear and say what the rip expects.
func Load(r, notes io.Reader) (*FrontEnd, error) {
	m, err := transformer.Load(r)
	if err != nil {
		return nil, err
	}
	var n *transformer.Model
	if notes != nil {
		if n, err = transformer.Load(notes); err != nil {
			return nil, fmt.Errorf("notes model: %w", err)
		}
	}
	return New(m, n)
}

// optionalHeads is how many of Spec's last heads a model may lack: one trained
// before the strike, release, velocity or family head loads without them, its
// notes read from what it has
const optionalHeads = 4

// New wraps a model and a notes model, nil for none, refusing a model whose
// inputs or heads are not a prefix of Spec's or that reads more than two
// channels, and a notes model that does not read the harmonic stack or lacks
// one of notesHeads.
func New(m, notes *transformer.Model) (*FrontEnd, error) {
	want, got := Spec(), m.Config()
	if got.Inputs != want.Inputs || got.Channels > 2 {
		return nil, fmt.Errorf("ripmodel: model reads %d inputs in %d channels, want %d in one or two, the bands in dB over the frame's level and the level, then each band's PCEN and their mean", got.Inputs, got.Channels, want.Inputs)
	}
	if n := len(got.Outputs); n > len(want.Outputs) || n < len(want.Outputs)-optionalHeads {
		return nil, fmt.Errorf("ripmodel: model has %d heads, want %d", n, len(want.Outputs))
	}
	for i, h := range want.Outputs[:len(got.Outputs)] {
		if got.Outputs[i] != h {
			return nil, fmt.Errorf("ripmodel: head %d is %q of %d, want %q of %d", i, got.Outputs[i].Name, got.Outputs[i].Size, h.Name, h.Size)
		}
	}
	if notes == nil {
		return &FrontEnd{Model: m}, nil
	}
	if got := notes.Config(); got.Inputs != stackBins+1 || got.Channels != stackChannels {
		return nil, fmt.Errorf("ripmodel: notes model reads %d inputs in %d channels, want the harmonic stack's %d in %d", got.Inputs, got.Channels, stackBins+1, stackChannels)
	}
	for _, name := range notesHeads {
		i := slices.IndexFunc(want.Outputs, func(h transformer.Head) bool { return h.Name == name })
		if j := slices.Index(notes.Config().Outputs, want.Outputs[i]); j < 0 {
			return nil, fmt.Errorf("ripmodel: notes model has no head %q of %d", name, want.Outputs[i].Size)
		}
	}
	return &FrontEnd{Model: m, Notes: notes}, nil
}

// Hear runs the recording's log-mel frames through the model, and the notes
// model when there is one, and reads their heads into frames: the onset and drum
// scores as strengths, the pitch scores as salience, the chroma folded from them,
// the wave scores and voice genes as they are, the strike, release and velocity
// scores when a model has them, and the family's softmax when the model has it.
func (fe *FrontEnd) Hear(x []float64, rate int, _ audio.RipParams) (*audio.RipFrames, error) {
	in := features(audio.MelFrames(x, rate), fe.pcen())
	out := fe.Model.Forward(fe.planes(in))
	var notes [][]float32
	if fe.Notes != nil {
		notes = fe.Notes.Forward(stacked(in))
	}
	return fe.read(out, notes), nil
}

// pcen says whether the models hear the PCEN channel
func (fe *FrontEnd) pcen() bool { return fe.Model.Config().Channels == 2 || fe.Notes != nil }

// planes is each frame of features as the model reads it, in its channels
func (fe *FrontEnd) planes(in [][]float32) [][]float32 {
	cfg := fe.Model.Config()
	planes := make([][]float32, len(in))
	for t, f := range in {
		planes[t] = f[:cfg.Inputs*max(cfg.Channels, 1)]
	}
	return planes
}

// read reads the heads of the model's outputs, and the notes model's for the same
// frames when it has one, into frames
func (fe *FrontEnd) read(out, notes [][]float32) *audio.RipFrames {
	// head is a head's scores at frame t, from the model that gives it; nil for none
	head := func(name string, t int) []float32 {
		if fe.Notes != nil && slices.Contains(notesHeads, name) {
			return fe.Notes.Slice(notes[t], name)
		}
		return fe.Model.Slice(out[t], name)
	}
	n := len(out)
	f := &audio.RipFrames{Hop: audio.RipFrameSec, Flux: make([]float64, n),
		Salience:   make([]float32, 0, n*audio.RipPitches),
		Chroma:     make([][12]float64, n),
		BassChroma: make([][12]float64, n),
		Wave:       make([]float32, 0, n*len(audio.VoiceWaves)),
		Voice:      make([]float32, 0, n*audio.VoiceGeneCount)}
	for d := range f.Onset {
		f.Onset[d] = make([]float64, n)
	}
	var per []struct {
		head string
		into *[]float32
	}
	for _, h := range []struct {
		head string
		into *[]float32
	}{{HeadStrike, &f.Strike}, {HeadRelease, &f.Release}, {HeadVelocity, &f.Velocity}} {
		if n > 0 && head(h.head, 0) != nil {
			*h.into = make([]float32, 0, n*audio.RipPitches)
			per = append(per, h)
		}
	}
	if n > 0 && head(HeadFamily, 0) != nil {
		f.Family = make([]float32, 0, n*len(audio.RipFamilies))
	}
	for t := range out {
		f.Flux[t] = sigmoid(head(HeadOnset, t)[0])
		for d, v := range head(HeadDrums, t) {
			f.Onset[d][t] = sigmoid(v)
		}
		for i, v := range head(HeadPitch, t) {
			s := sigmoid(v)
			f.Salience = append(f.Salience, float32(s))
			midi := audio.RipMinMIDI + i
			f.Chroma[t][midi%12] += s
			if midi < audio.RipBassTop {
				f.BassChroma[t][midi%12] += s
			}
		}
		for _, v := range head(HeadWave, t) {
			f.Wave = append(f.Wave, v)
		}
		for _, v := range head(HeadVoice, t) {
			f.Voice = append(f.Voice, float32(sigmoid(v)))
		}
		for _, h := range per {
			for _, v := range head(h.head, t) {
				*h.into = append(*h.into, float32(sigmoid(v)))
			}
		}
		if f.Family != nil {
			f.Family = appendSoftmax(f.Family, head(HeadFamily, t))
		}
	}
	return f
}

// earAhead is the seconds a frame waits for the frames after it before the
// models hear it, earStep the seconds of frames they hear at a time
const (
	earAhead = 0.5
	earStep  = 0.5
)

// Ear hears a recording as it plays: each earStep of frames once earAhead, or the
// notes model's reach, follows them, with the frames before them that reach or the
// model's attention window reads, all against the loudest sample heard by then. Its
// frames are the same whatever the blocks, and once the recording's loudest is
// heard the notes model hears each frame as in the whole, to rounding. Asked for
// the spectrum, it gives each frame's mel bands, the samples as they are.
func (fe *FrontEnd) Ear(_ audio.RipParams, spectrum bool) (audio.RipEar, error) {
	e := &ear{fe: fe, spectrum: spectrum, mel: audio.NewMelStream(), ahead: int(math.Round(earAhead / audio.RipFrameSec)),
		step: int(math.Round(earStep / audio.RipFrameSec)), left: fe.Model.Config().Window}
	if fe.Notes != nil {
		r := reach(fe.Notes.Config().Cells)
		e.ahead, e.notesLeft = max(e.ahead, r), r
	}
	return e, nil
}

// reach is how many frames either side of its own a cell net's output reads, as
// transformer's Cells lays out its layers: the convolutions over the cells, a
// long one's three frames its distance apart, then the cell convolution's or the
// frame path's, whichever is longer, and the wide convolution's
func reach(c *transformer.Cells) int {
	cells := c.Depth * (c.NearTime / 2)
	for _, d := range c.Long {
		cells += d
	}
	pooled := c.Dilation*(c.AgainTime/2) + c.FrameTime/2
	return max(cells+c.CellTime/2, pooled) + c.WideTime/2
}

// ear is a FrontEnd hearing as a recording plays
type ear struct {
	fe   *FrontEnd
	mel  *audio.MelStream
	n    int         // samples heard
	peak float64     // the loudest of them
	e    [][]float64 // each band's energy per frame, the samples as they are, from frame e0 on
	e0   int
	avg  []float64 // the bands' PCEN average before frame e0, against avgPeak; nil at the start
	// the loudest sample avg is against, and the next frame to hear
	avgPeak float64
	next    int
	// frames a frame waits for before it is heard, frames heard at once, and the
	// frames before them the model and the notes model read
	ahead, step, left, notesLeft int
	// the frames heard and not yet given, their bands' energies, when asked
	spectrum bool
	given    []float32
}

// Hear takes the next samples at RipRate and returns the frames heard, each step
// as soon as the samples that finish the frames it waits for are in
func (e *ear) Hear(x []float64) (*audio.RipFrames, error) {
	var out, notes [][]float32
	for {
		k := min(max(e.ready()-e.n, 0), len(x))
		e.write(x[:k])
		if x = x[k:]; e.n < e.ready() {
			return e.give(out, notes), nil
		}
		o, n := e.hear(e.next + e.step)
		out, notes = append(out, o...), append(notes, n...)
	}
}

// Close hears the frames left, the recording's end after them
func (e *ear) Close() (*audio.RipFrames, error) {
	e.e = append(e.e, e.mel.Close()...)
	return e.give(e.hear(e.e0 + len(e.e))), nil
}

// give is the frames the models' outputs say, with their spectrum when asked
func (e *ear) give(out, notes [][]float32) *audio.RipFrames {
	f := e.fe.read(out, notes)
	f.Spectrum, e.given = e.given, nil
	return f
}

// ready is the samples heard once the frames the next step waits for are in
func (e *ear) ready() int { return (e.next+e.step+e.ahead-1)*audio.RipHop + audio.RipN/2 }

// write takes samples into the mel frames and the loudest sample
func (e *ear) write(x []float64) {
	for _, v := range x {
		e.peak = max(e.peak, math.Abs(v))
	}
	e.n += len(x)
	e.e = append(e.e, e.mel.Write(x)...)
}

// mel is energies as MelFrames gives them, their samples against peak
func mel(e [][]float64, peak float64) [][]float32 {
	p2 := peak * peak
	if p2 == 0 {
		p2 = 1
	}
	out := make([][]float32, len(e))
	for t, row := range e {
		out[t] = make([]float32, len(row))
		for b, v := range row {
			out[t][b] = float32(math.Log1p(100 * v / p2))
		}
	}
	return out
}

// window is the features of the frames held, from frame e0, against the loudest
// sample heard
func (e *ear) window() [][]float32 {
	avg := slices.Clone(e.avg)
	if c := e.avgPeak / e.peak; c < 1 {
		for b := range avg {
			avg[b] *= c * c
		}
	}
	in, _ := featuresFrom(mel(e.e, e.peak), e.fe.pcen(), avg)
	return in
}

// hear runs the models over the frames held, against the loudest sample heard,
// and gives their outputs for frames next to upto; it then lets go of the frames
// no later run reads
func (e *ear) hear(upto int) (out, notes [][]float32) {
	if upto <= e.next {
		return nil, nil
	}
	if c := e.avgPeak / e.peak; c < 1 { // the average against the loudest sample now
		for b := range e.avg {
			e.avg[b] *= c * c
		}
	}
	e.avgPeak = e.peak
	in := e.window()
	a := max(e.next-e.left, e.e0)
	out = e.fe.Model.Forward(e.fe.planes(in[a-e.e0:]))[e.next-a : upto-a]
	if e.fe.Notes != nil {
		b := max(e.next-e.notesLeft, e.e0)
		notes = e.fe.Notes.Forward(stacked(in[b-e.e0:]))[e.next-b : upto-b]
	}
	if e.spectrum {
		for _, row := range e.e[e.next-e.e0 : upto-e.e0] {
			for _, v := range row {
				e.given = append(e.given, float32(v))
			}
		}
	}
	e.next = upto
	if drop := e.next - max(e.left, e.notesLeft) - e.e0; drop > 0 {
		if e.fe.pcen() {
			_, e.avg = featuresFrom(mel(e.e[:drop], e.peak), true, e.avg)
		}
		e.e, e.e0 = e.e[:copy(e.e, e.e[drop:])], e.e0+drop
	}
	return out, notes
}

// melFloor is the energy 80 dB under a full-scale sine's, where the logarithm floors
const melFloor = 1e-8

// PCEN (Wang et al. 2017) is a band's energy over its own average of the last
// moments: what a room or a microphone holds steady fades, and what is struck
// stands out at any level. These are the paper's values, as librosa keeps them.
const (
	pcenTime  = 0.4 // seconds the average's time constant spans
	pcenGain  = 0.98
	pcenBias  = 2.0
	pcenPower = 0.5
	pcenEps   = 1e-6
)

// features is the model's input from MelFrames: each band's energy in dB over
// melFloor less the frame's mean, and the mean itself, both over 80 dB, so a
// frame's loudness is one input and the bands are level-free: a soft phrase
// reaches the model as clearly as a loud one. With pcen, a second channel: each
// band's PCEN and their mean; it only looks back, so a live rip computes it.
func features(mel [][]float32, pcen bool) [][]float32 {
	out, _ := featuresFrom(mel, pcen, nil)
	return out
}

// featuresFrom is the features of frames whose bands' PCEN average before the
// first is avg, nil at a recording's start, and that average after the last;
// avg is updated in place
func featuresFrom(mel [][]float32, pcen bool, avg []float64) ([][]float32, []float64) {
	out := make([][]float32, len(mel))
	smooth, channels := 1-math.Exp(-audio.RipFrameSec/pcenTime), 1
	if pcen {
		channels = 2
	}
	for t, row := range mel {
		n := len(row)
		e, db := make([]float64, n), make([]float64, n)
		var level float64
		for b, v := range row {
			e[b] = math.Expm1(float64(v)) / 100
			db[b] = 10 * math.Log10(e[b]+melFloor)
			level += db[b]
		}
		level /= float64(n)
		f := make([]float32, n+1, channels*(n+1))
		for b, d := range db {
			f[b] = float32((d - level) / 80)
		}
		f[n] = float32((level + 80) / 80)
		if pcen {
			if avg == nil {
				avg = slices.Clone(e)
			}
			var sum float64
			for b, v := range e {
				avg[b] = (1-smooth)*avg[b] + smooth*v
				p := math.Pow(v/math.Pow(pcenEps+avg[b], pcenGain)+pcenBias, pcenPower) - math.Pow(pcenBias, pcenPower)
				f = append(f, float32(p))
				sum += p
			}
			f = append(f, float32(sum/float64(n)))
		}
		out[t] = f
	}
	return out, avg
}

// The harmonic stack a notes model reads: both planes on a log-frequency axis of
// three bins a semitone from MIDI stackRootMIDI, interpolated from the mel
// bands, each plane a channel for each harmonic of a half and one to seven,
// shifted so a pitch's harmonics lie at its own bin, ending in the plane's scalar
const (
	stackChannels = 16
	stackBins     = 213
	stackRootMIDI = 27
)

// stackShifts are the bins each harmonic lies above the fundamental, 36·log2 h
var stackShifts = [...]int{-36, 0, 36, 57, 72, 84, 93, 101}

// stackBin is a long-axis bin's lower mel band, the upper band's weight, and
// whether the bands reach it
type stackBin struct {
	band int
	w    float64
	ok   bool
}

var stackAxis = sync.OnceValue(func() []stackBin {
	mel := func(hz float64) float64 { return 2595 * math.Log10(1+hz/700) }
	hz := func(m float64) float64 { return 700 * (math.Pow(10, m/2595) - 1) }
	bands := audio.RipMelBands
	centers := make([]float64, bands)
	for b := range centers {
		centers[b] = math.Log(hz(mel(30) + (mel(audio.RipRate/2)-mel(30))*float64(b+1)/float64(bands+1)))
	}
	axis := make([]stackBin, stackBins+stackShifts[len(stackShifts)-1]-stackShifts[0])
	for k := range axis {
		f := math.Log(440) + math.Ln2*(float64(stackRootMIDI)+float64(k+stackShifts[0])/3-69)/12
		b := max(min(sort.SearchFloat64s(centers, math.Nextafter(f, math.Inf(1)))-1, bands-2), 0)
		axis[k] = stackBin{b, (f - centers[b]) / (centers[b+1] - centers[b]), f >= centers[0] && f < centers[bands-1]}
	}
	return axis
})

// stacked is each frame of features, both planes, as its harmonic channels
func stacked(in [][]float32) [][]float32 {
	axis, per := stackAxis(), stackChannels*(stackBins+1)
	flat, long := make([]float32, len(in)*per), make([]float32, len(axis))
	out := make([][]float32, len(in))
	for t, f := range in {
		row, width := flat[t*per:t*per:(t+1)*per], len(f)/2
		for _, plane := range [][]float32{f[:width], f[width:]} {
			for k, a := range axis {
				long[k] = 0
				if a.ok {
					long[k] = float32(float64(plane[a.band])*(1-a.w) + float64(plane[a.band+1])*a.w)
				}
			}
			for _, s := range stackShifts {
				row = append(row, long[s-stackShifts[0]:][:stackBins]...)
				row = append(row, plane[width-1])
			}
		}
		out[t] = row
	}
	return out
}

func sigmoid(v float32) float64 { return 1 / (1 + math.Exp(-float64(v))) }

// appendSoftmax appends a head's scores as shares that sum to one
func appendSoftmax(to, v []float32) []float32 {
	top := slices.Max(v)
	var sum float64
	for _, x := range v {
		sum += math.Exp(float64(x - top))
	}
	for _, x := range v {
		to = append(to, float32(math.Exp(float64(x-top))/sum))
	}
	return to
}
