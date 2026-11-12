// Package ripmodels is the rip's learned front end: a transformer over log-mel
// frames whose heads say, per frame, whether anything and which drum struck,
// which pitches sound, are struck or let go and how hard, the note's wave, genes
// and family, and whether a beat or a bar line falls there, and a notes model
// that may give the notes and what plays each. stapes' rip-train trains both.
package ripmodels

import (
	"encoding/binary"
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
	HeadBeat     = "beat"     // a beat of the meter falls on this frame
	HeadDownbeat = "downbeat" // a bar line falls on this frame
	// HeadCents is a notes model's alone: where in its cell each pitch's sound sits,
	// in cells from the centre, then the log of the error it states for each
	HeadCents = "cents"
	// A notes model's alone, what plays each pitch: a score for each family of
	// RipFamilies but unknown, one for each of VoiceWaves, and the voice's genes
	HeadPitchFamily = "pitchfamily"
	HeadPitchWave   = "pitchwave"
	HeadPitchVoice  = "pitchvoice"
	// HeadSure is the share a family must pass over a note to name it, a notes
	// model's: a head of no weights, the same at every frame, its trainer's measure
	HeadSure = "sure"
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
			{Name: HeadFamily, Size: len(audio.RipFamilies)}, {Name: HeadBeat, Size: 1}, {Name: HeadDownbeat, Size: 1},
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
		Outputs: []transformer.Head{heads[2], heads[6], heads[7], heads[8], {Name: HeadCents, Size: 2 * audio.RipPitches},
			{Name: HeadPitchFamily, Size: (len(audio.RipFamilies) - 1) * audio.RipPitches},
			{Name: HeadPitchWave, Size: len(audio.VoiceWaves) * audio.RipPitches}, {Name: HeadPitchVoice, Size: audio.VoiceGeneCount * audio.RipPitches},
			heads[0], heads[1], heads[3], heads[4], heads[5], {Name: HeadSure, Size: 1}},
		Cells: &transformer.Cells{Bins: stackBins, Reduce: 8, Wide: 16, WideTime: 3, WideFreq: 39, Cell: 32, CellTime: 7, CellFreq: 3,
			Stride: 3, Depth: 1, NearTime: 7, NearFreq: 3, First: audio.RipMinMIDI - stackRootMIDI, Read: audio.RipPitches, Heads: 8, Regions: 16,
			Scalars: []int{stackBins, stackChannels/2*(stackBins+1) + stackBins}, Frame: 64, FrameTime: 5, AgainTime: 9, Dilation: 2},
	}
}

// notesHead is a head a notes model gives: one of Spec's it gives in place of
// the model's, or one of its own of per values a pitch and frame values a
// frame, which a notes model trained before it may lack
type notesHead struct {
	name       string
	per, frame int
}

// notesHeads is a row a head
var notesHeads = []notesHead{{name: HeadPitch}, {name: HeadVoicing}, {name: HeadStrike}, {name: HeadRelease}, {name: HeadVelocity},
	{name: HeadCents, per: 2}, {name: HeadPitchFamily, per: len(audio.RipFamilies) - 1}, {name: HeadPitchWave, per: len(audio.VoiceWaves)},
	{name: HeadPitchVoice, per: audio.VoiceGeneCount}, {name: HeadSure, frame: 1}}

// FrontEnd hears with a model, and takes the notes from Notes when it has one
type FrontEnd struct {
	Model *transformer.Model
	Notes *transformer.Model
	// Ahead is the seconds a frame of an Ear waits for those after it before the
	// model hears it, when under the model's reach: its blocks share the wait,
	// and its heads are no longer Hear's. Zero waits the reach.
	Ahead float64
	// NotesSum is the checksum the file of the weights that give the notes ends
	// in, the notes model's or else the model's: what a judge names the weights
	// it was fitted on by (audio.LoadRipJudge). Load sets it; zero from New.
	NotesSum uint64
}

