package audio

import (
	"fmt"
	"time"
)

// Engine constants: every value the mixer, sequencer and synthesis path read.
// An embedder's policy (what moves the tiers, its per-sound levels) stays with it.

// --- Hardware / stream format ---
const (
	AudioSampleRate    = 44100
	AudioChannels      = 2
	AudioBitDepth      = 16
	AudioBytesPerFrame = AudioChannels * (AudioBitDepth / 8) // 4

	// AudioBufferDuration is the default mixer period, which sets output latency.
	// AudioConfig.Buffer chooses another inside AudioBufferMin..AudioBufferMax in
	// AudioBufferStep, the shortest period that is a whole number of frames (441).
	AudioBufferDuration = 50 * time.Millisecond
	AudioBufferMin      = 10 * time.Millisecond
	AudioBufferMax      = 200 * time.Millisecond
	AudioBufferStep     = 10 * time.Millisecond

	// AudioProbeWindow is the backend survival window after the probe write
	AudioProbeWindow = 60 * time.Millisecond

	// AudioPauseFade is the per-sample pause gain ramp length
	AudioPauseFade = 250 * time.Millisecond

	// stderrTailMax bounds the retained backend stderr tail
	stderrTailMax = 2048
)

// --- Mixer bus policy ---
const (
	MusicDuckAmount  = 0.7 // music gain under sounding sfx
	MusicDuckAttack  = 10 * time.Millisecond
	MusicDuckRelease = 100 * time.Millisecond

	// The output limiter holds the mix under LimiterCeiling: it sees each peak
	// LimiterLookahead samples before it sounds and ramps its gain down over that
	// time, so stacked kicks duck without the step a gain change inside one sample
	// puts on them, and recovers over LimiterRelease
	LimiterCeiling   = 0.9
	LimiterLookahead = 64
	LimiterRelease   = 80 * time.Millisecond
)

// --- Sfx policy ---
const (
	SoundVariants  = 3  // pre-rendered variants per sound a spec does not count
	MaxSfxPerSound = 2  // concurrent sfx per SoundID (most-progressed stolen)
	MaxActiveSfx   = 12 // global concurrent sfx cap

	// Rapid-fire dampening: repeats inside the cooldown attenuate, then
	// recover proportionally over RapidFireRecovery
	RapidFireCooldown  = 250 * time.Millisecond
	RapidFireDecay     = 0.65
	RapidFireMinVolume = 0.25
	RapidFireRecovery  = 600 * time.Millisecond
)

// --- Sequencer grid ---
const (
	DefaultBPM   = 140 // psytrance range 135-150
	MinBPM       = 80
	MaxBPM       = 200
	StepsPerBeat = 4 // 16th notes
	BeatsPerBar  = 4 // 4/4
	StepsPerBar  = StepsPerBeat * BeatsPerBar

	MaxPatternLen     = 64 // max steps per pattern
	MusicSlots        = 4  // rhythm, melody, free layer, bass
	MaxPolyphony      = 16 // simultaneous tonal voices per slot
	MaxPatternNameLen = 64
	MaxPatternTracks  = 32  // SetMask is a uint32; reveal shifts 1<<n
	MaxPatternEvents  = 512 // per track
	MaxStepDegree     = 64
	MaxStepAccidental = 2   // semitones an accidental may move a degree
	MaxTuningCents    = 100 // the engine's tuning off A440, a semitone either way
	MinStepOctave     = -2
	MaxStepOctave     = 8

	DefaultSwing = 0.0 // straight
	MaxSwing     = 0.5 // max shuffle

	// DefaultRootNote is the harmony root at engine construction (E2)
	DefaultRootNote = 40
)

// SamplesPerStep returns samples per 16th step at the given tempo
func SamplesPerStep(bpm int) int {
	return AudioSampleRate * 60 / (bpm * StepsPerBeat)
}

// SamplesPerBar returns samples per bar of meter m at the given tempo
func SamplesPerBar(bpm int, m Meter) int {
	return SamplesPerStep(bpm) * m.Steps()
}

