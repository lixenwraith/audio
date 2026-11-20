// Package ripmodels is the rip's learned front end: a frame model, a transformer
// over the band frames whose heads say, per frame, whether anything and which drum
// struck, which pitches sound, are struck or let go and how hard, the note's wave,
// genes and family, and whether a beat or a bar line falls there, and a notes
// model that may give the notes and what plays each. stapes' rip-train trains both.
package ripmodels

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"slices"

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

// Layout names the constant a weight file states the layout of the frames it was
// built for by, audio.RipLayout: a file of another layout, or of none, is refused
const Layout = "layout"

// layoutName is audio.RipLayout as a refusal names it
const layoutName = "315 bands three a semitone from G#0, a fine plane and a sharp one"

// FrameSpec is the frame model the trainer builds, which a weight file must match
// in its inputs, heads and layout, any head after these read by nothing: the
// piano's pitches, each plane's bands in dB in two channels, fine then sharp, or
// the PCEN planes after them in four (features). Its width, depth and window may differ.
func FrameSpec() transformer.Config {
	n := audio.RipPiano.Len()
	return transformer.Config{
		Inputs: audio.RipBands + 1, Channels: 2, Dim: 256, Heads: 8, Layers: 4, FFN: 1024, Window: 64,
		Outputs: []transformer.Head{
			{Name: HeadOnset, Size: 1}, {Name: HeadDrums, Size: int(audio.InstrClap)}, {Name: HeadPitch, Size: n},
			{Name: HeadVoicing, Size: 1}, {Name: HeadWave, Size: len(audio.VoiceWaves)}, {Name: HeadVoice, Size: audio.VoiceGeneCount},
			{Name: HeadStrike, Size: n}, {Name: HeadRelease, Size: n}, {Name: HeadVelocity, Size: n},
			{Name: HeadFamily, Size: len(audio.RipFamilies)}, {Name: HeadBeat, Size: 1}, {Name: HeadDownbeat, Size: 1},
		},
		Constants: []transformer.Constant{{Name: Layout, Value: audio.RipLayout}},
	}
}

// NotesSpec is the notes model the trainer builds (PitchNet), a cell net over the
// piano's harmonic stack (stacked) whose cells are its pitches, which a weight
// file must match in its inputs, layout and the heads notesHeads names; its other
// sizes may differ.
func NotesSpec() transformer.Config {
	heads, n := FrameSpec().Outputs, audio.RipPiano.Len()
	scalars := make([]int, stackPlanes)
	for p := range scalars { // each plane's scalar ends its first channel
		scalars[p] = p*len(stackShifts)*(stackBins+1) + stackBins
	}
	cells := &transformer.Cells{Bins: stackBins, Reduce: 8, Wide: 16, WideTime: 3, WideFreq: 39, Cell: 32, CellTime: 7, CellFreq: 3,
		Stride: stackStride, Depth: 1, NearTime: 7, NearFreq: 3, First: 1, Read: n, Heads: 8, Regions: 16,
		Scalars: scalars, Frame: 64, FrameTime: 5, AgainTime: 9, Dilation: 2}
	return transformer.Config{
		Inputs: stackBins + 1, Channels: stackChannels,
		Outputs: []transformer.Head{heads[2], heads[6], heads[7], heads[8], {Name: HeadCents, Size: 2 * n},
			{Name: HeadPitchFamily, Size: (len(audio.RipFamilies) - 1) * n},
			{Name: HeadPitchWave, Size: len(audio.VoiceWaves) * n}, {Name: HeadPitchVoice, Size: audio.VoiceGeneCount * n},
			heads[0], heads[1], heads[3], heads[4], heads[5], {Name: HeadSure, Size: 1}},
		Cells: cells, Constants: FrameSpec().Constants,
	}
}

// notesHead is a head a notes model gives: one of FrameSpec's it gives in place
// of the frame model's, or one of its own of per values a pitch and frame values
// a frame; a probe's file has no cents head, and rips as its start did
type notesHead struct {
	name       string
	per, frame int
	optional   bool
}