// Load reads a weight file, and a notes model's from notes when it is not nil,
// and checks they hear and say what the rip expects.
func Load(r, notes io.Reader) (*FrontEnd, error) {
	sum := &tail{r: r}
	m, err := transformer.Load(sum)
	if err != nil {
		return nil, err
	}
	var n *transformer.Model
	if notes != nil {
		sum = &tail{r: notes}
		if n, err = transformer.Load(sum); err != nil {
			return nil, fmt.Errorf("notes model: %w", err)
		}
	}
	fe, err := New(m, n)
	if err != nil {
		return nil, err
	}
	fe.NotesSum = binary.LittleEndian.Uint64(sum.last[:])
	return fe, nil
}

// tail reads through r and keeps the last eight bytes read, the checksum a
// weight file ends in
type tail struct {
	r    io.Reader
	last [8]byte
}

func (t *tail) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if k := min(n, len(t.last)); k > 0 {
		copy(t.last[:], t.last[k:])
		copy(t.last[len(t.last)-k:], p[n-k:n])
	}
	return n, err
}

// optionalHeads is how many of Spec's last heads a model may lack: one trained
// before the strike, release, velocity, family, beat or downbeat head loads
// without them, its notes and its bars read from what it has
const optionalHeads = 6

// New wraps a model and a notes model, nil for none, refusing a model whose
// inputs or heads are not a prefix of Spec's or that reads more than two
// channels, and a notes model that does not read the harmonic stack or lacks
// one of notesHeads that Spec has.
func New(m, notes *transformer.Model) (*FrontEnd, error) {
	want, got := Spec(), m.Config()
	if got.Inputs != want.Inputs || got.Channels > 2 {
		return nil, fmt.Errorf("ripmodels: model reads %d inputs in %d channels, want %d in one or two, the bands in dB over the frame's level and the level, then each band's PCEN and their mean", got.Inputs, got.Channels, want.Inputs)
	}
	if n := len(got.Outputs); n > len(want.Outputs) || n < len(want.Outputs)-optionalHeads {
		return nil, fmt.Errorf("ripmodels: model has %d heads, want %d", n, len(want.Outputs))
	}
	for i, h := range want.Outputs[:len(got.Outputs)] {
		if got.Outputs[i] != h {
			return nil, fmt.Errorf("ripmodels: head %d is %q of %d, want %q of %d", i, got.Outputs[i].Name, got.Outputs[i].Size, h.Name, h.Size)
		}
	}
	if notes == nil {
		return &FrontEnd{Model: m}, nil
	}
	if got := notes.Config(); got.Inputs != stackBins+1 || got.Channels != stackChannels {
		return nil, fmt.Errorf("ripmodels: notes model reads %d inputs in %d channels, want the harmonic stack's %d in %d", got.Inputs, got.Channels, stackBins+1, stackChannels)
	}
	for _, h := range notesHeads {
		head := transformer.Head{Name: h.name, Size: h.per*audio.RipPitches + h.frame}
		if head.Size == 0 {
			head = want.Outputs[slices.IndexFunc(want.Outputs, func(o transformer.Head) bool { return o.Name == h.name })]
		}
		i := slices.IndexFunc(notes.Config().Outputs, func(o transformer.Head) bool { return o.Name == h.name })
		if i < 0 && h.per+h.frame > 0 {
			continue
		}
		if i < 0 || notes.Config().Outputs[i] != head {
			return nil, fmt.Errorf("ripmodels: notes model has no head %q of %d", h.name, head.Size)
		}
	}
	if lo, hi := sureSpread(notes); lo != hi {
		return nil, fmt.Errorf("ripmodels: notes model's %q head says %g to %g as what it hears changes; the trainer's export writes its row zero, the measure its bias", HeadSure, lo, hi)
	}
	return &FrontEnd{Model: m, Notes: notes}, nil
}

// sureSpread is the least and the most a notes model's sure head says over a
// probe of frames unlike each other, one number where its row is zero as the
// export writes it, and zero for a model without the head
func sureSpread(notes *transformer.Model) (lo, hi float32) {
	if !slices.ContainsFunc(notes.Config().Outputs, func(o transformer.Head) bool { return o.Name == HeadSure }) {
		return 0, 0
	}
	probe := make([][]float32, 4)
	for t := range probe {
		probe[t] = make([]float32, stackChannels*(stackBins+1))
		for i := range probe[t] {
			probe[t][i] = float32(math.Sin(float64((t + 1) * (i + 1))))
		}
	}
	for t, out := range notes.Forward(probe) {
		v := notes.Slice(out, HeadSure)[0]
		if t == 0 {
			lo, hi = v, v
		}
		lo, hi = min(lo, v), max(hi, v)
	}
	return lo, hi
}

