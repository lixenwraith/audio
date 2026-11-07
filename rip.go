package audio

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"sort"

	"github.com/lixenwraith/toml"
)

// RipParams tunes the analysis a Rip makes of a recording. DefaultRipParams is
// the hand-set starting point; a search over material the engine renders, whose
// patterns are known, refines them (RenderMusic, ExpectedRip, CompareRips), and
// a [rip] table in a TOML document carries the result (LoadRipParams).
type RipParams struct {
	TempoPrior float64 `toml:"tempo_prior"` // the BPM the tempo prior centres on
	TempoWidth float64 `toml:"tempo_width"` // the prior's width, in octaves
	Tightness  float64 `toml:"tightness"`   // how hard beat tracking holds to the period

	OnsetFloor float64 `toml:"onset_floor"` // a hit rises this far above its band's local median
	OnsetMin   float64 `toml:"onset_min"`   // and this far, as a share of the band's loudest, at least
	OnsetGap   float64 `toml:"onset_gap"`   // seconds between two hits of one drum
	KickShare  float64 `toml:"kick_share"`  // the share of a hit's percussive rise under 100 Hz that makes it a kick
	SnareShare float64 `toml:"snare_share"` // the share between 200 Hz and 4 kHz a snare has at least
	SnareNoise float64 `toml:"snare_noise"` // and the share above 5 kHz its wires give it at least
	HatShare   float64 `toml:"hat_share"`   // the share above 5 kHz that makes a hit a hihat

	Harmonics     int     `toml:"harmonics"`      // partials summed into a pitch's salience
	HarmonicDecay float64 `toml:"harmonic_decay"` // the weight ratio between successive partials
	Voicing       float64 `toml:"voicing"`        // the salience, against the recording's loud frames, under which a frame carries no note
	Jump          float64 `toml:"jump"`           // the pitch track's cost per semitone moved
	Switch        float64 `toml:"switch"`         // its cost of a note starting or ending
	Retrigger     float64 `toml:"retrigger"`      // the fraction of a note's peak salience it dips under before a rise is a new note
	MinNote       float64 `toml:"min_note"`       // seconds a note lasts at least
	SecondVoice   float64 `toml:"second_voice"`   // the strength, against the melody's, a second voice in its register is kept from; 0 hears one
	Compress      float64 `toml:"compress"`       // the power the harmonic spectrum is raised to before the salience, under one to flatten the partials saturation piles on a note; one leaves it
	Cross         float64 `toml:"cross"`          // what the first melody line pays a frame to be the quieter of the two; the lead changes hands over a longer rest or crossing
	Strike        float64 `toml:"strike"`         // how sure a front end that hears each pitch struck must be before the strike starts a note
	StrikeHold    float64 `toml:"strike_hold"`    // the share of the register's reference a struck pitch's salience reaches within MinNote, or the strike is no note
	StrikeGap     float64 `toml:"strike_gap"`     // seconds within which a pitch struck again is the same strike
	StrikeAgain   float64 `toml:"strike_again"`   // how sure a strike of a pitch still sounding must be to end its note and start another
	Release       float64 `toml:"release"`        // how sure a front end that hears each pitch let go must be before the release ends its note
}

// ripDrumsPerMinute is the least a drum strikes to be a part of the music,
// ripLineNote and ripLineStrength how long and strong a bass line's note is to stand
// without a strike, and ripOwnPitch the dB a low note's own pitch stands at least
// against its next partials, under which it is a higher note's shadow
const (
	ripDrumsPerMinute = 4
	ripLineNote       = 0.15
	ripLineStrength   = 0.8
	ripOwnPitch       = -10
)

func DefaultRipParams() RipParams {
	return RipParams{
		TempoPrior: 120, TempoWidth: 1, Tightness: 100,
		OnsetFloor: 2.0, OnsetMin: 0.1, OnsetGap: 0.06, KickShare: 0.06, SnareShare: 0.45, SnareNoise: 0.1, HatShare: 0.3,
		Harmonics: 6, HarmonicDecay: 0.8, Voicing: 0.3, Jump: 0.06, Switch: 0.35, Retrigger: 0.4, MinNote: 0.06,
		SecondVoice: 0.5, Compress: 1, Cross: 0.003, Strike: 0.8, StrikeHold: 0.9, StrikeGap: 0.12, StrikeAgain: 0.95, Release: 0.8,
	}
}

// ripGene is one parameter as a search sees it: its range, and where it lives.
type ripGene struct {
	name     string
	min, max float64
	at       func(*RipParams) *float64
}

// ripGenes is the parameters in gene order with the range each is sane in; the
// partial count is the one integer, rounded when read back.
var ripGenes = []ripGene{
	{"tempo_prior", 90, 150, func(p *RipParams) *float64 { return &p.TempoPrior }},
	{"tempo_width", 0.5, 2, func(p *RipParams) *float64 { return &p.TempoWidth }},
	{"tightness", 20, 400, func(p *RipParams) *float64 { return &p.Tightness }},
	{"onset_floor", 1.2, 4, func(p *RipParams) *float64 { return &p.OnsetFloor }},
	{"onset_min", 0.02, 0.3, func(p *RipParams) *float64 { return &p.OnsetMin }},
	{"onset_gap", 0.03, 0.12, func(p *RipParams) *float64 { return &p.OnsetGap }},
	{"kick_share", 0.02, 0.3, func(p *RipParams) *float64 { return &p.KickShare }},
	{"snare_share", 0.3, 0.7, func(p *RipParams) *float64 { return &p.SnareShare }},
	{"snare_noise", 0.02, 0.3, func(p *RipParams) *float64 { return &p.SnareNoise }},
	{"hat_share", 0.15, 0.6, func(p *RipParams) *float64 { return &p.HatShare }},
	{"harmonics", 3, 8, nil},
	{"harmonic_decay", 0.5, 0.95, func(p *RipParams) *float64 { return &p.HarmonicDecay }},
	{"voicing", 0.1, 0.6, func(p *RipParams) *float64 { return &p.Voicing }},
	{"jump", 0.01, 0.2, func(p *RipParams) *float64 { return &p.Jump }},
	{"switch", 0.1, 0.8, func(p *RipParams) *float64 { return &p.Switch }},
	{"retrigger", 0.2, 0.7, func(p *RipParams) *float64 { return &p.Retrigger }},
	{"min_note", 0.03, 0.15, func(p *RipParams) *float64 { return &p.MinNote }},
	{"second_voice", 0, 0.9, func(p *RipParams) *float64 { return &p.SecondVoice }},
	{"compress", 0.3, 1, func(p *RipParams) *float64 { return &p.Compress }},
	{"cross", 0, 0.02, func(p *RipParams) *float64 { return &p.Cross }},
	{"strike", 0.2, 0.99, func(p *RipParams) *float64 { return &p.Strike }},
	{"strike_hold", 0.3, 1, func(p *RipParams) *float64 { return &p.StrikeHold }},
	{"strike_gap", 0.03, 0.3, func(p *RipParams) *float64 { return &p.StrikeGap }},
	{"strike_again", 0.5, 0.99, func(p *RipParams) *float64 { return &p.StrikeAgain }},
	{"release", 0.2, 0.99, func(p *RipParams) *float64 { return &p.Release }},
}

func (g *ripGene) get(p *RipParams) float64 {
	if g.at == nil {
		return float64(p.Harmonics)
	}
	return *g.at(p)
}

func (g *ripGene) set(p *RipParams, v float64) {
	if g.at == nil {
		p.Harmonics = int(math.Round(v))
		return
	}
	*g.at(p) = v
}

// RipGeneRanges is the range of each gene of Genes, in its order, for a search
// to stay inside.
func RipGeneRanges() [][2]float64 {
	out := make([][2]float64, len(ripGenes))
	for i, g := range ripGenes {
		out[i] = [2]float64{g.min, g.max}
	}
	return out
}

// Genes is the parameters as a vector a search moves, in RipGeneRanges' order.
func (p RipParams) Genes() []float64 {
	out := make([]float64, len(ripGenes))
	for i := range ripGenes {
		out[i] = ripGenes[i].get(&p)
	}
	return out
}

// RipParamsFromGenes is Genes' inverse, each gene held to its range.
func RipParamsFromGenes(genes []float64) RipParams {
	p := DefaultRipParams()
	for i := range ripGenes {
		if i < len(genes) {
			ripGenes[i].set(&p, min(max(genes[i], ripGenes[i].min), ripGenes[i].max))
		}
	}
	return p
}

// ValidateRipParams refuses a parameter outside its range, by its key.
func ValidateRipParams(p RipParams) error {
	for i := range ripGenes {
		if v := ripGenes[i].get(&p); !finite(v) || v < ripGenes[i].min || v > ripGenes[i].max {
			return fmt.Errorf("rip: %s %v outside [%v,%v]", ripGenes[i].name, v, ripGenes[i].min, ripGenes[i].max)
		}
	}
	return nil
}

// ripFile is the root of a rip parameters document.
type ripFile struct {
	Rip RipParams `toml:"rip"`
}

// LoadRipParams reads a [rip] table, every key present and in range; a key the
// document omits keeps its default.
func LoadRipParams(data []byte) (RipParams, error) {
	raw, err := toml.NewParser(data).Parse()
	if err != nil {
		return RipParams{}, fmt.Errorf("rip params: %w", err)
	}
	if err := checkKeys("", raw, reflect.TypeOf(ripFile{})); err != nil {
		return RipParams{}, fmt.Errorf("rip params: %w", err)
	}
	f := ripFile{Rip: DefaultRipParams()}
	if err := toml.Decode(raw, &f); err != nil {
		return RipParams{}, fmt.Errorf("rip params: %w", err)
	}
	if err := ValidateRipParams(f.Rip); err != nil {
		return RipParams{}, err
	}
	return f.Rip, nil
}

// MarshalRipParams writes the parameters as a document LoadRipParams reads.
func MarshalRipParams(p RipParams) ([]byte, error) {
	return toml.Marshal(ripFile{Rip: p})
}

// Rip is what the analysis heard in a recording: its tempo and meter, where its
// bars fall, its key, the chord root each bar leans on, and per bar the drum hits,
// the melody and the bass line as steps of the engine's grid. Patterns writes it
// as patterns the sequencer plays.
type Rip struct {
	BPM         int
	Meter       Meter   // its bars', whose beats Beats are
	Tuning      float64 // cents the recording sits off A440; its pitches were read on it
	Root        int     // MIDI, in the bass octave as the engine's harmony root is
	Scale       ScaleID
	Progression []int     // chord root degree per bar, as SetHarmony takes it
	Downbeat    float64   // seconds into the recording the first full bar starts
	Beats       []float64 // seconds of each beat of the meter, the first bar's downbeat through the last bar's end
	Bars        []RipBar
	Params      RipParams
	// MelodyVoice, UnderVoice and BassVoice are the voices a front end with a
	// voice head heard the melody, the line under it and the bass played with,
	// nil otherwise; stapes fits them when none was heard
	MelodyVoice, UnderVoice, BassVoice *VoiceDef
	// Notes is the transcript before the grid: every note heard, in time
	Notes []RipNote
	// Tone is how each drum sounds in the recording, what Kit matches the
	// registered drums to; unread for a score
	Tone [InstrClap]RipTone
}

// RipNote is one note as heard, in seconds of the recording, before it was
// quantized: the sample a voice is fitted to.
type RipNote struct {
	Start, End float64
	MIDI       int
	Vel        float64
	Bass       bool // under E3, the bass register
	Second     bool // the melody register's second line, heard under the louder
	// Bright is a melody-register note's fundamental's share of its first six
	// partials, 1 for a pure tone; RipUnmeasured for a bass note, a note given
	// as text, and one whose fundamental a concurrent note covered throughout
	Bright float64
}

// RipUnmeasured is a RipNote's Bright where none was measured, never a share
const RipUnmeasured = -1.0

// RipHit is a drum's hit in seconds of a recording
type RipHit struct {
	Start float64
	Drum  InstrumentType // InstrKick, InstrSnare or InstrHihat
	Vel   float64
}