// Meter is a bar's time signature, Num beats of a 1/Den note; the zero value is
// 4/4, as ParseMeter gives it. A step stays a sixteenth and the tempo counts
// quarters, so a bar holds StepsPerBar*Num/Den steps (3/4 and 6/8 twelve, 3/8
// six) and a beat 16/Den.
type Meter struct{ Num, Den int }

// ParseMeter reads "n/d", "" being 4/4
func ParseMeter(s string) (Meter, error) {
	if s == "" {
		return Meter{}, nil
	}
	var m Meter
	if n, err := fmt.Sscanf(s, "%d/%d", &m.Num, &m.Den); n != 2 || err != nil || m.String() != s {
		return Meter{}, fmt.Errorf("meter %q: want beats/note, as 3/4 or 6/8", s)
	}
	if err := m.check(); err != nil {
		return Meter{}, err
	}
	return meterOf(m.Num, m.Den), nil
}

// meterOf is n/d with 4/4 the zero value
func meterOf(n, d int) Meter {
	if n == 4 && d == 4 {
		return Meter{}
	}
	return Meter{n, d}
}

// check holds a meter to what the grid plays: the note a power of two to a
// sixteenth, so a beat is whole steps, and a bar of at most MaxPatternLen steps
func (m Meter) check() error {
	switch {
	case m == Meter{}:
		return nil
	case m.Den < 1 || m.Den > StepsPerBar || m.Den&(m.Den-1) != 0:
		return fmt.Errorf("meter %d/%d: the note is 1, 2, 4, 8 or 16", m.Num, m.Den)
	case m.Num < 1 || m.Num > MaxPatternLen || m.Steps() > MaxPatternLen:
		return fmt.Errorf("meter %d/%d: a bar of 1 to %d sixteenths", m.Num, m.Den, MaxPatternLen)
	}
	return nil
}

func (m Meter) norm() Meter {
	if m.Num == 0 || m.Den == 0 {
		return Meter{4, 4}
	}
	return m
}

// Steps is the steps a bar holds
func (m Meter) Steps() int { m = m.norm(); return StepsPerBar * m.Num / m.Den }

// Beat is the steps a beat holds, the denominator's note
func (m Meter) Beat() int { return StepsPerBar / m.norm().Den }

// Beats is the beats a bar holds
func (m Meter) Beats() int { return m.norm().Num }

// Compound is whether the bar is felt in dotted beats of three of its notes:
// 6/8, 9/8, 12/8
func (m Meter) Compound() bool {
	m = m.norm()
	return m.Den >= 8 && m.Num%3 == 0 && m.Num > 3
}

// Felt is the beats a bar is felt in and the steps of each: a compound meter's
// are its dotted beats, 6/8 two of six steps
func (m Meter) Felt() (beats, steps int) {
	if m.Compound() {
		return m.Beats() / 3, 3 * m.Beat()
	}
	return m.Beats(), m.Beat()
}

func (m Meter) String() string { m = m.norm(); return fmt.Sprintf("%d/%d", m.Num, m.Den) }

// Doc is the meter as a document writes it, "" for 4/4
func (m Meter) Doc() string {
	if m.norm() == (Meter{4, 4}) {
		return ""
	}
	return m.String()
}

// --- Transitions / generative ---
const (
	MinCrossfadeSamples = 256 // ~6ms declick floor for pattern swaps
	MusicStartFadeIn    = 1200 * time.Millisecond

	HumanizeVelJitter       = 0.12
	HumanizeMaxDelaySamples = AudioSampleRate * 4 / 1000 // 4ms positive lag
	MaxPendingTrigs         = 16

	PhraseBars    = 8
	FillEveryBars = 8
	GroupPhrases  = 4 // phrases one group holds before the music moves to another
	// VariationGapBars is the least a drawn slot rests after any slot changed before a
	// phrase varies or moves it, so a tier change and a variation never stack
	VariationGapBars = 4
)