// notesHeads is a row a head
var notesHeads = []notesHead{{name: HeadPitch}, {name: HeadVoicing}, {name: HeadStrike}, {name: HeadRelease}, {name: HeadVelocity},
	{name: HeadCents, per: 2, optional: true}, {name: HeadPitchFamily, per: len(audio.RipFamilies) - 1}, {name: HeadPitchWave, per: len(audio.VoiceWaves)},
	{name: HeadPitchVoice, per: audio.VoiceGeneCount}, {name: HeadSure, frame: 1}}

// FrontEnd hears with a frame model, and takes the notes from Notes when it has one
type FrontEnd struct {
	Frame *transformer.Model
	Notes *transformer.Model
	// Ahead is the seconds a frame of an Ear waits for those after it before the
	// frame model hears it, when under its reach: its blocks share the wait, and
	// its heads are no longer Hear's. Zero waits the reach.
	Ahead float64
	// NotesSum is the checksum the file of the weights that give the notes ends
	// in, the notes model's or else the frame model's: what a judge names the weights
	// it was fitted on by (audio.LoadRipJudge). Load sets it; zero from New.
	NotesSum uint64
}

// Load reads a frame model's weight file, and a notes model's from notes when it
// is not nil, and checks they hear and say what the rip expects.
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

// New wraps a frame model and a notes model, nil for none, refusing a model whose
// file states no layout or another, a frame model whose inputs are not FrameSpec's
// or whose heads do not start with its, and a notes model that does not read the
// harmonic stack or lacks one of notesHeads.
func New(m, notes *transformer.Model) (*FrontEnd, error) {
	for _, model := range []struct {
		name string
		m    *transformer.Model
	}{{"frame model", m}, {"notes model", notes}} {
		if model.m == nil {
			continue
		}
		switch v, ok := model.m.Config().Constant(Layout); {
		case !ok:
			return nil, fmt.Errorf("ripmodels: the %s's file states no frame layout, these frames are layout %d (%s)", model.name, audio.RipLayout, layoutName)
		case v != audio.RipLayout:
			return nil, fmt.Errorf("ripmodels: the %s's file states frame layout %g, these frames are layout %d (%s)", model.name, v, audio.RipLayout, layoutName)
		}
	}
	want, got := FrameSpec(), m.Config()
	if got.Inputs != want.Inputs || got.Channels != 2 && got.Channels != 4 {
		return nil, fmt.Errorf("ripmodels: frame model reads %d inputs in %d channels, want %d in two, each plane's bands in dB over its level and the level, fine then sharp, or four, then each plane's PCEN and their mean", got.Inputs, got.Channels, want.Inputs)
	}
	if len(got.Outputs) < len(want.Outputs) || !slices.Equal(got.Outputs[:len(want.Outputs)], want.Outputs) {
		return nil, fmt.Errorf("ripmodels: frame model's heads are %v, want %v first", got.Outputs, want.Outputs)
	}
	if notes == nil {
		return &FrontEnd{Frame: m}, nil
	}
	n := audio.RipPiano.Len()
	if got := notes.Config(); got.Inputs != stackBins+1 || got.Channels != stackChannels {
		return nil, fmt.Errorf("ripmodels: notes model reads %d inputs in %d channels, want the harmonic stack's %d in %d", got.Inputs, got.Channels, stackBins+1, stackChannels)
	}
	for _, h := range notesHeads {
		head := transformer.Head{Name: h.name, Size: h.per*n + h.frame}
		if head.Size == 0 {
			head = want.Outputs[slices.IndexFunc(want.Outputs, func(o transformer.Head) bool { return o.Name == h.name })]
		}
		i := slices.IndexFunc(notes.Config().Outputs, func(o transformer.Head) bool { return o.Name == h.name })
		if i < 0 && h.optional {
			continue
		}
		if i < 0 || notes.Config().Outputs[i] != head {
			return nil, fmt.Errorf("ripmodels: notes model has no head %q of %d", h.name, head.Size)
		}
	}
	if lo, hi := sureSpread(notes); lo != hi {
		return nil, fmt.Errorf("ripmodels: notes model's %q head says %g to %g as what it hears changes; the trainer's export writes its row zero, the measure its bias", HeadSure, lo, hi)
	}
	return &FrontEnd{Frame: m, Notes: notes}, nil
}