// Hits are the drums' hits in seconds, each bar's steps placed on its beats
func (r *Rip) Hits() []RipHit {
	var out []RipHit
	per, sub := r.Meter.Beats(), r.Meter.Beat()
	for b, bar := range r.Bars {
		for d, steps := range bar.Drums {
			for _, s := range steps {
				i := per*b + s.Pos/sub
				if i+1 >= len(r.Beats) {
					continue
				}
				span := (r.Beats[i+1] - r.Beats[i]) / float64(sub)
				out = append(out, RipHit{Start: r.Beats[i] + span*float64(s.Pos%sub), Drum: InstrumentType(d), Vel: s.Vel})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b RipHit) int { return cmpFloat(a.Start, b.Start) })
	return out
}

// RipBar is one bar: each drum's hits, kick, snare and hihat in instrument order,
// and the melody and bass notes, whose degrees are relative to the Rip's key.
type RipBar struct {
	Drums  [InstrClap][]Step
	Melody []Step
	Under  []Step // the melody register's second line, a voice of its own
	Bass   []Step
}

// The analysis rate and transform, 93 ms frames of 10.8 Hz bins every 11.6 ms,
// the bands the drums are told apart by, the pitches the salience spans and the
// log-mel bands a front-end model hears: the frame grid every front end keeps.
const (
	RipRate     = 22050
	RipN        = 2048
	RipHop      = 256
	RipFrameSec = float64(RipHop) / RipRate
	RipMelBands = 229
	ripKernel   = 17 // frames and bins a median spans in the harmonic-percussive split
	// ripOnsetLead is the frames an event reads early under the centred window;
	// the beat tracker shares it, and a pinned grid takes it on so a note on the
	// beat lands on its step rather than the one before
	ripOnsetLead = 2
	ripSubHz     = 100.0 // a kick's body is under it; a snare's is not
	ripKickHz    = 200.0
	ripSnareHz   = 4000.0
	ripHatHz     = 5000.0
	RipMinMIDI   = 28 // E1
	RipBassTop   = 52 // the bass register's rows end under E3, where the long window stops
	RipBassReach = 57 // the bass line is decoded up to A3, over the melody's lowest rows; settle gives a note both hear to the line it continues
	RipMelodyLow = 53 // the melody from F3 up
	RipMaxMIDI   = 97 // C7 exclusive
	RipPitches   = RipMaxMIDI - RipMinMIDI
	ripMaxSec    = 20 * 60
	// a note's Bright reads its first partials under a window twice the rip's,
	// each the peak within a share of k times its frequency
	ripBrightN        = 2 * RipN
	ripBrightPartials = 6
	ripBrightSpread   = 0.03
)

// RipFrames is a recording as the rip's decoders read it, a value per frame every
// Hop seconds: onset strength, each drum's (kick, snare, hihat), each pitch's
// salience from E1 to C7 (the bass's by its first partials, the melody's with the
// bass cancelled) and each register's chroma. A front end fills it: this package's
// analysis, or a model (ripmodel), which may also give each frame's wave and voice.
type RipFrames struct {
	Hop                float64
	Tuning             float64 // cents the recording's pitches sit off A440, as the front end heard them
	Flux               []float64
	Onset              [InstrClap][]float64
	Salience           []float32 // frames × RipPitches, MIDI RipMinMIDI up
	Chroma, BassChroma [][12]float64
	Wave               []float32 // frames × len(VoiceWaves), or nil
	Voice              []float32 // frames × VoiceGeneCount, or nil
	Strike             []float32 // frames × RipPitches, how surely each pitch is struck there, or nil
	Release            []float32 // frames × RipPitches, how surely each pitch is let go there, or nil
	Velocity           []float32 // frames × RipPitches, how hard a pitch struck there was struck, or nil
}

func (f *RipFrames) frames() int { return len(f.Flux) }

// RipFrontEnd hears a recording into frames
type RipFrontEnd interface {
	Hear(x []float64, rate int, p RipParams) (*RipFrames, error)
}

// Ripper rips with a front end of its choosing; a nil FrontEnd is the package's
// own analysis.
type Ripper struct {
	// Grid is the tempo and downbeat to read the recording on, nil to decode them
	Grid     *RipGrid
	Params   RipParams
	FrontEnd RipFrontEnd
	// Lead is which of the melody register's two lines is the melody, the
	// busier by default
	Lead LeadRule
	// Meter pins the bars' meter, the bar line still decoded; nil decodes it,
	// or reads 4/4 under a Grid pinned with its downbeat
	Meter *Meter
}

// RipGrid pins the grid a recording is read on, as a render's is known, so a
// rip of a render grades the render and not the decoders: a tempo and a
// downbeat, or the tempo alone when Downbeat is negative, the bar line decoded;
// or Beats, the beats themselves in seconds from the first bar's downbeat
// through the last bar's end, for a render whose tempo moves bar by bar.
type RipGrid struct {
	BPM      int
	Downbeat float64 // seconds; negative to decode the bar line at the tempo
	Beats    []float64
}

// pinned is the grid a RipGrid describes in meter m: its beats a tempo apart
// through the recording, from before the downbeat to its end, the last beat
// allowed half a beat past the end so a render of whole bars keeps its last
func (f *RipFrames) pinned(gr *RipGrid, m Meter) ripGrid {
	var beats []int
	before := 0
	if gr.Beats != nil {
		for _, s := range gr.Beats {
			beats = append(beats, max(int(math.Round(s/f.Hop))-ripOnsetLead, 0))
		}
		beats = beats[:len(beats)-(len(beats)-1)%m.Beats()] // whole bars
		g := ripGrid{m: m}
		for i := 0; i < len(beats); i += m.Beats() {
			g.lines = append(g.lines, beats[i])
		}
		return g
	}
	beat := 60 / float64(max(gr.BPM, 1)) / f.Hop * float64(m.Beat()) / StepsPerBeat
	start := max(gr.Downbeat, 0) / f.Hop
	before = int(start / beat)
	for i := -before; ; i++ {
		fr := int(math.Round(start+float64(i)*beat)) - ripOnsetLead
		if float64(fr) > float64(f.frames())+beat/2 {
			break
		}
		beats = append(beats, max(fr, 0))
	}
	g := ripGrid{m: m}
	for i := before; i < len(beats); i += m.Beats() {
		g.lines = append(g.lines, beats[i])
	}
	return g
}

// pinnedMeter is the meter pinned, 4/4 when none is
func (rp Ripper) pinnedMeter() Meter {
	if rp.Meter != nil {
		return *rp.Meter
	}
	return Meter{}
}

// RipWAV reads a WAV stream and rips it with the package's analysis.
func RipWAV(r io.Reader, p RipParams) (*Rip, error) {
	return Ripper{Params: p}.WAV(r)
}

// RipSamples rips a mono recording at rate with the package's analysis.
func RipSamples(x []float64, rate int, p RipParams) (*Rip, error) {
	return Ripper{Params: p}.Samples(x, rate)
}

// WAV reads a WAV stream and rips it.
func (rp Ripper) WAV(r io.Reader) (*Rip, error) {
	x, rate, err := ReadWAV(r)
	if err != nil {
		return nil, err
	}
	return rp.Samples(x, rate)
}

// Samples rips a mono recording at rate: tempo and bars first, then the key, the
// drums on a sixteenth grid between the beats, and the melody and bass as pitch
// tracks cut into notes on that grid. A note off the key lands on its nearest
// degree with the semitones left as an accidental. A front end that names
// voices leaves the registers' voices on the rip.
func (rp Ripper) Samples(x []float64, rate int) (*Rip, error) {
	if rate <= 0 || len(x) < 4*rate {
		return nil, errors.New("rip: the recording is shorter than four seconds")
	}
	if len(x) > ripMaxSec*rate {
		x = x[:ripMaxSec*rate]
	}
	p := rp.params()
	raw := x
	x = resample(x, rate, RipRate) // once, for the front end and the notes' partials
	f, err := rp.Hear(x, RipRate)
	if err != nil {
		return nil, err
	}
	if f.frames() < 2 {
		return nil, errors.New("rip: the front end heard nothing")
	}
	hits := f.drumHits(p)
	root, scale := f.key()
	bass, melody := f.lines(x, p)
	bass, melody = settle(bass, melody)
	bass, melody = f.account(x, p, bass, melody)
	melody = streams(melody, f.Hop, rp.Lead)
	if t := musicStart(x); t > 0 {
		hits, bass, melody = heardFrom(t, hits, bass, melody)
	}
	var bpm float64
	var g ripGrid
	if rp.Grid != nil && (rp.Grid.Downbeat >= 0 || rp.Grid.Beats != nil) {
		bpm = float64(rp.Grid.BPM)
		g = f.pinned(rp.Grid, rp.pinnedMeter())
		if gb := rp.Grid.Beats; len(gb) > 1 && bpm == 0 { // the mean of the beats given
			bpm = 60 / ((gb[len(gb)-1] - gb[0]) / float64(len(gb)-1)) * float64(g.m.Beat()) / StepsPerBeat
		}
	} else {
		var period float64
		if rp.Grid != nil { // the tempo given, the beats tracked at it and the bar line decoded
			bpm = float64(min(max(rp.Grid.BPM, MinBPM), MaxBPM))
			period = 60 / bpm / f.Hop
		} else {
			bpm, period = f.tempo(p)
		}
		k := f.tatum(period, f.beats(period, p))
		pulses := f.beats(period/float64(k), p)
		if len(pulses) < (BeatsPerBar+1)*k {
			return nil, errors.New("rip: no full bar found")
		}
		var err error
		if g, bpm, err = f.meter(pulses, k, bpm, hits, root%12, bass, melody, rp.Meter); err != nil {
			return nil, err
		}
		g = g.pickup(slices.Concat(melody, bass), hits)
	}
	if g.bars() < 1 {
		return nil, errors.New("rip: no full bar found")
	}
	r := &Rip{BPM: int(math.Round(bpm)), Meter: g.m, Tuning: f.Tuning, Root: root, Scale: scale, Downbeat: float64(g.lines[0]) * f.Hop, Bars: make([]RipBar, g.bars()), Params: p}
	r.Progression = f.progression(g, r.Root, r.Scale)
	r.Beats = make([]float64, g.m.Beats()*g.bars()+1)
	for i := range r.Beats {
		r.Beats[i] = float64(g.beat(i)) * f.Hop
	}
	g.drums(r, hits)
	var at [InstrClap][]float64
	for d := range hits {
		for _, h := range hits[d] {
			at[d] = append(at[d], float64(h.frame)*f.Hop)
		}
	}
	r.Tone = drumTones(x, raw, rate, hitStarts(x, at))
	// the pitch paths start late against the onsets the beats were set by: the
	// notes' median lag comes out before they are quantized
	shift := int(math.Round(g.lag(slices.Concat(melody, bass)) * g.stepFrames()))
	early := func(n ripNote) ripNote {
		n.start, n.end = n.start-shift, n.end-shift
		return n
	}
	var firsts, seconds []ripNote
	for _, n := range melody {
		if n.second {
			seconds = append(seconds, n)
			g.place(r, early(n), func(b *RipBar) *[]Step { return &b.Under })
			continue
		}
		firsts = append(firsts, n)
		g.place(r, early(n), func(b *RipBar) *[]Step { return &b.Melody })
	}
	for _, n := range bass {
		g.place(r, early(n), func(b *RipBar) *[]Step { return &b.Bass })
	}
	r.MelodyVoice, r.UnderVoice, r.BassVoice = voiceOf(melody), voiceOf(melody), voiceOf(bass)
	f.bright(x, melody, bass)
	for i, ns := range [2][]ripNote{melody, bass} {
		for _, n := range ns {
			if i == 1 {
				n.bright = RipUnmeasured
			}
			r.Notes = append(r.Notes, RipNote{Start: float64(n.start) * f.Hop, End: float64(n.end) * f.Hop, MIDI: n.midi, Vel: n.vel, Bass: i == 1, Second: n.second, Bright: n.bright})
		}
	}
	slices.SortFunc(r.Notes, func(a, b RipNote) int { return cmp.Compare(a.Start, b.Start) })
	return r, nil
}

// bright sets each melody-register note's share of its fundamental in its
// first partials' magnitudes, each partial averaged over the note's frames of
// the recording x (at RipRate) on which no partial of a note under the frame's
// window, melody or bass, falls within its spread; a note whose fundamental is
// covered on every frame is RipUnmeasured
func (f *RipFrames) bright(x []float64, melody, bass []ripNote) {
	all := slices.Concat(melody, bass)
	reach := int(float64(ripBrightN/2) / RipRate / f.Hop) // frames a window spans either side
	near := make([][]int, f.frames())                     // the notes under each frame's window
	for i, n := range all {
		for t := max(n.start-reach, 0); t < min(n.end+reach, len(near)); t++ {
			near[t] = append(near[t], i)
		}
	}
	tune := math.Pow(2, f.Tuning/1200)
	freq := func(i int) float64 { return NoteFreq(all[i].midi) * tune }
	binHz := float64(RipRate) / ripBrightN
	type partials struct{ sum, frames [ripBrightPartials]float64 }
	acc := make([]partials, len(melody))
	s := newSTFT(ripBrightN)
	for t, ns := range near {
		measured := func(i int) bool { return i < len(melody) && all[i].start <= t && t < all[i].end }
		if !slices.ContainsFunc(ns, measured) {
			continue
		}
		mag := s.at(x, int(math.Round(float64(t)*f.Hop*RipRate)))
		for _, i := range ns {
			if !measured(i) {
				continue
			}
		partial:
			for k := range ripBrightPartials {
				hz := float64(k+1) * freq(i)
				hi := int(hz * (1 + ripBrightSpread) / binHz)
				if hi >= len(mag) {
					break
				}
				for _, j := range ns {
					for h := 1; j != i && h <= ripBrightPartials; h++ {
						if math.Abs(float64(h)*freq(j)-hz) <= ripBrightSpread*hz {
							continue partial
						}
					}
				}
				lo := min(int(math.Ceil(hz*(1-ripBrightSpread)/binHz)), hi) // under 90 Hz the spread holds no bin
				acc[i].sum[k] += slices.Max(mag[lo : hi+1])
				acc[i].frames[k]++
			}
		}
	}
	for i, a := range acc {
		melody[i].bright = RipUnmeasured
		if a.frames[0] == 0 {
			continue
		}
		var total float64
		for k, n := range a.frames {
			if n > 0 {
				total += a.sum[k] / n
			}
		}
		if total > 0 {
			melody[i].bright = a.sum[0] / a.frames[0] / total
		}
	}
}

// MelDistance is how far two sets of mel frames are apart, the mean squared
// band difference over the longer's frames, the shorter silent past its end, so
// a note that rings on or dies early pays for it.
func MelDistance(a, b [][]float32) float64 {
	n := max(len(a), len(b))
	if n == 0 {
		return 0
	}
	var sum float64
	var count int
	for t := range n {
		var fa, fb []float32
		if t < len(a) {
			fa = a[t]
		}
		if t < len(b) {
			fb = b[t]
		}
		bands := max(len(fa), len(fb))
		for k := range bands {
			var x, y float64
			if k < len(fa) {
				x = float64(fa[k])
			}
			if k < len(fb) {
				y = float64(fb[k])
			}
			sum += (x - y) * (x - y)
			count++
		}
	}
	return sum / float64(max(count, 1))
}

// BarDistance is how far two bars are apart, the steps one has and the other
// lacks over the steps either has, a hit by its drum and step and a note by its
// step and pitch: zero for the same bar, one for bars sharing nothing.
func BarDistance(a, b *RipBar) float64 {
	keys := func(bar *RipBar) map[[5]int]bool {
		m := map[[5]int]bool{}
		for d := range bar.Drums {
			for _, s := range bar.Drums[d] {
				m[[5]int{d, s.Pos, 0, 0, 0}] = true
			}
		}
		for _, s := range bar.Melody {
			m[[5]int{10, s.Pos, s.Deg, s.Oct, s.Acc}] = true
		}
		for _, s := range bar.Under {
			m[[5]int{10, s.Pos, s.Deg, s.Oct, s.Acc}] = true
		}
		for _, s := range bar.Bass {
			m[[5]int{11, s.Pos, s.Deg, s.Oct, s.Acc}] = true
		}
		return m
	}
	ka, kb := keys(a), keys(b)
	union, differ := len(ka), 0
	for k := range kb {
		if ka[k] {
			continue
		}
		union++
		differ++
	}
	for k := range ka {
		if !kb[k] {
			differ++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(differ) / float64(union)
}

// voiceOf is the voice a register's notes were heard played with, each note's genes
// weighed by its length; nil when the front end named none. It plays on a modal bank
// whatever wave the head names, as the head names saw through a phone's narrow band.
func voiceOf(notes []ripNote) *VoiceDef {
	var genes []float64
	var total float64
	for _, n := range notes {
		if n.voice == nil {
			continue
		}
		if genes == nil {
			genes = make([]float64, len(n.voice))
		}
		w := float64(n.end - n.start)
		for i, g := range n.voice {
			genes[i] += g * w
		}
		total += w
	}
	if genes == nil || total == 0 {
		return nil
	}
	for i := range genes {
		genes[i] /= total
	}
	v := VoiceFromGenes("modal", genes)
	v.Unison = 1 // the genes' mean lands on two, and two detuned oscillators beat as a slow swell
	lasting(v, notes)
	return v
}

// lasting raises a voice's natural fade to its line's long notes, still within 12 dB
// of their start at their end: the heads' mean genes would fade them before their release.
func lasting(v *VoiceDef, notes []ripNote) {
	lens, keys := make([]int, 0, len(notes)), make([]int, 0, len(notes))
	for _, n := range notes {
		lens, keys = append(lens, n.end-n.start), append(keys, n.midi)
	}
	slices.Sort(lens)
	slices.Sort(keys)
	tau := float64(lens[len(lens)*9/10]) * float64(RipHop) / float64(RipRate) / 1.4
	at := math.Pow(keyRefHz/(440*math.Pow(2, float64(keys[len(keys)/2]-69)/12)), v.DecayTrack)
	v.DecayTime = min(max(v.DecayTime, tau/at), 4)
	v.Ring = min(max(v.Ring, 6.9*tau/at), 8)
}

// Voices is the voices the rip heard the registers played with, named after
// name as Patterns names their tracks; none for a front end without a voice head.
func (r *Rip) Voices(name string) []*VoiceDef {
	var out []*VoiceDef
	if r.MelodyVoice != nil {
		v := r.MelodyVoice.Clone()
		v.Name = name + "_melody_voice"
		out = append(out, v)
	}
	if r.UnderVoice != nil {
		v := r.UnderVoice.Clone()
		v.Name = name + "_under_voice"
		out = append(out, v)
	}
	if r.BassVoice != nil {
		v := r.BassVoice.Clone()
		v.Name = name + "_bass_voice"
		out = append(out, v)
	}
	return out
}

// Patterns writes the rip as patterns of up to bars bars each, named
// <name>_beat_<n>, <name>_melody_<n> and <name>_bass_<n>, in a group of their
// own and no tier, so they play where they are placed; a window with nothing in
// a part has no pattern for it. A register's track names the voice Voices heard
// for it, or the lead and the bass when none was.
func (r *Rip) Patterns(name string, bars int) []*PatternDef {
	bars = min(max(bars, 1), MaxPatternLen/r.Meter.Steps())
	var out []*PatternDef
	for w := 0; w*bars < len(r.Bars); w++ {
		lo, hi := w*bars, min((w+1)*bars, len(r.Bars))
		for part := range RipParts {
			if p := r.Pattern(fmt.Sprintf("%s_%s_%d", name, RipPartNames[part], w+1), name, part, lo, hi); p != nil {
				out = append(out, p)
			}
		}
	}
	return out
}

// bassRows is the harmonic magnitude of the bass register's bins, up to the
// third partial of its top, under a window four times the rip's, frame for
// frame with it; the split is the rip's, a median over frames per bin against a
// median over bins per frame, the latter over four times the bins since the
// bins are four times as fine.
func (f *RipFrames) bassRows(x []float64) ([][]float64, float64) {
	const n, half = 4 * RipN, ripKernel / 2
	width := float64(RipRate) / n
	bins := int(NoteFreq(RipBassTop)*3/width) + 3
	var rows [][]float64
	spectrogram(x, n, RipHop, func(_ int, mag []float64) {
		rows = append(rows, slices.Clone(mag[:bins]))
	})
	out := make([][]float64, len(rows))
	timeMed := make([]*slidingMedian, bins)
	for k := range timeMed {
		timeMed[k] = newSlidingMedian(ripKernel)
	}
	const fk = 4*ripKernel + 1
	freqMed := newSlidingMedian(fk)
	hrow, prow := make([]float64, bins), make([]float64, bins)
	zero := make([]float64, bins)
	for t := 0; t < len(rows)+half; t++ {
		row := zero
		if t < len(rows) {
			row = rows[t]
		}
		for k := range timeMed {
			hrow[k] = timeMed[k].push(row[k])
		}
		c := t - half
		if c < 0 {
			continue
		}
		src := rows[c]
		freqMed.reset()
		for k := 0; k < bins+fk/2; k++ {
			v := 0.0
			if k < bins {
				v = src[k]
			}
			m := freqMed.push(v)
			if j := k - fk/2; j >= 0 {
				prow[j] = m
			}
		}
		h := make([]float64, bins)
		for k := range h {
			h2, p2 := hrow[k]*hrow[k], prow[k]*prow[k]
			h[k] = src[k] * h2 / (h2 + p2 + 1e-12)
		}
		out[c] = h
	}
	return out, width
}

// analysis is the package's own front end: one pass over the spectrogram, each
// frame split into a harmonic and a percussive part by the medians across time and
// across frequency around it, so a frame is finished ripKernel/2 frames after it
// arrives; the drums by where the percussive magnitude rose, the pitches by a
// harmonic salience.
type analysis struct{}

func (analysis) Hear(x []float64, rate int, p RipParams) (*RipFrames, error) {
	x = resample(x, rate, RipRate)
	const bins, half = RipN/2 + 1, ripKernel / 2
	binHz := float64(RipRate) / RipN
	f := &RipFrames{Hop: RipFrameSec, Tuning: tuning(x)}
	var lo, mid, hi, rSub, rLo, rMid, rHi []float64 // per band: log flux and linear rise
	ring := make([][]float64, ripKernel)
	for i := range ring {
		ring[i] = make([]float64, bins)
	}
	timeMed := make([]*slidingMedian, bins)
	for k := range timeMed {
		timeMed[k] = newSlidingMedian(ripKernel)
	}
	freqMed := newSlidingMedian(ripKernel)
	hm, pm, prevPM, lp, prevLP, hrow, prow := make([]float64, bins), make([]float64, bins), make([]float64, bins), make([]float64, bins), make([]float64, bins), make([]float64, bins), make([]float64, bins)
	cancelled := make([]float64, bins)
	weights := make([]float64, max(p.Harmonics, 1))
	for h := range weights {
		weights[h] = math.Pow(p.HarmonicDecay, float64(h))
	}
	pitchHz := make([]float64, RipPitches) // the pitches where the recording's tuning puts them
	for i := range pitchHz {
		pitchHz[i] = NoteFreq(RipMinMIDI+i) * math.Pow(2, f.Tuning/1200)
	}
	subBin, loBin, midBin, hatBin := int(ripSubHz/binHz), int(ripKickHz/binHz), int(ripSnareHz/binHz), int(ripHatHz/binHz)
	// partialIn reads the magnitude at a frequency between the bins of a
	// transform of the given bin width, which tells neighbouring semitones apart
	// where a bin is wider than one; zero past the band
	partialIn := func(m []float64, hz, width float64) float64 {
		pos := hz / width
		b := int(pos)
		if b+1 >= len(m) {
			return 0
		}
		frac := pos - float64(b)
		return m[b]*(1-frac) + m[b+1]*frac
	}
	// salienceIn sums a pitch's partials, each weighted by its rank, scaled down
	// as the fundamental falls short of the strongest partial, which a note's own
	// octave above and a bass's second partial both do
	salienceIn := func(m []float64, width, f0 float64, n int) float64 {
		var s, top float64
		for h := 0; h < n && h < len(weights); h++ {
			v := partialIn(m, f0*float64(h+1), width)
			s, top = s+weights[h]*v, max(top, v)
		}
		if top > 0 {
			s *= min(1, partialIn(m, f0, width)/(0.3*top))
		}
		return s
	}
	salience := func(m []float64, f0 float64, n int) float64 { return salienceIn(m, binHz, f0, n) }
	// The bass register is also read from a window four times longer, whose bins
	// tell neighbouring semitones apart at 110 Hz where the short window's main
	// lobe spans two and the pitch path wobbles between them; its harmonic part
	// is split by the same medians over the bins the register's partials reach
	hLong, longHz := f.bassRows(x)
	// under one, the harmonic spectrum is flattened before the salience, so the
	// partials saturation piles on a note do not outweigh its fundamental
	gamma := p.Compress
	if gamma <= 0 || gamma > 1 {
		gamma = 1
	}
	var longC []float64
	if len(hLong) > 0 {
		longC = make([]float64, len(hLong[0]))
	}

	finish := func(c int) {
		row := ring[c%ripKernel]
		for k := range hrow {
			hrow[k] = timeMed[k].sorted[len(timeMed[k].sorted)/2]
		}
		freqMed.reset()
		for k := 0; k < bins+half; k++ {
			v := 0.0
			if k < bins {
				v = row[k]
			}
			m := freqMed.push(v)
			if j := k - half; j >= 0 {
				prow[j] = m
			}
		}
		var fl, fm, fh, rs, rl, rm, rh float64
		for k := range row {
			h2, p2 := hrow[k]*hrow[k], prow[k]*prow[k]
			mask := h2 / (h2 + p2 + 1e-12)
			hm[k], pm[k] = row[k]*mask, row[k]*(1-mask)
			if gamma < 1 {
				hm[k] = math.Pow(hm[k], gamma)
			}
			lp[k] = math.Log1p(100 * pm[k])
			d := max(lp[k]-prevLP[k], 0)
			r := max(pm[k]-prevPM[k], 0)
			switch {
			case k < subBin:
				fl, rs = fl+d, rs+r
			case k < loBin:
				fl, rl = fl+d, rl+r
			case k < midBin:
				fm, rm = fm+d, rm+r
			case k >= hatBin:
				fh, rh = fh+d, rh+r
			}
		}
		prevLP, lp = lp, prevLP
		prevPM, pm = pm, prevPM
		lo, mid, hi = append(lo, fl), append(mid, fm), append(hi, fh)
		rSub, rLo, rMid, rHi = append(rSub, rs), append(rLo, rl), append(rMid, rm), append(rHi, rh)
		// the chroma reads every pitch's full series: a ghost an octave up shares
		// the pitch class of what casts it
		var ch, bch [12]float64
		for i, f0 := range pitchHz {
			ch[(RipMinMIDI+i)%12] += salience(hm, f0, len(weights))
		}
		// the bass is the strongest pitch of its register by its first partials,
		// read from the long window; those partials come out of what the melody
		// is read from
		bass, second, bassAt, secondAt := -1, -1, 0.0, 0.0
		var longRow []float64
		if c < len(hLong) {
			longRow = hLong[c]
			if gamma < 1 {
				for k, v := range longRow {
					longC[k] = math.Pow(v, gamma)
				}
				longRow = longC
			}
		}
		for i := 0; i < RipBassTop-RipMinMIDI; i++ {
			// the short window says when the register sounds, the long one which
			// semitone: their geometric mean keeps the long window's pitch and the
			// short window's onsets and ends
			s := salience(hm, pitchHz[i], 3)
			if longRow != nil {
				s = math.Sqrt(s * salienceIn(longRow, longHz, pitchHz[i], 3))
			}
			f.Salience = append(f.Salience, float32(s))
			bch[(RipMinMIDI+i)%12] += s
			switch {
			case s > bassAt:
				bass, second, bassAt, secondAt = i, bass, s, bassAt
			case s > secondAt:
				second, secondAt = i, s
			}
		}
		// the bass's partials come out of what the melody is read from; a
		// neighbour nearly as strong, the other bin of a wobble, loses its too
		copy(cancelled, hm)
		for _, i := range []int{bass, second} {
			if i < 0 || i == second && (secondAt < 0.7*bassAt || second-bass > 2 || bass-second > 2) {
				continue
			}
			for h := 1; h <= 3; h++ {
				if b := int(pitchHz[i]*float64(h)/binHz + 0.5); b+1 < bins && b >= 1 {
					cancelled[b-1], cancelled[b], cancelled[b+1] = 0, 0, 0
				}
			}
		}
		for i := RipBassTop - RipMinMIDI; i < RipPitches; i++ {
			f.Salience = append(f.Salience, float32(salience(cancelled, pitchHz[i], len(weights))))
		}
		f.Chroma, f.BassChroma = append(f.Chroma, ch), append(f.BassChroma, bch)
	}
	frames := 0
	push := func(t int, mag []float64) {
		row := ring[t%ripKernel]
		copy(row, mag)
		for k, v := range mag {
			timeMed[k].push(v)
		}
		if c := t - half; c >= 0 {
			finish(c)
		}
	}
	spectrogram(x, RipN, RipHop, func(t int, mag []float64) {
		frames = t + 1
		push(t, mag)
	})
	zero := make([]float64, bins)
	for t := frames; t < frames+half; t++ { // the last frames' windows run past the end
		push(t, zero)
	}

	// Any onset: each band's flux against its own peak, the kick's band counting
	// double and the hihat's half, so a broadband hat does not outvote the drum
	// that keeps the beat. Each drum's: its band's flux where the rise sits as the
	// drum's does, a kick's under 100 Hz, a hihat's mostly above 5 kHz, a snare's in
	// the middle with some of its noise above 5 kHz, which a kick's click lacks.
	n := len(lo)
	peak := func(v []float64) float64 {
		m := 1e-9
		for _, x := range v {
			m = max(m, x)
		}
		return m
	}
	pl, pm2, ph := peak(lo), peak(mid), peak(hi)
	f.Flux = make([]float64, n)
	for d := range f.Onset {
		f.Onset[d] = make([]float64, n)
	}
	for t := range n {
		f.Flux[t] = 2*lo[t]/pl + mid[t]/pm2 + 0.5*hi[t]/ph
		var a, b, c, e float64
		for u := t; u <= t+1 && u < n; u++ {
			a, b, c, e = max(a, rSub[u]), max(b, rLo[u]), max(c, rMid[u]), max(e, rHi[u])
		}
		sum := a + b + c + e
		if sum == 0 {
			continue
		}
		sub, midShare, hiShare := a/sum, c/sum, e/sum
		if sub >= p.KickShare {
			f.Onset[InstrKick][t] = lo[t]
		}
		if midShare >= p.SnareShare && hiShare >= p.SnareNoise && hiShare < p.HatShare {
			f.Onset[InstrSnare][t] = mid[t]
		}
		if hiShare >= p.HatShare {
			f.Onset[InstrHihat][t] = hi[t]
		}
	}
	return f, nil
}

func (m *slidingMedian) reset() {
	m.n, m.pos = 0, 0
	m.sorted = m.sorted[:0]
}

// MelFrames is a recording as the log-mel frames a front-end model hears: the
// rip's frame grid, RipMelBands triangular bands from 30 Hz to the top of the
// band on the mel scale, the energy under each as log1p of a hundred times it,
// the recording's peak at one so a level change is not a different sound.
func MelFrames(x []float64, rate int) [][]float32 {
	x = resample(x, rate, RipRate)
	var peak float64
	for _, v := range x {
		peak = max(peak, math.Abs(v))
	}
	if peak > 0 {
		y := make([]float64, len(x))
		for i, v := range x {
			y[i] = v / peak
		}
		x = y
	}
	const bins = RipN/2 + 1
	mel := func(hz float64) float64 { return 2595 * math.Log10(1+hz/700) }
	hz := func(m float64) float64 { return 700 * (math.Pow(10, m/2595) - 1) }
	lo, hi := mel(30), mel(RipRate/2)
	edges := make([]float64, RipMelBands+2)
	for i := range edges {
		edges[i] = hz(lo+(hi-lo)*float64(i)/float64(RipMelBands+1)) * RipN / RipRate // in bins
	}
	var out [][]float32
	scale := 1 / float64(RipN/4) // a unit sine's peak magnitude under the window
	spectrogram(x, RipN, RipHop, func(_ int, mag []float64) {
		frame := make([]float32, RipMelBands)
		for b := range RipMelBands {
			l, c, r := edges[b], edges[b+1], edges[b+2]
			var e float64
			for k := max(int(l), 0); k <= int(r) && k < bins; k++ {
				var w float64
				switch fk := float64(k); {
				case fk < l || fk > r:
				case fk <= c:
					w = (fk - l) / max(c-l, 1e-9)
				default:
					w = (r - fk) / max(r-c, 1e-9)
				}
				m := mag[k] * scale
				e += w * m * m
			}
			frame[b] = float32(math.Log1p(100 * e))
		}
		out = append(out, frame)
	})
	return out
}

// tuning is the cents a recording's pitches sit off equal temperament at A440:
// over windows four times the frame, whose bins resolve it, every spectral peak
// from 55 Hz to 2.5 kHz votes its offset from the nearest semitone with its
// height, around the circle of a semitone; the mean's angle, zero when the
// votes agree on no offset, as noise and drums do not.
func tuning(x []float64) float64 {
	const n = 4 * RipN
	binHz := float64(RipRate) / n
	var re, im, sum float64
	spectrogram(x, n, n/2, func(_ int, mag []float64) {
		var top float64
		for _, v := range mag {
			top = max(top, v)
		}
		for k := max(int(55/binHz), 1); k <= int(2500/binHz) && k+1 < len(mag); k++ {
			if mag[k] <= mag[k-1] || mag[k] < mag[k+1] || mag[k] < 0.05*top {
				continue
			}
			a, b, c := math.Log(mag[k-1]+1e-12), math.Log(mag[k]+1e-12), math.Log(mag[k+1]+1e-12)
			peak := float64(k)
			if d := a - 2*b + c; d != 0 {
				peak += 0.5 * (a - c) / d
			}
			angle := 2 * math.Pi * 12 * math.Log2(peak*binHz/440)
			re, im, sum = re+mag[k]*math.Cos(angle), im+mag[k]*math.Sin(angle), sum+mag[k]
		}
	})
	if sum == 0 || math.Hypot(re, im)/sum < 0.3 {
		return 0
	}
	return math.Atan2(im, re) / (2 * math.Pi) * 100
}

// tempo is the lag the onset strength repeats at, under a log-Gaussian prior on
// the tempo and a comb that credits a lag with its double, folded into the
// sequencer's range; the period is in frames.
func (f *RipFrames) tempo(p RipParams) (bpm, period float64) {
	o := f.onsets()
	minLag, maxLag := int(60/240/f.Hop), int(60/60/f.Hop)
	acf := func(lag int) float64 {
		var s float64
		for t := lag; t < len(o); t++ {
			s += o[t] * o[t-lag]
		}
		return s / float64(max(len(o)-lag, 1))
	}
	score := func(lag int) float64 {
		b := 60 / (float64(lag) * f.Hop)
		prior := math.Exp(-0.5 * math.Pow(math.Log2(b/p.TempoPrior)/p.TempoWidth, 2))
		s := acf(lag)
		if 2*lag < len(o) {
			s += 0.5 * acf(2*lag)
		}
		return s * prior
	}
	best, bestScore := minLag, -1.0
	for lag := minLag; lag <= maxLag; lag++ {
		if s := score(lag); s > bestScore {
			best, bestScore = lag, s
		}
	}
	lag := float64(best)
	if best > minLag && best < maxLag { // the peak between its neighbours
		l, c, r := score(best-1), score(best), score(best+1)
		if d := l - 2*c + r; d < 0 {
			lag += 0.5 * (l - r) / d
		}
	}
	bpm = 60 / (lag * f.Hop)
	for bpm < ripTempoMin {
		bpm *= 2
	}
	for bpm > ripTempoMax {
		bpm /= 2
	}
	return bpm, 60 / (bpm * f.Hop)
}

// ripTempoMin and ripTempoMax bound the beat the tracker follows: the bars
// read over it name a tempo of their own, half or double, within the
// sequencer's wider range.
const (
	ripTempoMin = 80
	ripTempoMax = 200
)

// ripPulseMin is the fewest frames a tracked pulse spans, about 140 ms;
// ripTatumShare how much of the beat's onset autocorrelation a subdivision's
// must hold, at a local peak, to be the pulse tracked; ripThirds how much more
// onset each third of a beat holds than its quarters, or half its middle, for
// the beat to be a dotted quarter rather than swung
const (
	ripPulseMin   = 12
	ripTatumShare = 0.5
	ripThirds     = 1.5
)

// tatum is how many pulses the tracker follows in a beat of period frames:
// three when the beats hold onsets on both their thirds (thirds), else the
// finer of two or four whose lag is a local peak of the onset autocorrelation
// at least ripTatumShare of the beat's, each spanning ripPulseMin frames, else
// one. Every onset is a pulse there, so a ritardando keeps its count.
func (f *RipFrames) tatum(period float64, beats []int) int {
	if period/3 >= ripPulseMin && f.thirds(beats) {
		return 3
	}
	o := f.onsets()
	acf := func(lag int) float64 {
		var s float64
		for t := lag; t < len(o); t++ {
			s += o[t] * o[t-lag]
		}
		return s / float64(max(len(o)-lag, 1))
	}
	beat := acf(int(math.Round(period)))
	k := 1
	for _, n := range [...]int{2, 4} {
		lag := period / float64(n)
		if lag < ripPulseMin {
			continue
		}
		l := int(math.Round(lag))
		if v := acf(l); v >= ripTatumShare*beat && v >= acf(l-1) && v >= acf(l+1) {
			k = n
		}
	}
	return k
}

// thirds reports whether the onsets inside the beats fall on both their
// thirds, as a dotted quarter's eighths do, each by ripThirds over the onsets
// on the beats' quarters and over half those on their middles: a swung
// sixteenth sounds near the first third only and a shuffled eighth on the
// second, and a dotted quarter's sixteenths sound on its middle
func (f *RipFrames) thirds(beats []int) bool {
	o := f.onsets()
	var on [5]float64
	for i := 0; i+1 < len(beats); i++ {
		a, d := float64(beats[i]), float64(beats[i+1]-beats[i])
		for k, x := range [5]float64{1. / 4, 1. / 3, 1. / 2, 2. / 3, 3. / 4} {
			on[k] += peak(o, a+x*d)
		}
	}
	return min(on[1], on[3]) > ripThirds*max(on[0], on[4], on[2]/2)
}

// onsets is what the tempo and the beats are read from: the onset strength above
// its local mean, clipped at zero and scaled to unit deviation.
func (f *RipFrames) onsets() []float64 {
	n := f.frames()
	o := make([]float64, n)
	w := int(1 / f.Hop)
	sum := make([]float64, n+1)
	for t, v := range f.Flux {
		sum[t+1] = sum[t] + v
	}
	var sq float64
	for t := range o {
		a, b := max(t-w, 0), min(t+w+1, n)
		o[t] = max(f.Flux[t]-(sum[b]-sum[a])/float64(b-a), 0)
		sq += o[t] * o[t]
	}
	if sd := math.Sqrt(sq / float64(max(n, 1))); sd > 0 {
		for t := range o {
			o[t] /= sd
		}
	}
	return o
}

// beats places beats one period apart, give or take what the onsets justify: each
// frame's best score is its onset strength plus the best earlier beat's, less a
// penalty growing with the square of the log of the interval's ratio to the period.
func (f *RipFrames) beats(period float64, p RipParams) []int {
	o := f.onsets()
	n := len(o)
	score, back := make([]float64, n), make([]int, n)
	lo, hi := int(period/2+0.5), int(2*period+0.5)
	for t := range n {
		best, arg := 0.0, -1
		for d := lo; d <= hi && d <= t; d++ {
			s := score[t-d] - p.Tightness*math.Pow(math.Log(float64(d)/period), 2)
			if arg < 0 || s > best {
				best, arg = s, t-d
			}
		}
		score[t], back[t] = o[t]+best, arg
	}
	last := n - 1
	for t := max(n-int(period)-1, 0); t < n; t++ {
		if score[t] > score[last] {
			last = t
		}
	}
	var beats []int
	for t := last; t >= 0; t = back[t] {
		beats = append(beats, t)
	}
	slices.Reverse(beats)
	return beats
}

// ripGrid is the bars of meter m over the frames: the frame each bar line
// falls on, the last the end of the last bar; a bar's sixteenths are its span
// cut in the meter's steps.
type ripGrid struct {
	lines []int
	m     Meter
}

func (g *ripGrid) bars() int { return len(g.lines) - 1 }

// beat is the frame of beat i, counted through the bars in the meter's beats
func (g *ripGrid) beat(i int) int {
	per := g.m.Beats()
	if i >= per*g.bars() {
		return g.lines[g.bars()]
	}
	a, b := g.lines[i/per], g.lines[i/per+1]
	return a + int(math.Round(float64(b-a)*float64(i%per)/float64(per)))
}

// ripReading is one way to read bars of meter m over the tracked pulses, each
// unit steps, at tempo
type ripReading struct {
	m      Meter
	pulses []int
	unit   int // steps a pulse holds
	stride int // pulses a tracked beat holds
	tempo  float64
}

// ripMeterMargin is how far a reading's score must rise over the first's to
// be taken; ripMeterBars the bars of 4/4 the tracked beats must hold before
// any reading but 4/4 over them is tried
const (
	ripMeterMargin = 0.35
	ripMeterBars   = 6
)

// ripAllReadings offers every bar the harness grades
var ripAllReadings = false

// ripRepeatBars are the lags, in a reading's bars, its repetition is heard at
var ripRepeatBars = [...]int{1, 2, 4}

// ripRepeatMargin is how much less unlike itself (repeats) a reading within
// ripMeterMargin of the best must sound to take it; or its bars nearer their
// nearest other bar (nearest) by the share ripNearMargin and it less unlike
// itself by ripNearRepeat. Set over the labeled sets (doc/audio.md §9): Für
// Elise reads 3/8 on all four takes through two models, few 4/4 pieces flip.
var (
	ripRepeatMargin = 0.03
	ripNearMargin   = 0.07
	ripNearRepeat   = 0.015
)

// meter decodes the bars over the pulses tracked, k a beat of tempo bpm, and
// their tempo: the first reading holds unless another's bar path scores over
// it by ripMeterMargin (twice for a bar of two of the best's), or comes within
// it at its tempo and repeats more at its own bars or cuts them nearer their
// nearest; a compound meter names the same bar by its felt beats. pin fixes it.
func (f *RipFrames) meter(pulses []int, k int, bpm float64, hits [InstrClap][]ripHit, rootPC int, bass, melody []ripNote, pin *Meter) (ripGrid, float64, error) {
	readings, err := f.readings(pulses, k, bpm, pin)
	if err != nil {
		return ripGrid{}, 0, err
	}
	c := f.lineCues(hits, rootPC, bass)
	notes := slices.Concat(bass, melody)
	var best ripGrid
	var bestTempo, bestUnlike, bestNear, bestFelt float64
	bestScore := math.Inf(-1)
	for _, rd := range readings {
		lines, score, _, _, felt := f.barPath(rd, c)
		g := ripGrid{lines: lines, m: rd.m}
		var unlike, near float64
		if len(readings) > 1 {
			unlike, near = f.repeats(rd), g.nearest(notes, hits, rootPC)
		}
		need := ripMeterMargin
		same := best.bars() > 0 && rd.m.Steps() == best.m.Steps() // the same bar under another name
		if best.bars() > 0 && !same && rd.m.Compound() {          // a compound meter only names a simple reading's bar
			continue
		}
		nested := !same && best.bars() > 0 && rd.m.Steps()%best.m.Steps() == 0 // its bars two of best's: only stronger lines
		if nested {
			need *= 2
		}
		// a bar of two of the best's shares its lags, so only its lines can tell it;
		// the same bar is named by its score with the onsets on its other felt beats
		closer := !nested && !same && rd.tempo == bestTempo && score > bestScore-need && (unlike <= bestUnlike-ripRepeatMargin || near <= bestNear*(1-ripNearMargin) && unlike <= bestUnlike-ripNearRepeat)
		if bestScore > math.Inf(-1) && !closer && (same && score+felt <= bestScore+bestFelt || !same && score <= bestScore+need) {
			continue
		}
		best, bestTempo, bestScore, bestUnlike, bestNear, bestFelt = g, rd.tempo, score, unlike, near, felt
	}
	if pin == nil { // a drum part has a snare every other bar or a hat every bar; kicks alone are a bass's attacks, fewer hits a front end's doubts
		drums := len(hits[InstrSnare]) >= best.bars()/2 || len(hits[InstrHihat]) >= best.bars()
		best.m, bestTempo = name(best.m, bestTempo, k, drums, best.sixteenths(notes) > ripSixteenths)
	}
	return best, bestTempo, nil
}

// name is the meter and tempo a bar read as m at tempo over k pulses a beat is
// stored as, one name whichever pulse the tracker locked on: 6 or 8 beats halve
// the tempo, 2 beats or 3 eighths double it, tracked triplets at a fast tempo
// read the simple meter at two thirds, and a drumless bar whose notes use no
// sixteenth is in eighths at half the tempo (3/8, 5/8, 7/8; 6/8 as 3/8).
func name(m Meter, tempo float64, k int, drums, sixteenths bool) (Meter, float64) {
	eighths := !drums && !sixteenths && tempo/2 >= MinBPM
	switch n, d := m.norm().Num, m.norm().Den; {
	case d == 2 && tempo/2 >= MinBPM: // 3/2, 4/2
		return meterOf(n, 4), tempo / 2
	case n == 2 && d == 4 && tempo*2 <= MaxBPM:
		return Meter{}, tempo * 2
	case n == 3 && d == 8 && tempo*2 <= MaxBPM:
		return Meter{3, 4}, tempo * 2
	case k == 1 && d == 8 && n%3 == 0 && tempo >= 150:
		return meterOf(n/3, 4), tempo * 2 / 3
	case d == 4 && n%2 == 1 && eighths:
		return Meter{n, 8}, tempo / 2
	case d == 8 && n == 6 && eighths:
		return Meter{n / 2, 8}, tempo / 2
	}
	return m, tempo
}

// ripSixteenths is the share of a rip's notes that must start on a sixteenth
// of the beat for the beat to be a quarter and not an eighth; under it they
// are a front end's jitter
const ripSixteenths = 0.25

// sixteenths is the share of the notes starting on odd steps of the bars, the
// sixteenths of the beat, among those within a quarter step of a step once
// their median lag against the grid is taken out, as the pitch paths start
// late against the onsets the beats sit on
func (g *ripGrid) sixteenths(notes []ripNote) float64 {
	lag := g.lag(notes)
	odd, all := 0, 0
	for _, n := range notes {
		p, ok := g.phase(n.start)
		if !ok {
			continue
		}
		_, step, ok := g.at(n.start)
		if !ok {
			continue
		}
		pos := float64(step) + p - lag
		if math.Abs(pos-math.Round(pos)) > 0.25 {
			continue
		}
		all++
		if int(math.Round(pos))%2 != 0 {
			odd++
		}
	}
	return float64(odd) / float64(max(all, 1))
}

// nearest is how far each bar is from its nearest other bar, over the notes
// and hits quantized on the grid (BarDistance), averaged: music repeats at its
// own bar, so a bar cut at the right length finds a near twin where one cut
// across the bars does not.
func (g *ripGrid) nearest(notes []ripNote, hits [InstrClap][]ripHit, root int) float64 {
	r := &Rip{Root: root, Scale: ScaleMajor, Meter: g.m, Bars: make([]RipBar, g.bars())}
	g.drums(r, hits)
	for _, n := range notes {
		part := func(b *RipBar) *[]Step { return &b.Melody }
		if n.midi < RipBassTop {
			part = func(b *RipBar) *[]Step { return &b.Bass }
		}
		g.place(r, n, part)
	}
	if len(r.Bars) < 2 {
		return 1
	}
	var sum float64
	for i := range r.Bars {
		best := 1.0
		for j := range r.Bars {
			if j != i {
				best = math.Min(best, BarDistance(&r.Bars[i], &r.Bars[j]))
			}
		}
		sum += best
	}
	return sum / float64(len(r.Bars))
}

// repeats is how unlike itself the recording sounds a bar, two and four bars of
// a reading on, each sixteenth's chroma and onset against the one so many steps
// later: music repeats at its own bar and that bar's doubles, where another
// meter's bars fall out of step, so a loop of even beats still tells its bar.
func (f *RipFrames) repeats(rd ripReading) float64 {
	steps := split(rd.pulses, rd.unit)
	o := f.onsets()
	var loudest float64
	for _, v := range o {
		loudest = max(loudest, v)
	}
	at := make([][13]float64, max(len(steps)-1, 0)) // a sixteenth's unit chroma, and its onset against the loudest
	for i := range at {
		var c [12]float64
		for t := steps[i]; t < steps[i+1] && t < f.frames(); t++ {
			for pc := range c {
				c[pc] += f.Chroma[t][pc]
			}
		}
		var sq float64
		for _, v := range c {
			sq += v * v
		}
		for pc, v := range c {
			if sq > 0 {
				at[i][pc] = v / math.Sqrt(sq)
			}
		}
		if loudest > 0 {
			at[i][12] = peak(o, float64(steps[i])) / loudest
		}
	}
	var unlike float64
	for _, k := range ripRepeatBars {
		lag := k * rd.m.Steps()
		var dot, na, nb float64
		for i := 0; i+lag < len(at); i++ {
			for j := range at[i] {
				dot += at[i][j] * at[i+lag][j]
				na += at[i][j] * at[i][j]
				nb += at[i+lag][j] * at[i+lag][j]
			}
		}
		if na > 0 && nb > 0 {
			unlike += 1 - dot/math.Sqrt(na*nb)
		} else {
			unlike++
		}
	}
	return unlike / float64(len(ripRepeatBars))
}

// readings is the ways meter reads the tracked pulses, 4/4 first: k to a beat,
// 4/4, 3/4 and 6/8, each pulse 4/k steps; three to a beat, the beat a dotted
// quarter and a pulse an eighth, the same at three halves of bpm, or the
// pulses sixteenths at three quarters when that is out of range. Under
// ripMeterBars bars only 4/4 is read; a pin is read alone, its beat split.
func (f *RipFrames) readings(pulses []int, k int, bpm float64, pin *Meter) ([]ripReading, error) {
	if pin == nil && len(pulses) <= StepsPerBeat*ripMeterBars*k {
		return []ripReading{{Meter{}, pulses, StepsPerBeat / k, k, bpm}}, nil
	}
	unit, tempo := StepsPerBeat/k, bpm
	if k == 3 {
		if unit, tempo = 2, bpm*3/2; tempo > MaxBPM {
			unit, tempo = 1, bpm*3/4
		}
	}
	read := func(m Meter) ripReading { return ripReading{m, pulses, unit, k, tempo} }
	var readings []ripReading
	switch {
	case pin != nil:
		if err := pin.check(); err != nil {
			return nil, fmt.Errorf("rip: %w", err)
		}
		readings = []ripReading{read(*pin)}
	case k == 3:
		readings = []ripReading{read(Meter{}), read(Meter{3, 4}), read(Meter{6, 8})}
		if ripAllReadings {
			readings = append(readings, read(Meter{9, 8}), read(Meter{2, 4}), read(Meter{5, 4}), read(Meter{7, 4}), read(Meter{4, 2}))
		}
	default:
		readings = []ripReading{read(Meter{}), read(Meter{3, 4}), read(Meter{6, 8})}
		if ripAllReadings {
			readings = append(readings, read(Meter{2, 4}), read(Meter{5, 4}), read(Meter{3, 2}), read(Meter{7, 4}), read(Meter{4, 2}))
			if k%2 == 0 {
				readings = append(readings, read(Meter{3, 8}))
			}
		}
	}
	for i, rd := range readings {
		if b := rd.m.Beat(); b < rd.unit { // a pulse a beat at least
			rd.pulses, rd.stride, rd.unit = split(rd.pulses, rd.unit/b), rd.stride*rd.unit/b, b
			readings[i] = rd
		}
	}
	return readings, nil
}

// split cuts each span between beats into n
func split(beats []int, n int) []int {
	out := make([]int, 0, n*len(beats))
	for i, b := range beats {
		out = append(out, b)
		if i+1 < len(beats) {
			for k := 1; k < n; k++ {
				out = append(out, b+int(math.Round(float64(k*(beats[i+1]-b))/float64(n))))
			}
		}
	}
	return out
}

// peak is the strongest onset within a frame of t
func peak(o []float64, t float64) float64 {
	var v float64
	for k := max(int(math.Round(t))-1, 0); k <= int(math.Round(t))+1 && k < len(o); k++ {
		v = max(v, o[k])
	}
	return v
}

// ripLineCues are what a bar line is scored by, per frame: a kick or a snare
// within two frames, a bass note starting within two, and the bass register's
// and the whole chroma summed up to the frame, so a span's chroma is a
// difference of sums.
type ripLineCues struct {
	kick, snare, bass []float64
	bassChroma        [][12]float64
	chroma            [][12]float64
	root              int
	beat              []float64 // per pulse of the reading scored: how much more onset its class, a beat apart, holds than the pulses around it
}

// lineCues reads the per-frame terms of the bar lines
func (f *RipFrames) lineCues(hits [InstrClap][]ripHit, rootPC int, bass []ripNote) *ripLineCues {
	n := f.frames()
	c := &ripLineCues{kick: make([]float64, n), snare: make([]float64, n), bass: make([]float64, n), bassChroma: make([][12]float64, n+1), chroma: make([][12]float64, n+1)}
	mark := func(into []float64, frame int) {
		for t := max(frame-2, 0); t <= frame+2 && t < n; t++ {
			into[t] = 1
		}
	}
	for _, h := range hits[InstrKick] {
		mark(c.kick, h.frame)
	}
	for _, h := range hits[InstrSnare] {
		mark(c.snare, h.frame)
	}
	for _, note := range bass {
		mark(c.bass, note.start)
	}
	for t := range n {
		for pc := range c.chroma[t] {
			c.bassChroma[t+1][pc] = c.bassChroma[t][pc] + f.BassChroma[t][pc]
			c.chroma[t+1][pc] = c.chroma[t][pc] + f.Chroma[t][pc]
		}
	}
	c.root = rootPC
	return c
}

// ripBarSlack is the pulses a bar may hold beyond or short of a whole bar's,
// where the tracker added or lost one; ripBarMove what such a bar costs per
// pulse of difference; ripBarLine the weight of a line's cues beside its comb,
// ripBarBeat that of its pulse standing out as a beat and ripBarAccent that
// of the bass starting on it
const ripBarLine = 1.0

var (
	ripBarSlack  = 2
	ripBarMove   = 0.5
	ripBarBeat   = 1.0
	ripBarAccent = 0.5
)

// beats reads, per pulse, how much its class, every stride-th pulse from it,
// stands out in onset over the eight beats around it: the beat the tracker
// found against the pulses between.
func (c *ripLineCues) beats(o []float64, pulses []int, stride int) {
	c.beat = make([]float64, len(pulses))
	for i := range pulses {
		var class, all, classes, alls float64
		for j := max(i-8*stride, 0); j < min(i+8*stride+1, len(pulses)); j++ {
			v := peak(o, float64(pulses[j]))
			all, alls = all+v, alls+1
			if (j-i)%stride == 0 {
				class, classes = class+v, classes+1
			}
		}
		if all > 0 {
			c.beat[i] = class/classes/(all/alls) - 1
		}
	}
}

// bar scores a bar of meter m from frame a, pulse i, d frames long: its comb,
// the mean onset on its sixteenths; its line's cues, a kick and no snare on
// it, the snare on the felt backbeats, the root in the bass, the chroma change
// over span frames (four tracked beats for every reading), its pulse's beat
// class and the bass accent; and, apart, the onsets on its other felt beats.
func (c *ripLineCues) bar(o []float64, m Meter, i, a int, d, span float64) (comb, line, other float64) {
	n := len(o)
	steps := m.Steps()
	felt, _ := m.Felt()
	var bassOn float64
	for j := range steps {
		t := float64(a) + float64(j)*d/float64(steps)
		comb += peak(o, t)
		if j > 0 {
			bassOn += c.bass[min(int(math.Round(t)), n-1)]
		}
	}
	comb /= float64(steps)
	line = c.kick[a] - c.snare[a] + ripBarBeat*c.beat[i] + ripBarAccent*(c.bass[a]-bassOn/float64(steps-1))
	var back, backs, felts float64
	for j := 1; j < felt; j++ {
		t := float64(a) + float64(j)*d/float64(felt)
		felts += peak(o, t)
		if felt%2 == 1 || j%2 == 1 {
			back, backs = back+c.snare[min(int(math.Round(t)), n-1)], backs+1
		}
	}
	if backs > 0 {
		line += back / backs
	}
	if felt > 1 {
		felts /= float64(felt - 1)
	}
	sum := func(sums [][12]float64, from, to int) (out [12]float64) {
		from, to = max(from, 0), min(max(to, 0), n)
		for pc := range out {
			out[pc] = sums[to][pc] - sums[from][pc]
		}
		return out
	}
	w := int(math.Round(span))
	root := sum(c.bassChroma, a, a+int(math.Round(span/4)))
	normalize(&root)
	line += root[c.root]
	if a-w >= 0 {
		line += 1 - cosine(sum(c.chroma, a-w, a), sum(c.chroma, a, a+w))
	}
	return comb, ripBarLine * line, felts
}

// barPath decodes a reading's bars as a path over the pulses, each bar
// starting where the last ended with a whole bar's pulses or within
// ripBarSlack of them at ripBarMove a pulse, scored by bar for the time it
// covers; it returns each bar line's frame, the last the end of the last bar,
// and the mean score of a bar's time, its comb, its line and its felt beats.
func (f *RipFrames) barPath(rd ripReading, c *ripLineCues) (lines []int, score, comb, line, felt float64) {
	o := f.onsets()
	n := len(rd.pulses)
	per := rd.m.Steps() / rd.unit
	span := float64(rd.pulses[n-1]-rd.pulses[0]) / float64(n-1) * float64(4*rd.stride) // four tracked beats
	c.beats(o, rd.pulses, rd.stride)
	best := make([]float64, n) // of a path whose last bar ends on pulse i
	from := make([]int, n)
	for i := range best {
		best[i], from[i] = math.Inf(-1), -1
	}
	for i := range n {
		for l := max(per-ripBarSlack, 1); l <= per+ripBarSlack; l++ {
			j := i - l
			if j < 0 {
				continue
			}
			prev := best[j]
			if j <= per+ripBarSlack { // a first bar
				prev = max(prev, 0)
			}
			if math.IsInf(prev, -1) {
				continue
			}
			d := float64(rd.pulses[i] - rd.pulses[j])
			comb, line, _ := c.bar(o, rd.m, j, rd.pulses[j], d, span)
			v := prev + (comb+line-ripBarMove*math.Abs(float64(l-per)))*float64(l)/float64(per)
			if v > best[i] {
				best[i], from[i] = v, j
			}
		}
	}
	last, lastScore := -1, math.Inf(-1)
	for i := max(n-1-per-ripBarSlack, 0); i < n; i++ {
		if best[i] > lastScore {
			last, lastScore = i, best[i]
		}
	}
	if last < 0 {
		return []int{rd.pulses[0], rd.pulses[min(per, n-1)]}, 0, 0, 0, 0
	}
	var at []int
	for i := last; i >= 0; i = from[i] {
		at = append(at, i)
	}
	slices.Reverse(at)
	for k, i := range at {
		lines = append(lines, rd.pulses[i])
		if k > 0 {
			cb, ln, ft := c.bar(o, rd.m, at[k-1], rd.pulses[at[k-1]], float64(rd.pulses[i]-rd.pulses[at[k-1]]), span)
			comb, line, felt = comb+cb, line+ln, felt+ft
		}
	}
	bars := float64(len(at) - 1)
	return lines, lastScore / bars, comb / bars, line / bars, felt / bars
}

func cosine(a, b [12]float64) float64 {
	var dot, na, nb float64
	for i := range a {
		dot, na, nb = dot+a[i]*b[i], na+a[i]*a[i], nb+b[i]*b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// at is the bar and sixteenth a frame falls in, the bar's span cut in the
// meter's steps, false before the first bar or after the last.
func (g *ripGrid) at(frame int) (bar, step int, ok bool) {
	if frame < g.lines[0] {
		return 0, 0, false
	}
	bar = sort.SearchInts(g.lines, frame+1) - 1
	if bar >= g.bars() {
		return 0, 0, false
	}
	a, b := g.lines[bar], g.lines[bar+1]
	step = int(math.Round(float64(frame-a) / float64(b-a) * float64(g.m.Steps())))
	if step >= g.m.Steps() {
		bar, step = bar+1, 0
		if bar >= g.bars() {
			return 0, 0, false
		}
	}
	return bar, step, true
}

// stepFrames is the mean length of a sixteenth over the bars
func (g *ripGrid) stepFrames() float64 {
	return float64(g.lines[g.bars()]-g.lines[0]) / float64(g.m.Steps()*g.bars())
}

// musicStart is the frame the recording's sound first comes within 80 dB of its
// loudest: a model hears hits in the silence before that, and a soft opening can
// sit 40 dB under the piece's loudest.
func musicStart(x []float64) int {
	var loudest float64
	block := func(t int) float64 {
		var e float64
		for _, v := range x[t*RipHop : (t+1)*RipHop] {
			e += v * v
		}
		return e
	}
	n := len(x) / RipHop
	for t := range n {
		loudest = max(loudest, block(t))
	}
	for t := range n {
		if block(t) >= loudest*1e-8 {
			return t
		}
	}
	return 0
}

// heardFrom drops the hits and notes before the frame the music starts, less two
// frames' slack for a head that fires as the attack begins.
func heardFrom(t int, hits [InstrClap][]ripHit, bass, melody []ripNote) ([InstrClap][]ripHit, []ripNote, []ripNote) {
	t -= 2
	for d := range hits {
		hits[d] = slices.DeleteFunc(hits[d], func(h ripHit) bool { return h.frame < t })
	}
	early := func(n ripNote) bool { return n.start < t }
	return hits, slices.DeleteFunc(bass, early), slices.DeleteFunc(melody, early)
}

// pickup opens a bar before the first full one when a note or a hit sounds more
// than half a step before its line, so a phrase that leads into the first bar
// is kept, as long as the first, into the time before the recording when it
// begins inside that bar.
func (g ripGrid) pickup(notes []ripNote, hits [InstrClap][]ripHit) ripGrid {
	line := float64(g.lines[0]) - g.stepFrames()/2
	early := slices.ContainsFunc(notes, func(n ripNote) bool { return float64(n.start) < line })
	for _, hs := range hits {
		early = early || slices.ContainsFunc(hs, func(h ripHit) bool { return float64(h.frame) < line })
	}
	if !early {
		return g
	}
	g.lines = slices.Concat([]int{2*g.lines[0] - g.lines[1]}, g.lines)
	return g
}

// phase is how far a frame sits from its nearest step, in steps, in [-0.5, 0.5)
func (g *ripGrid) phase(frame int) (float64, bool) {
	if frame < g.lines[0] {
		return 0, false
	}
	bar := sort.SearchInts(g.lines, frame+1) - 1
	if bar >= g.bars() {
		return 0, false
	}
	a, b := g.lines[bar], g.lines[bar+1]
	pos := float64(frame-a) / float64(b-a) * float64(g.m.Steps())
	return pos - math.Round(pos), true
}

// lag is the median of the notes' phases, in steps: what the pitch paths start
// late by against the onsets the beats sit on, zero for too few notes to say
func (g *ripGrid) lag(notes []ripNote) float64 {
	var ph []float64
	for _, n := range notes {
		if p, ok := g.phase(n.start); ok {
			ph = append(ph, p)
		}
	}
	if len(ph) < 4 {
		return 0
	}
	slices.Sort(ph)
	return ph[len(ph)/2]
}

// frames of a bar: its line to the next bar's
func (g *ripGrid) span(bar int) (int, int) {
	return g.lines[bar], g.lines[bar+1]
}

// key sums the chroma over the recording and takes keyOf it
func (f *RipFrames) key() (int, ScaleID) {
	var c, b [12]float64
	for t := range f.frames() {
		for pc := range c {
			c[pc] += f.Chroma[t][pc]
			b[pc] += f.BassChroma[t][pc]
		}
	}
	return keyOf(c, b)
}

// keyOf is the root and scale whose tones hold the most of a chroma c and whose
// other tones the least, the tonic and fifth weighing extra and the bass
// register's chroma b voting for the tonic; the root lands in the octave the
// engine's harmony root lives in.
func keyOf(c, b [12]float64) (int, ScaleID) {
	normalize(&c)
	normalize(&b)
	root, scale, best := 0, ScalePhrygian, math.Inf(-1)
	for _, s := range []ScaleID{ScaleMajor, ScaleMinor, ScaleDorian, ScalePhrygian, ScaleHarmonicMinor, ScaleMinorPent} {
		for r := range 12 {
			var in [12]bool
			for _, iv := range scaleTable[s] {
				in[(r+iv)%12] = true
			}
			var score float64
			for pc := range 12 {
				if in[pc] {
					score += c[pc]
				} else {
					score -= c[pc]
				}
			}
			score += c[r] + 0.5*c[(r+7)%12] + b[r]
			if score > best {
				root, scale, best = r, s, score
			}
		}
	}
	return root + 3*12, scale
}

func normalize(c *[12]float64) {
	var sum float64
	for _, v := range c {
		sum += v
	}
	if sum > 0 {
		for i := range c {
			c[i] /= sum
		}
	}
}

// progression names each bar's chord by chordOf the bar's chroma
func (f *RipFrames) progression(g ripGrid, root int, scale ScaleID) []int {
	prog := make([]int, g.bars())
	for bar := range prog {
		var c [12]float64
		start, end := g.span(bar)
		for t := max(start, 0); t < end && t < f.frames(); t++ {
			for pc := range c {
				c[pc] += f.Chroma[t][pc]
			}
		}
		prog[bar] = chordOf(c, root, scale)
	}
	return prog
}

// chordOf is the degree whose triad a chroma favours
func chordOf(c [12]float64, root int, scale ScaleID) int {
	iv := scaleTable[scale]
	deg, best := 0, math.Inf(-1)
	for d := range iv {
		pc := func(k int) int { return (root + iv[(d+k)%len(iv)]) % 12 }
		if s := c[pc(0)] + 0.6*c[pc(2)] + 0.8*c[pc(4)]; s > best {
			deg, best = d, s
		}
	}
	return deg
}

// ripHit is one onset of a band's strength
type ripHit struct {
	frame    int
	strength float64
}

// pickHits takes the local peaks of v that rise OnsetFloor over its median of the
// surrounding second and OnsetMin of its loudest, the stronger of two within gap
// frames.
func pickHits(v []float64, gap int, p RipParams, hop, peak float64) []ripHit {
	n := len(v)
	half := int(0.5 / hop)
	m := newSlidingMedian(2*half + 1)
	med := make([]float64, n)
	for t := 0; t < n+half; t++ {
		x := 0.0
		if t < n {
			x, peak = v[t], max(peak, v[t])
		}
		mv := m.push(x)
		if c := t - half; c >= 0 && c < n {
			med[c] = mv
		}
	}
	var hits []ripHit
	for t := range n {
		if v[t] <= p.OnsetFloor*med[t]+p.OnsetMin*peak {
			continue
		}
		top := true
		for d := -2; d <= 2 && top; d++ {
			if u := t + d; u >= 0 && u < n && (v[u] > v[t] || (v[u] == v[t] && u < t)) {
				top = false
			}
		}
		if !top {
			continue
		}
		if k := len(hits) - 1; k >= 0 && t-hits[k].frame < gap {
			if v[t] > hits[k].strength {
				hits[k] = ripHit{t, v[t]}
			}
			continue
		}
		hits = append(hits, ripHit{t, v[t]})
	}
	return hits
}

// drumHits picks each drum's onsets off its strength. A front end's strength is how
// sure it is, so a drum it never grows sure of stays silent where the drum's loudest
// would raise its doubts to hits, and one striking under ripDrumsPerMinute is no part.
func (f *RipFrames) drumHits(p RipParams) [InstrClap][]ripHit {
	gap := max(int(p.OnsetGap/f.Hop), 1)
	var out [InstrClap][]ripHit
	for d := range f.Onset {
		if f.Strike == nil {
			out[d] = pickHits(f.Onset[d], gap, p, f.Hop, 0)
			continue
		}
		hits := pickHits(f.Onset[d], gap, p, f.Hop, 1)
		if float64(len(hits)) >= ripDrumsPerMinute*float64(len(f.Onset[d]))*f.Hop/60 {
			out[d] = hits
		}
	}
	return out
}

// drums quantizes the hits to the grid, a hit's velocity its strength against
// the drum's loudest
func (g *ripGrid) drums(r *Rip, hits [InstrClap][]ripHit) {
	for d := range hits {
		var loudest float64
		for _, h := range hits[d] {
			loudest = max(loudest, h.strength)
		}
		for _, h := range hits[d] {
			g.hit(r, InstrumentType(d), h.frame, 0.4+0.6*h.strength/loudest)
		}
	}
}

// hit quantizes a drum's hit to the grid, the louder of two on one step kept
func (g *ripGrid) hit(r *Rip, d InstrumentType, frame int, vel float64) {
	bar, step, ok := g.at(frame)
	if !ok {
		return
	}
	evs := &r.Bars[bar].Drums[d]
	if i := slices.IndexFunc(*evs, func(s Step) bool { return s.Pos == step }); i >= 0 {
		(*evs)[i].Vel = max((*evs)[i].Vel, vel)
		return
	}
	*evs = append(*evs, Step{Pos: step, Vel: vel})
}

// ripNote is a stretch of frames the pitch track held one pitch over, with the
// voice genes the front end heard it played with when it hears those
type ripNote struct {
	start, end, midi int
	vel              float64
	strength         float64 // mean salience over the note, against the register's reference loudness
	second           bool    // heard under another note of the register
	bright           float64 // RipNote.Bright
	voice            []float64
}

// notes tracks a register's lines: the bass as one path, the melody as two
// decoded together where SecondVoice is set, the second's notes kept where they
// reach SecondVoice of the register's mean strength and last twice MinNote, which
// drops a decaying partial's. The melody comes back as one list, which streams
// parts into its lines once settle has given the bass what both registers heard.
func (f *RipFrames) notes(lo, hi int, p RipParams) []ripNote {
	if f.Strike != nil && lo >= RipMelodyLow {
		return f.strikes(lo, hi, p)
	}
	if p.SecondVoice <= 0 || lo < RipMelodyLow {
		return f.path(lo, hi, p)
	}
	first, second := f.pair(lo, hi, p)
	all := slices.Concat(first, second)
	if len(all) == 0 {
		return nil
	}
	var strength float64
	for _, n := range all {
		strength += n.strength
	}
	strength /= float64(len(all))
	short := max(int(2*p.MinNote/f.Hop), 1)
	kept := slices.Clone(first)
	for _, n := range second {
		if n.strength >= p.SecondVoice*strength && n.end-n.start >= short {
			kept = append(kept, n)
		}
	}
	return kept
}

// strikes reads a register's notes from a front end that hears each pitch struck:
// a note starts where a strike peaks over Strike, as hard as the front end heard,
// and lasts, any number at once, until its salience falls under Voicing of the
// reference, its release peaks over Release or a sure strike renews it. None starts
// where the pitch never sounds, at a stronger strike's partial, or within StrikeGap.
func (f *RipFrames) strikes(lo, hi int, p RipParams) []ripNote {
	r, ok := f.register(lo, hi)
	if !ok {
		return nil
	}
	T, short, gap := f.frames(), max(int(p.MinNote/f.Hop), 1), int(p.StrikeGap/f.Hop)
	at := func(h []float32, t, midi int) float32 {
		if h == nil || t < 0 || t >= T || midi < RipMinMIDI || midi >= RipMaxMIDI {
			return 0
		}
		return h[t*RipPitches+midi-RipMinMIDI]
	}
	// peak: a head's score for midi peaks at t, at least over
	peak := func(h []float32, t, midi int, over float64) bool {
		v := at(h, t, midi)
		return h != nil && float64(v) >= over && v >= at(h, t-1, midi) && v > at(h, t+1, midi)
	}
	struck := func(t, s int) bool { return peak(f.Strike, t, lo+s, p.Strike) }
	// stronger: a stronger strike within two frames at one of the intervals ds from midi, at or over from
	stronger := func(t, midi, from int, ds ...int) bool {
		for _, d := range ds {
			for u := t - 2; u <= t+2 && midi+d >= from; u++ {
				if at(f.Strike, u, midi+d) > at(f.Strike, t, midi) {
					return true
				}
			}
		}
		return false
	}
	// another note's attack sets off strikes on the pitches ringing, a tone either side as sure as its own
	renews := func(t, s int) bool {
		return struck(t, s) && float64(at(f.Strike, t, lo+s)) >= p.StrikeAgain && !stronger(t, lo+s, RipMinMIDI, -2, -1, 1, 2)
	}
	sounds := func(t, s int) bool {
		for u := t; u < min(t+short, T); u++ {
			if r.sal(u, s) >= p.StrikeHold*r.ref {
				return true
			}
		}
		return false
	}
	var notes []ripNote
	for s := range r.reach {
		last := -gap
		for t := 0; t < T; {
			// partials of the register's own strikes only, as a bass note often doubles a melody note
			if !struck(t, s) || t-last < gap || stronger(t, lo+s, lo, -12, -19, -24) || !sounds(t, s) {
				t++
				continue
			}
			last = t
			end := t + 1
			for end < T && !(renews(end, s) && (end-t < short || end-t >= gap)) &&
				(end < t+short || r.sal(end, s) >= p.Voicing*r.ref && !peak(f.Release, end, lo+s, p.Release)) {
				end++
			}
			if end-t >= short {
				n := r.note(t, end, s)
				if f.Velocity != nil {
					n.vel = min(max(float64(at(f.Velocity, t, lo+s)), 0.1), 1)
				}
				notes = append(notes, n)
			}
			t = end
			// let go under a pedal it rings on, and only a sure strike starts it again
			for t < T && r.sal(t, s) >= p.Voicing*r.ref && !renews(t, s) {
				t++
			}
		}
	}
	slices.SortFunc(notes, func(a, b ripNote) int { return cmp.Or(a.start-b.start, a.midi-b.midi) })
	return notes
}

// lines reads the registers. A front end that hears each pitch struck reads both by
// strikes, any number at once, the bass line's own notes kept only where no strike
// sounds, as a plain bass's are struck softly, and long and strong enough to be sure.
// A low note with little sound at its own pitch is a higher note's shadow, and goes.
func (f *RipFrames) lines(x []float64, p RipParams) (bass, melody []ripNote) {
	line := f.notes(RipMinMIDI, RipBassReach, p)
	if f.Strike == nil {
		return line, f.notes(RipMelodyLow, RipMaxMIDI, p)
	}
	melody, bass = f.strikes(RipBassTop, RipMaxMIDI, p), f.strikes(RipMinMIDI, RipBassTop, p)
	struck := slices.Clone(bass)
	for _, n := range line {
		if n.midi >= RipBassTop || float64(n.end-n.start)*f.Hop < ripLineNote || n.strength < ripLineStrength {
			continue
		}
		over := 0
		for _, o := range struck {
			over += max(min(o.end, n.end)-max(o.start, n.start), 0)
		}
		if 4*over < n.end-n.start {
			bass = append(bass, n)
		}
	}
	bass = slices.DeleteFunc(bass, func(n ripNote) bool { return ownPitch(x, n, f.Hop) < ripOwnPitch })
	slices.SortFunc(bass, func(a, b ripNote) int { return cmp.Or(a.start-b.start, a.midi-b.midi) })
	return bass, melody
}

// ownPitch is how far a note's level at its own pitch stands over the stronger of
// its next two partials, in dB, over the 160 ms after its attack; x at RipRate.
func ownPitch(x []float64, n ripNote, hop float64) float64 {
	const win = RipRate * 16 / 100
	at := int((float64(n.start)*hop + 0.03) * RipRate)
	if at+win > len(x) {
		return 0
	}
	level := func(hz float64) float64 {
		var re, im float64
		for i := range win {
			w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/win)
			ph := 2 * math.Pi * hz * float64(i) / RipRate
			re += w * x[at+i] * math.Cos(ph)
			im += w * x[at+i] * math.Sin(ph)
		}
		return 10 * math.Log10(re*re+im*im+1e-12)
	}
	f0 := 440 * math.Pow(2, float64(n.midi-69)/12)
	return level(f0) - max(level(2*f0), level(3*f0))
}

// settle gives a note both registers heard, where the bass's reach and the
// melody's rows overlap, to the line it continues: the one whose nearest other
// note in time is nearer in pitch, the bass on a tie. A reached note more than an
// octave from every other bass note is not the bass's at all. So a bass line that
// climbs keeps its notes, and a melody that dips keeps its own.
func settle(bass, melody []ripNote) (b, m []ripNote) {
	nearest := func(notes []ripNote, n ripNote) float64 { // semitones to the nearest other note within a second
		best := math.Inf(1)
		for _, o := range notes {
			if o.start == n.start && o.midi == n.midi {
				continue
			}
			if gap := max(o.start-n.end, n.start-o.end); gap < 200 { // frames, about a second
				best = math.Min(best, math.Abs(float64(o.midi-n.midi)))
			}
		}
		return best
	}
	bass = slices.DeleteFunc(slices.Clone(bass), func(n ripNote) bool {
		if n.midi < RipBassTop {
			return false
		}
		near := nearest(bass, n)
		return !math.IsInf(near, 1) && near > 12
	})
	dropB, dropM := map[int]bool{}, map[int]bool{}
	for i, n := range melody {
		for j, bn := range bass {
			if bn.midi != n.midi || 2*(min(bn.end, n.end)-max(bn.start, n.start)) <= min(n.end-n.start, bn.end-bn.start) {
				continue
			}
			if nearest(bass, bn) <= nearest(melody, n) {
				dropM[i] = true
			} else {
				dropB[j] = true
			}
		}
	}
	for i, n := range bass {
		if !dropB[i] {
			b = append(b, n)
		}
	}
	for i, n := range melody {
		if !dropM[i] {
			m = append(m, n)
		}
	}
	return b, m
}

// register is a register of the salience as a decoder reads it: its states,
// the pitches from lo then the unvoiced one, and the reference loudness its own
// rows' ninetieth-percentile peak sets. Rows it reaches into, the next register's
// on another scale, sound at reachShare of their strength, so a line takes one
// only where it is strong and continues.
type register struct {
	f          *RipFrames
	lo, states int
	reach      int // the first state in the next register's rows, states when none
	ref        float64
}

// reachShare is what a reached row's salience counts for against the register's own
const reachShare = 0.75

func (f *RipFrames) register(lo, hi int) (register, bool) {
	r := register{f: f, lo: lo, states: hi - lo, reach: hi - lo}
	if lo < RipBassTop && hi > RipBassTop {
		r.reach = RipBassTop - lo
	}
	peaks := make([]float64, f.frames())
	for t := range peaks {
		for s := range r.reach {
			peaks[t] = max(peaks[t], r.sal(t, s))
		}
	}
	slices.Sort(peaks)
	r.ref = peaks[len(peaks)*9/10]
	return r, r.ref > 0
}

func (r register) sal(t, s int) float64 {
	return float64(r.f.Salience[t*RipPitches+r.lo+s-RipMinMIDI])
}

// emit is a state's score at a frame: the salience against the reference, one
// at most, a reached row's at reachShare, or Voicing for the unvoiced state
func (r register) emit(t, s int, p RipParams) float64 {
	if s == r.states {
		return p.Voicing
	}
	if s >= r.reach {
		return reachShare * min(r.sal(t, s)/r.ref, 1)
	}
	return min(r.sal(t, s)/r.ref, 1)
}

// relax is one line's step of a decoder: out[s] is the best in[q] less the cost
// of moving from q to s, Jump a semitone between pitches, Switch between a
// pitch and the unvoiced state, nothing to stay unvoiced; arg[s] is the q. The
// moves between pitches are swept each way, so a step is linear in the states.
func (r register) relax(in, out []float64, arg []uint8, p RipParams) {
	n := r.states
	best, from := math.Inf(-1), 0
	for s := 0; s < n; s++ {
		if best -= p.Jump; in[s] > best {
			best, from = in[s], s
		}
		out[s], arg[s] = best, uint8(from)
	}
	best, from = math.Inf(-1), 0
	for s := n - 1; s >= 0; s-- {
		if best -= p.Jump; in[s] > best {
			best, from = in[s], s
		}
		if best > out[s] {
			out[s], arg[s] = best, uint8(from)
		}
	}
	top, at := in[n], n
	for s := 0; s < n; s++ {
		if v := in[n] - p.Switch; v > out[s] {
			out[s], arg[s] = v, uint8(n)
		}
		if v := in[s] - p.Switch; v > top {
			top, at = v, s
		}
	}
	out[n], arg[n] = top, uint8(at)
}

// path is one line through a register: a hidden path through the salience
// scored against the costs of moving and of starting or ending a note, cut
// into notes
func (f *RipFrames) path(lo, hi int, p RipParams) []ripNote {
	r, ok := f.register(lo, hi)
	if !ok {
		return nil
	}
	n := r.states + 1
	prev, cur := make([]float64, n), make([]float64, n)
	back := make([][]uint8, f.frames())
	for s := range prev {
		prev[s] = r.emit(0, s, p)
	}
	for t := 1; t < f.frames(); t++ {
		back[t] = make([]uint8, n)
		r.relax(prev, cur, back[t], p)
		for s := range cur {
			cur[s] += r.emit(t, s, p)
		}
		prev, cur = cur, prev
	}
	track := make([]int, f.frames())
	s := 0
	for q := range prev {
		if prev[q] > prev[s] {
			s = q
		}
	}
	for t := f.frames() - 1; t >= 0; t-- {
		track[t] = s
		if t > 0 {
			s = int(back[t][s])
		}
	}
	return r.cut(track, p)
}

// pair decodes a register as two lines, a hidden path over pairs of states, each
// line paying its own moves, starts and ends: no pair a unison, a semitone or a
// harmonic apart, and A paying Cross a frame to be the quieter, so A is the line
// the ear follows through a crossing or a rest. A long recording keeps scores at
// checkpoints and backpointers a block at a time, so it costs little memory.
func (f *RipFrames) pair(lo, hi int, p RipParams) (a, b []ripNote) {
	r, ok := f.register(lo, hi)
	if !ok {
		return nil, nil
	}
	S, T := r.states+1, f.frames()
	// apart says two states may sound together: the unvoiced state with any,
	// two pitches when neither a unison, a semitone nor a harmonic interval apart
	apart := make([]bool, S*S)
	for a := range S {
		for b := range S {
			switch max(a, b) - min(a, b) {
			case 0, 1, 11, 12, 13, 18, 19, 20, 23, 24, 25, 27, 28, 29, 30, 31, 32:
				apart[a*S+b] = a == r.states || b == r.states
			default:
				apart[a*S+b] = true
			}
		}
	}
	e := make([]float64, S) // a frame's emissions, one a state
	emit := func(t int) {
		for s := range S {
			e[s] = r.emit(t, s, p)
		}
	}
	score := func(a, b int) float64 {
		if !apart[a*S+b] {
			return math.Inf(-1)
		}
		if e[a] < e[b] {
			return e[a] + e[b] - p.Cross
		}
		return e[a] + e[b]
	}
	mid, col, out := make([]float64, S*S), make([]float64, S), make([]float64, S)
	argB, argA := make([]uint8, S*S), make([]uint8, S)
	// step scores a frame's pairs from the last frame's: along B for each A,
	// then along A for each B, a pair's move costing the two lines' moves
	step := func(t int, prev, cur []float64, back []uint16) {
		emit(t)
		for qa := range S {
			r.relax(prev[qa*S:qa*S+S], mid[qa*S:qa*S+S], argB[qa*S:qa*S+S], p)
		}
		for b := range S {
			for qa := range S {
				col[qa] = mid[qa*S+b]
			}
			r.relax(col, out, argA, p)
			for a := range S {
				cur[a*S+b] = out[a] + score(a, b)
				if back != nil {
					qa := int(argA[a])
					back[a*S+b] = uint16(qa*S + int(argB[qa*S+b]))
				}
			}
		}
	}
	const block = 256
	prev, cur := make([]float64, S*S), make([]float64, S*S)
	emit(0)
	for a := range S {
		for b := range S {
			prev[a*S+b] = score(a, b)
		}
	}
	ta, tb := make([]int, T), make([]int, T)
	walk := func(state int, t0, t int, backs [][]uint16) int {
		for u := t; u > t0; u-- {
			ta[u], tb[u] = state/S, state%S
			state = int(backs[u-t0-1][state])
		}
		return state
	}
	best := func() int {
		state := 0
		for s := range prev {
			if prev[s] > prev[state] {
				state = s
			}
		}
		return state
	}
	if T <= 16*block {
		backs := make([][]uint16, T)
		for t := 1; t < T; t++ {
			backs[t-1] = make([]uint16, S*S)
			step(t, prev, cur, backs[t-1])
			prev, cur = cur, prev
		}
		state := walk(best(), 0, T-1, backs)
		ta[0], tb[0] = state/S, state%S
		return r.cut(ta, p), r.cut(tb, p)
	}
	var marks [][]float64 // the scores every block frames
	for t := 1; t < T; t++ {
		if (t-1)%block == 0 {
			marks = append(marks, slices.Clone(prev))
		}
		step(t, prev, cur, nil)
		prev, cur = cur, prev
	}
	state := best()
	backs := make([][]uint16, block)
	for i := range backs {
		backs[i] = make([]uint16, S*S)
	}
	for t := T - 1; t >= 1; {
		t0 := (t - 1) / block * block
		copy(prev, marks[t0/block])
		for u := t0 + 1; u <= t; u++ {
			step(u, prev, cur, backs[u-t0-1])
			prev, cur = cur, prev
		}
		state = walk(state, t0, t, backs)
		t = t0
	}
	ta[0], tb[0] = state/S, state%S
	return r.cut(ta, p), r.cut(tb, p)
}

// cut reads a line's track, a state a frame, into notes: a run of one pitch is
// a note, two when it dips under Retrigger of its peak and rises again, and none
// when shorter than MinNote. A rise without such a dip is no new note, since
// strings that beat rise and fall within one; a repeat is Retrigger's to find.
func (r register) cut(track []int, p RipParams) []ripNote {
	var notes []ripNote
	minFrames := max(int(p.MinNote/r.f.Hop), 1)
	flush := func(n ripNote) {
		if n.end-n.start >= minFrames {
			notes = append(notes, r.note(n.start, n.end, n.midi-r.lo))
		}
	}
	open := ripNote{start: -1}
	var peak float64
	dipped := false
	for t, pitch := range track {
		if open.start >= 0 && pitch == open.midi-r.lo {
			v := r.sal(t, pitch)
			if v < peak*p.Retrigger {
				dipped = true
			} else if dipped && v > 0.7*peak {
				flush(ripNote{start: open.start, end: t, midi: open.midi})
				open, peak, dipped = ripNote{start: t, midi: open.midi}, 0, false
			}
			peak = max(peak, v)
			continue
		}
		if open.start >= 0 {
			flush(ripNote{start: open.start, end: t, midi: open.midi})
			open = ripNote{start: -1}
		}
		if pitch < r.states {
			open, peak, dipped = ripNote{start: t, midi: r.lo + pitch}, r.sal(t, pitch), false
		}
	}
	if open.start >= 0 {
		flush(ripNote{start: open.start, end: len(track), midi: open.midi})
	}
	return notes
}

// note is the register's note on row s over frames start to end: its strength
// the mean salience against the register's reference, a velocity from it, and
// the voice a front end heard there
func (r register) note(start, end, s int) ripNote {
	var sum float64
	for t := start; t < end; t++ {
		sum += r.sal(t, s)
	}
	n := ripNote{start: start, end: end, midi: r.lo + s, strength: sum / float64(end-start) / r.ref}
	n.vel = min(max(0.3, n.strength), 1)
	if v := r.f.Velocity; v != nil {
		if i := start*RipPitches + r.lo + s - RipMinMIDI; i >= 0 && i < len(v) {
			n.vel = min(max(float64(v[i]), 0.1), 1)
		}
	}
	n.voice = r.f.voiceOver(start, end)
	return n
}

// voiceOver is the mean of the voice genes a front end heard over frames a to b;
// nil without a voice head.
func (f *RipFrames) voiceOver(a, b int) []float64 {
	if f.Voice == nil {
		return nil
	}
	genes := make([]float64, VoiceGeneCount)
	for t := a; t < b; t++ {
		for i := range genes {
			genes[i] += float64(f.Voice[t*VoiceGeneCount+i])
		}
	}
	for i := range genes {
		genes[i] /= float64(max(b-a, 1))
	}
	return genes
}

// place quantizes a note onto the grid as a step of the key's nearest degree
// with an accidental for the semitones left, its length in sixteenths, the
// louder of two on one step kept.
func (g *ripGrid) place(r *Rip, n ripNote, part func(*RipBar) *[]Step) {
	bar, step, ok := g.at(n.start)
	if !ok {
		return
	}
	deg, oct, acc := PitchStep(r.Root, r.Scale, n.midi)
	dur := min(max(int(math.Round(float64(n.end-n.start)/g.stepFrames())), 1), MaxPatternLen)
	s := Step{Pos: step, Vel: n.vel, Deg: deg, Oct: oct, Acc: acc, Dur: dur}
	evs := part(&r.Bars[bar])
	if i := slices.IndexFunc(*evs, func(e Step) bool { return e.Pos == step }); i >= 0 {
		if (*evs)[i].Vel < s.Vel {
			(*evs)[i] = s
		}
		return
	}
	*evs = append(*evs, s)
	slices.SortFunc(*evs, func(a, b Step) int { return a.Pos - b.Pos })
}

// RipEvent is one step of patterns placed in the slots as they would sound: a
// drum's hit, or a note's pitch, length and instrument.
type RipEvent struct {
	Bar, Step, Dur int
	Instr          InstrumentType
	MIDI           int // 0 for a drum
	Vel            float64
}

// ExpectedEvents is what patterns placed in the slots and played for bars bars of
// meter m under the harmony given would sound, step by step, a pattern shorter
// than a bar as often as it plays, chance steps left out since they may not. It
// is the truth a rendered loop is labelled with.
func ExpectedEvents(slots [MusicSlots]PatternID, m Meter, bars, root int, scale ScaleID, prog []int) []RipEvent {
	h := newHarmony()
	h.set(root, scale, prog)
	var out []RipEvent
	for bar := range bars {
		if bar > 0 {
			h.advanceBar()
		}
		for _, id := range slots {
			p := GetPattern(id)
			if p == nil || p.Steps <= 0 {
				continue
			}
			for _, tr := range p.Tracks {
				for _, ev := range tr.Events {
					if ev.Prob > 0 && ev.Prob < 1 || ev.Vel <= 0 {
						continue
					}
					for step := ((ev.Pos-bar*m.Steps())%p.Steps + p.Steps) % p.Steps; step < m.Steps(); step += p.Steps {
						e := RipEvent{Bar: bar, Step: step, Dur: max(ev.Dur, 1), Instr: tr.Instr, Vel: ev.Vel}
						if !tr.Instr.IsDrum() {
							e.MIDI = h.resolve(ev.Deg, ev.Oct, ev.Acc, tr.FollowChord)
						}
						out = append(out, e)
					}
				}
			}
		}
	}
	return out
}

// ExpectedRip is the rip a perfect listener would make of patterns placed in the
// slots and played for bars bars of meter m under the harmony given: each bar's
// drum hits by instrument and its tonal notes by pitch, a note under E3 a bass
// note, as the rip reports them. It is what CompareRips grades a rip of
// RenderMusic's output against.
func ExpectedRip(slots [MusicSlots]PatternID, bpm int, m Meter, bars, root int, scale ScaleID, prog []int) *Rip {
	r := &Rip{BPM: bpm, Meter: m, Root: root, Scale: scale, Bars: make([]RipBar, bars)}
	for _, e := range ExpectedEvents(slots, m, bars, root, scale, prog) {
		b := &r.Bars[e.Bar]
		if e.Instr.IsDrum() {
			if e.Instr < InstrClap {
				b.Drums[e.Instr] = append(b.Drums[e.Instr], Step{Pos: e.Step, Vel: e.Vel})
			}
			continue
		}
		deg, oct, acc := PitchStep(root, scale, e.MIDI)
		s := Step{Pos: e.Step, Vel: e.Vel, Deg: deg, Oct: oct, Acc: acc, Dur: e.Dur}
		if e.MIDI < RipBassTop {
			b.Bass = append(b.Bass, s)
		} else {
			b.Melody = append(b.Melody, s)
		}
	}
	return r
}

// RipScore grades a rip against what was played, each part in [0, 1]: the tempo
// within two BPM, the bars found, and the drums, melody and bass as the harmonic
// mean of recall and precision of their steps, a note's pitch included. Total
// weighs the drums and the melody double.
type RipScore struct {
	Tempo, Bars, Drums, Melody, Bass, Total float64
}

// CompareRips grades got against want, the bars aligned by got's downbeat, so a
// rip that missed the first bar is graded on the rest, and one that opened a
// pickup bar before the recording on the bars after it. Steps count from each
// rip's first bar in its own meter, so a rip in another meter is graded on its
// notes where they sound and on Bars by the bar lines it shares.
func CompareRips(got, want *Rip) RipScore {
	var s RipScore
	if d := got.BPM - want.BPM; d >= -2 && d <= 2 {
		s.Tempo = 1
	}
	gs, ws := got.Meter.Steps(), want.Meter.Steps()
	offset := 0 // got's first bar line, in steps from want's
	if got.BPM > 0 {
		offset = gs * int(math.Round(got.Downbeat/(float64(gs)*60/StepsPerBeat/float64(got.BPM))))
	}
	lo, hi := max(offset, 0), min(offset+len(got.Bars)*gs, len(want.Bars)*ws)
	if len(want.Bars) > 0 {
		shared := 0
		for line := (lo + ws - 1) / ws * ws; line < hi; line += ws {
			if (line-offset)%gs == 0 {
				shared++
			}
		}
		s.Bars = float64(shared) / float64(len(want.Bars))
	}
	key := func(r *Rip, at int, st Step, pitched bool) int {
		if !pitched {
			return at
		}
		return at<<8 | StepPitch(r.Root, r.Scale, st.Deg, st.Oct, st.Acc)
	}
	keys := func(r *Rip, first, bar int, pick func(*RipBar) []Step, pitched bool) map[int]bool {
		out := map[int]bool{}
		for i := range r.Bars {
			for _, st := range pick(&r.Bars[i]) {
				if at := first + i*bar + st.Pos; at >= lo && at < hi {
					out[key(r, at, st, pitched)] = true
				}
			}
		}
		return out
	}
	f := func(pick func(*RipBar) []Step, pitched bool) float64 {
		var tp, fp, fn int
		have, wantKeys := keys(got, offset, gs, pick, pitched), keys(want, 0, ws, pick, pitched)
		for k := range wantKeys {
			if have[k] {
				tp++
			} else {
				fn++
			}
		}
		for k := range have {
			if !wantKeys[k] {
				fp++
			}
		}
		if tp+fp+fn == 0 {
			return 1 // nothing played, nothing heard
		}
		return 2 * float64(tp) / float64(2*tp+fp+fn)
	}
	var drums float64
	for d := range InstrClap {
		drums += f(func(b *RipBar) []Step { return b.Drums[d] }, false)
	}
	s.Drums = drums / float64(InstrClap)
	s.Melody = f(func(b *RipBar) []Step { return append(slices.Clone(b.Melody), b.Under...) }, true)
	s.Bass = f(func(b *RipBar) []Step { return b.Bass }, true)
	s.Total = (s.Tempo + s.Bars + 2*s.Drums + 2*s.Melody + s.Bass) / 7
	return s
}