// Hear runs the recording's log-mel frames through the model, and the notes
// model when there is one, and reads their heads into frames: the onset and drum
// scores as strengths, the pitch scores as salience, the chroma folded from them,
// the wave scores and voice genes as they are, the strike, release and velocity
// scores when a model has them, the family's softmax and the beat and downbeat
// scores when the model has them, and when the notes model has them each pitch's
// cents and their error, what plays each frame's most salient pitches, and Sure.
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
		if fe.Notes != nil && slices.ContainsFunc(notesHeads, func(h notesHead) bool { return h.name == name }) {
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
	if n > 0 && head(HeadBeat, 0) != nil {
		f.Beat = make([]float64, n)
	}
	if n > 0 && head(HeadDownbeat, 0) != nil {
		f.Downbeat = make([]float64, n)
	}
	if n > 0 && head(HeadCents, 0) != nil {
		f.Cents, f.CentsError = make([]float32, 0, n*audio.RipPitches), make([]float32, 0, n*audio.RipPitches)
	}
	if n > 0 && head(HeadPitchFamily, 0) != nil && head(HeadPitchWave, 0) != nil && head(HeadPitchVoice, 0) != nil {
		f.VoiceAt, f.Voices = make([]uint8, 0, n*audio.RipVoiceCells), make([]uint8, 0, n*audio.RipVoiceCells*audio.RipVoiceSize)
		if s := head(HeadSure, 0); s != nil {
			f.Sure = float64(s[0])
		}
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
		if f.Beat != nil {
			f.Beat[t] = sigmoid(head(HeadBeat, t)[0])
		}
		if f.Downbeat != nil {
			f.Downbeat[t] = sigmoid(head(HeadDownbeat, t)[0])
		}
		if f.Cents != nil {
			c := head(HeadCents, t)
			for i, v := range c[:audio.RipPitches] {
				f.Cents = append(f.Cents, 100*v)
				f.CentsError = append(f.CentsError, float32(100*math.Exp(float64(c[audio.RipPitches+i]))))
			}
		}
		if f.Voices != nil {
			f.VoiceAt, f.Voices = appendVoices(f.VoiceAt, f.Voices, head(HeadPitch, t), head(HeadPitchFamily, t), head(HeadPitchWave, t), head(HeadPitchVoice, t))
		}
	}
	return f
}

// appendVoices appends a frame's RipVoiceCells most salient pitches, the most
// salient first, and what the heads say plays each as bytes of 255 a whole:
// the family scores' softmax, the wave scores' and each gene's sigmoid. A
// head's scores lie value by value, each value's pitches in turn.
func appendVoices(at, to []uint8, pitch, family, wave, voice []float32) ([]uint8, []uint8) {
	var top [audio.RipVoiceCells]int
	for i := range pitch { // an insertion into the cells kept, the earlier of two alike first
		k := min(i, len(top))
		for ; k > 0 && pitch[i] > pitch[top[k-1]]; k-- {
			if k < len(top) {
				top[k] = top[k-1]
			}
		}
		if k < len(top) {
			top[k] = i
		}
	}
	var shares []float32
	for _, cell := range top {
		at = append(at, uint8(cell))
		for _, h := range [][]float32{family, wave} {
			shares = shares[:0]
			for i := cell; i < len(h); i += audio.RipPitches {
				shares = append(shares, h[i])
			}
			n := len(shares)
			shares = appendSoftmax(shares, shares)
			for _, v := range shares[n:] {
				to = append(to, uint8(math.Round(255*float64(v))))
			}
		}
		for i := cell; i < len(voice); i += audio.RipPitches {
			to = append(to, uint8(math.Round(255*sigmoid(voice[i]))))
		}
	}
	return at, to
}

// earStep is the seconds of frames the ear gives at a time
const earStep = 0.5