// sureSpread is the least and the most a notes model's sure head says over a
// probe of frames unlike each other, one number where its row is zero as the
// export writes it
func sureSpread(notes *transformer.Model) (lo, hi float32) {
	probe := make([][]float32, 4)
	for t := range probe {
		probe[t] = make([]float32, stackChannels*notes.Config().Inputs)
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

// Hear runs the recording's band frames through the frame model, and the notes
// model when there is one, and reads their heads into frames: the onset and drum
// scores as strengths, the pitch scores as salience and chroma, the wave scores
// and voice genes, and where a model has them the strikes, releases, velocities,
// the family's shares, the beat, the downbeat, the cents and what plays each pitch.
func (fe *FrontEnd) Hear(x []float64, rate int, _ audio.RipParams) (*audio.RipFrames, error) {
	in := features(audio.BandFrames(x, rate), fe.pcen())
	out := fe.Frame.Forward(fe.planes(in))
	var notes [][]float32
	if fe.Notes != nil {
		notes = fe.Notes.Forward(stacked(in))
	}
	return fe.read(out, notes), nil
}

// pcen says whether the models hear the PCEN planes
func (fe *FrontEnd) pcen() bool { return fe.Frame.Config().Channels == 4 || fe.Notes != nil }

// planes is each frame of features as the frame model reads it, in its channels
func (fe *FrontEnd) planes(in [][]float32) [][]float32 {
	cfg := fe.Frame.Config()
	planes := make([][]float32, len(in))
	for t, f := range in {
		planes[t] = f[:cfg.Inputs*max(cfg.Channels, 1)]
	}
	return planes
}

// read reads the heads of the frame model's outputs, and the notes model's for
// the same frames when it has one, into frames
func (fe *FrontEnd) read(out, notes [][]float32) *audio.RipFrames {
	// head is a head's scores at frame t, from the model that gives it; nil for none
	head := func(name string, t int) []float32 {
		if fe.Notes != nil && slices.ContainsFunc(notesHeads, func(h notesHead) bool { return h.name == name }) {
			return fe.Notes.Slice(notes[t], name)
		}
		return fe.Frame.Slice(out[t], name)
	}
	n, rg := len(out), audio.RipPiano
	f := &audio.RipFrames{Hop: audio.RipFrameSec, Range: rg, Flux: make([]float64, n),
		Salience:   make([]float32, 0, n*rg.Len()),
		Strike:     make([]float32, 0, n*rg.Len()),
		Release:    make([]float32, 0, n*rg.Len()),
		Velocity:   make([]float32, 0, n*rg.Len()),
		Chroma:     make([][12]float64, n),
		BassChroma: make([][12]float64, n),
		Wave:       make([]float32, 0, n*len(audio.VoiceWaves)),
		Voice:      make([]float32, 0, n*audio.VoiceGeneCount),
		Family:     make([]float32, 0, n*len(audio.RipFamilies)),
		Beat:       make([]float64, n), Downbeat: make([]float64, n)}
	for d := range f.Onset {
		f.Onset[d] = make([]float64, n)
	}
	per := []struct {
		head string
		into *[]float32
	}{{HeadStrike, &f.Strike}, {HeadRelease, &f.Release}, {HeadVelocity, &f.Velocity}}
	if n > 0 && fe.Notes != nil {
		if head(HeadCents, 0) != nil {
			f.Cents, f.CentsError = make([]float32, 0, n*rg.Len()), make([]float32, 0, n*rg.Len())
		}
		f.VoiceAt, f.Voices = make([]uint8, 0, n*audio.RipVoiceCells), make([]uint8, 0, n*audio.RipVoiceCells*audio.RipVoiceSize)
		f.Sure = float64(head(HeadSure, 0)[0])
	}
	for t := range out {
		f.Flux[t] = sigmoid(head(HeadOnset, t)[0])
		for d, v := range head(HeadDrums, t) {
			f.Onset[d][t] = sigmoid(v)
		}
		for i, v := range head(HeadPitch, t) {
			s := sigmoid(v)
			f.Salience = append(f.Salience, float32(s))
			midi := rg.Min + i
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
		f.Family = appendSoftmax(f.Family, head(HeadFamily, t))
		f.Beat[t], f.Downbeat[t] = sigmoid(head(HeadBeat, t)[0]), sigmoid(head(HeadDownbeat, t)[0])
		if f.Cents != nil {
			c := head(HeadCents, t)
			for i, v := range c[:rg.Len()] {
				f.Cents = append(f.Cents, 100*v)
				f.CentsError = append(f.CentsError, float32(100*math.Exp(float64(c[rg.Len()+i]))))
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
			for i := cell; i < len(h); i += len(pitch) {
				shares = append(shares, h[i])
			}
			n := len(shares)
			shares = appendSoftmax(shares, shares)
			for _, v := range shares[n:] {
				to = append(to, uint8(math.Round(255*float64(v))))
			}
		}
		for i := cell; i < len(voice); i += len(pitch) {
			to = append(to, uint8(math.Round(255*sigmoid(voice[i]))))
		}
	}
	return at, to
}

// earStep is the seconds of frames the ear gives at a time
const earStep = 0.5

// Ear hears a recording as it plays, each frame through each model once
// (transformer's Stream): a step of frames is given once the frames the models
// read after them are in, the frame model's reach or fe.Ahead, and the notes
// model's at least, each scaled by the loudest sample heard when it came, alike
// whatever the blocks; asked for the spectrum, each has its mel bands.
func (fe *FrontEnd) Ear(_ audio.RipParams, spectrum bool) (audio.RipEar, error) {
	e := &ear{fe: fe, bands: audio.NewBandStream(), step: int(math.Round(earStep / audio.RipFrameSec)), ahead: fe.Frame.Reach()}
	if spectrum {
		e.mel = audio.NewMelStream()
	}
	if fe.Ahead > 0 {
		e.ahead = min(e.ahead, int(math.Round(fe.Ahead/audio.RipFrameSec)))
	}
	if fe.Notes != nil {
		e.ahead = max(e.ahead, fe.Notes.Reach())
		e.notes = &heard{s: fe.Notes.Stream(fe.Notes.Reach())}
	}
	e.model = &heard{s: fe.Frame.Stream(e.ahead)}
	return e, nil
}

// ear is a FrontEnd hearing as a recording plays
type ear struct {
	fe      *FrontEnd
	bands   *audio.BandStream
	n       int         // samples heard
	peak    float64     // the loudest of them
	fresh   [][]float64 // the frames no model has taken: each band's energy, the samples as they are
	avg     []float64   // the bands' PCEN average before them, against avgPeak; nil at the start
	avgPeak float64
	// frames the models have taken, the next to give, the frames a frame waits
	// for before it is given, and the frames given at once
	taken, next, ahead, step int
	model, notes             *heard
	// the spectrum's stream when asked, and its frames' mel bands not yet given
	mel      *audio.MelStream
	spectrum []float32
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
	e.fresh = append(e.fresh, e.bands.Close()...)
	if e.mel != nil {
		e.addSpectrum(e.mel.Close())
	}
	return e.give(e.hear(e.taken+len(e.fresh), true)), nil
}

// give is the frames the models' outputs say, with their spectrum when asked
func (e *ear) give(out, notes [][]float32) *audio.RipFrames {
	f := e.fe.read(out, notes)
	if e.mel != nil {
		n := len(out) * audio.RipMelBands
		f.Spectrum, e.spectrum = e.spectrum[:n:n], e.spectrum[n:]
	}
	return f
}

// ready is the samples heard once the frames the next step waits for are in
func (e *ear) ready() int {
	return (e.next+e.step+e.ahead-1)*audio.RipHop + audio.RipBandsAhead
}

// write takes samples into the band frames, the spectrum's when asked, and the
// loudest sample
func (e *ear) write(x []float64) {
	for _, v := range x {
		e.peak = max(e.peak, math.Abs(v))
	}
	e.n += len(x)
	e.fresh = append(e.fresh, e.bands.Write(x)...)
	if e.mel != nil {
		e.addSpectrum(e.mel.Write(x))
	}
}

// addSpectrum queues mel frames until their frames are given; a mel frame is in
// before its band frame is
func (e *ear) addSpectrum(frames [][]float64) {
	for _, row := range frames {
		for _, v := range row {
			e.spectrum = append(e.spectrum, float32(v))
		}
	}
}

// scaled is energies as BandFrames gives them, their samples against peak
func scaled(e [][]float64, peak float64) [][]float32 {
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
	in, e.avg = featuresFrom(scaled(e.fresh, e.peak), e.fe.pcen(), e.avg)
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

// bandFloor is the energy 80 dB under a full-scale sine's, where the logarithm floors
const bandFloor = 1e-8

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

// features is the models' input from BandFrames, a plane of RipBands+1 values for
// each of the fine and the sharp bands: each band's energy in dB over bandFloor
// less its plane's mean, then the mean, over 80 dB, so the bands are level-free.
// With pcen, two planes more: each band's PCEN, then their mean; it only looks
// back, so a live rip computes it.
func features(frames [][]float32, pcen bool) [][]float32 {
	out, _ := featuresFrom(frames, pcen, nil)
	return out
}

// featuresFrom is the features of frames whose bands' PCEN average before the
// first is avg, nil at a recording's start, and that average after the last;
// avg is updated in place
func featuresFrom(frames [][]float32, pcen bool, avg []float64) ([][]float32, []float64) {
	const n = audio.RipBands
	smooth, planes := 1-math.Exp(-audio.RipFrameSec/pcenTime), 2
	if pcen {
		planes = 4
	}
	out, e, db := make([][]float32, len(frames)), make([]float64, 2*n), make([]float64, n)
	for t, row := range frames {
		f := make([]float32, planes*(n+1))
		for b, v := range row {
			e[b] = math.Expm1(float64(v)) / 100
		}
		for p := range 2 {
			var level float64
			for b, v := range e[p*n : (p+1)*n] {
				db[b] = 10 * math.Log10(v+bandFloor)
				level += db[b]
			}
			level /= n
			plane := f[p*(n+1) : (p+1)*(n+1)]
			for b, d := range db {
				plane[b] = float32((d - level) / 80)
			}
			plane[n] = float32((level + 80) / 80)
		}
		if pcen {
			if avg == nil {
				avg = slices.Clone(e)
			}
			for p := range 2 {
				plane, sum := f[(2+p)*(n+1):(3+p)*(n+1)], 0.0
				for b := range n {
					a := &avg[p*n+b]
					*a = (1-smooth)**a + smooth*e[p*n+b]
					v := math.Pow(e[p*n+b]/math.Pow(pcenEps+*a, pcenGain)+pcenBias, pcenPower) - math.Pow(pcenBias, pcenPower)
					plane[b], sum = float32(v), sum+v
				}
				plane[n] = float32(sum / n)
			}
		}
		out[t] = f
	}
	return out, avg
}

// The harmonic stack a notes model reads: each plane of features a channel for
// each harmonic of a half and one to seven, the bands shifted so a pitch's
// harmonics lie at its own bin, and ending in the plane's scalar; bin k is band k,
// stackStride a semitone from the semitone under the piano's lowest key to the one
// over its highest, and zero where the shift runs past the bands
const (
	stackPlanes   = 4
	stackStride   = 3
	stackChannels = stackPlanes * len(stackShifts)
)

// stackBins is the stack's bins, its cells the piano's keys
var stackBins = stackStride * (audio.RipPiano.Len() + 2)

// stackShifts are the bins each harmonic lies above the fundamental, 36·log2 h
var stackShifts = [...]int{-36, 0, 36, 57, 72, 84, 93, 101}

// stacked is each frame of features, all four planes, as the harmonic stack
func stacked(in [][]float32) [][]float32 {
	const n = audio.RipBands
	per := stackChannels * (stackBins + 1)
	flat, out := make([]float32, len(in)*per), make([][]float32, len(in))
	for t, f := range in {
		row := flat[t*per : (t+1)*per]
		for p := range stackPlanes {
			plane := f[p*(n+1) : (p+1)*(n+1)]
			for c, s := range stackShifts {
				ch := row[(p*len(stackShifts)+c)*(stackBins+1):][:stackBins+1]
				lo, hi := max(-s, 0), min(n-s, stackBins)
				copy(ch[lo:hi], plane[lo+s:hi+s])
				ch[stackBins] = plane[n]
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
