package audio

import (
	"math"
	"slices"
	"testing"
)

// accTone is a note of harmonics falling as 1/h, struck at from and ringing to
// end, each harmonic fading at its own rate, added into x at RipRate
func accTone(x []float64, midi, from, end int, level float64) {
	f0 := NoteFreq(midi)
	for h := 1; h <= 8; h++ {
		for i := from * RipHop; i < end*RipHop && i < len(x); i++ {
			dt := float64(i-from*RipHop) / RipRate
			x[i] += level / float64(h) * math.Exp(-dt*float64(h)) * math.Sin(2*math.Pi*f0*float64(h)*dt)
		}
	}
}

// accFrames is a front end that hears every pitch given as sounding over its
// frames and struck at its start, as surely as said
func accFrames(frames int, notes []ripNote, strikes map[int]float32) *RipFrames {
	f := &RipFrames{Hop: RipFrameSec, Flux: make([]float64, frames), Salience: make([]float32, frames*RipPitches), Strike: make([]float32, frames*RipPitches), Release: make([]float32, frames*RipPitches), Velocity: make([]float32, frames*RipPitches)}
	at := func(t, midi int) int { return t*RipPitches + midi - RipMinMIDI }
	for _, n := range notes {
		for t := n.start; t < n.end; t++ {
			f.Salience[at(t, n.midi)] = 0.9
		}
		f.Strike[at(n.start, n.midi)] = 0.95
		f.Velocity[at(n.start, n.midi)] = 0.5
	}
	for k, v := range strikes {
		f.Strike[at(k/1000, k%1000)] = v
	}
	return f
}

func accPitches(notes []ripNote) []int {
	var out []int
	for _, n := range notes {
		out = append(out, n.midi)
	}
	slices.Sort(out)
	return out
}

// TestANoteTheOthersAccountForGoes: a note reported at the octave of a note
// still ringing, whose harmonics the ringing note's own account for, goes, the
// lower note having sounded alone before so its template knows its second
// harmonic; the ringing note and a note of its own sound struck beside it stay.
func TestANoteTheOthersAccountForGoes(t *testing.T) {
	const frames = 1000
	x := make([]float64, frames*RipHop)
	reported := []ripNote{{start: 50, end: 300, midi: 57, vel: 0.5}, {start: 350, end: 600, midi: 57, vel: 0.5}, {start: 650, end: 950, midi: 57, vel: 0.5}, {start: 700, end: 950, midi: 64, vel: 0.5}, {start: 700, end: 950, midi: 69, vel: 0.5}}
	for _, n := range reported {
		if n.midi != 69 {
			accTone(x, n.midi, n.start, n.end, 0.3)
		}
	}
	f := accFrames(frames, reported, nil)
	bass, melody := f.account(x, DefaultRipParams(), nil, reported)
	if got := accPitches(slices.Concat(bass, melody)); !slices.Equal(got, []int{57, 57, 57, 64}) {
		t.Errorf("notes kept %v, want the three A3s and the E4 and not the A4 their sound accounts for", got)
	}
}

// TestAStrikeWhoseSoundIsItsOwnComesBack: a pitch the recording plays whose
// strike the decoder dropped is a candidate, and comes back when the kept
// notes leave its sound unexplained, while a strike on a silent pitch does not.
func TestAStrikeWhoseSoundIsItsOwnComesBack(t *testing.T) {
	const frames = 400
	x := make([]float64, frames*RipHop)
	accTone(x, 57, 50, 350, 0.3)
	accTone(x, 62, 150, 350, 0.3)
	reported := []ripNote{{start: 50, end: 350, midi: 57, vel: 0.5}}
	f := accFrames(frames, slices.Concat(reported, []ripNote{{start: 150, end: 350, midi: 62}, {start: 150, end: 350, midi: 71}}), nil)
	bass, melody := f.account(x, DefaultRipParams(), nil, reported)
	if got := accPitches(slices.Concat(bass, melody)); !slices.Equal(got, []int{57, 62}) {
		t.Errorf("notes kept %v, want the A3 reported and the D4 struck in the recording, not the B4 struck on silence", got)
	}
}

// TestTwoNotesOfOnePitchAtOneStartAreOne: the strike's note and the bass
// line's own note of one pitch at one start merge into one, the longer end kept.
func TestTwoNotesOfOnePitchAtOneStartAreOne(t *testing.T) {
	const frames = 400
	x := make([]float64, frames*RipHop)
	accTone(x, 40, 50, 350, 0.3)
	reported := []ripNote{{start: 50, end: 120, midi: 40, vel: 0.6}, {start: 51, end: 300, midi: 40, vel: 0.4}}
	f := accFrames(frames, reported, nil)
	bass, melody := f.account(x, DefaultRipParams(), reported, nil)
	if len(bass) != 1 || len(melody) != 0 || bass[0].start != 50 || bass[0].end != 300 || bass[0].vel != 0.6 {
		t.Errorf("bass %+v and %d melody notes, want one E2 from 50 to 300 at the strike's velocity", bass, len(melody))
	}
}