// Ear hears a recording as it plays, each frame through each model once
// (transformer's Stream): a step of frames is given once the frames the models
// read after them are in, the model's reach or fe.Ahead, and the notes model's
// at least, each scaled by the loudest sample heard when it came. Its frames
// are the same whatever the blocks; asked for the spectrum, each has its bands.
func (fe *FrontEnd) Ear(_ audio.RipParams, spectrum bool) (audio.RipEar, error) {
	e := &ear{fe: fe, spectrum: spectrum, mel: audio.NewMelStream(), step: int(math.Round(earStep / audio.RipFrameSec)), ahead: fe.Model.Reach()}
	if fe.Ahead > 0 {
		e.ahead = min(e.ahead, int(math.Round(fe.Ahead/audio.RipFrameSec)))
	}
	if fe.Notes != nil {
		e.ahead = max(e.ahead, fe.Notes.Reach())
		e.notes = &heard{s: fe.Notes.Stream(fe.Notes.Reach())}
	}
	e.model = &heard{s: fe.Model.Stream(e.ahead)}
	return e, nil
}

// ear is a FrontEnd hearing as a recording plays
type ear struct {
	fe      *FrontEnd
	mel     *audio.MelStream
	n       int         // samples heard
	peak    float64     // the loudest of them
	fresh   [][]float64 // the frames no model has taken: each band's energy, the samples as they are
	avg     []float64   // the bands' PCEN average before them, against avgPeak; nil at the start
	avgPeak float64
	// frames the models have taken, the next to give, the frames a frame waits
	// for before it is given, and the frames given at once
	taken, next, ahead, step int
	model, notes             *heard
	// the bands' energies of the frames taken and not yet given, when asked
	spectrum bool
	bands    []float32
}

// heard is a model's stream and its outputs not yet given
type heard struct {
	s   *transformer.Stream
	out [][]float32
}

// take runs frames through the stream, the last of the recording when end
func (h *heard) take(frames [][]float32, end bool) {
	if h.out = append(h.out, h.s.Write(frames)...); end {
		h.out = append(h.out, h.s.Close()...)
	}
}

// give hands over the outputs of the next n frames
func (h *heard) give(n int) [][]float32 {
	out := h.out[:n:n]
	h.out = h.out[n:]
	return out
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
		o, n := e.hear(e.next+e.step, false)
		out, notes = append(out, o...), append(notes, n...)
	}
}

// Close hears the frames left, the recording's end after them
func (e *ear) Close() (*audio.RipFrames, error) {
	e.fresh = append(e.fresh, e.mel.Close()...)
	return e.give(e.hear(e.taken+len(e.fresh), true)), nil
}

// give is the frames the models' outputs say, with their spectrum when asked
func (e *ear) give(out, notes [][]float32) *audio.RipFrames {
	f := e.fe.read(out, notes)
	if e.spectrum {
		n := len(out) * audio.RipMelBands
		f.Spectrum, e.bands = e.bands[:n:n], e.bands[n:]
	}
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
	e.fresh = append(e.fresh, e.mel.Write(x)...)
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

// hear runs the fresh frames through the models, against the loudest sample
// heard, the recording's last when end, and gives their outputs for frames next
// to upto
func (e *ear) hear(upto int, end bool) (out, notes [][]float32) {
	if c := e.avgPeak / e.peak; c < 1 { // the average against the loudest sample now
		for b := range e.avg {
			e.avg[b] *= c * c
		}
	}
	e.avgPeak = e.peak
	var in [][]float32
	in, e.avg = featuresFrom(mel(e.fresh, e.peak), e.fe.pcen(), e.avg)
	if e.spectrum {
		for _, row := range e.fresh {
			for _, v := range row {
				e.bands = append(e.bands, float32(v))
			}
		}
	}
	e.taken, e.fresh = e.taken+len(e.fresh), e.fresh[:0]
	e.model.take(e.fe.planes(in), end)
	out = e.model.give(upto - e.next)
	if e.notes != nil {
		e.notes.take(stacked(in), end)
		notes = e.notes.give(upto - e.next)
	}
	e.next = upto
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
