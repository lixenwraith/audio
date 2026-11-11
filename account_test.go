package audio

import (
	"math"
	"math/rand"
	"slices"
	"testing"
)

// accDrone is a note held without fading, its harmonics falling as 1/h
func accDrone(x []float64, midi, from, end int, level float64, harmonics int) {
	f0 := NoteFreq(midi)
	for h := 1; h <= harmonics; h++ {
		for i := from * RipHop; i < end*RipHop && i < len(x); i++ {
			x[i] += level / float64(h) * math.Sin(2*math.Pi*f0*float64(h)*float64(i)/RipRate)
		}
	}
}

// accNoise lays a white floor level under x, the same every run
func accNoise(x []float64, level float64) {
	r := rand.New(rand.NewSource(1))
	for i := range x {
		x[i] += level * (2*r.Float64() - 1)
	}
}

// accTone is a note of harmonics harmonics falling as 1/h, struck at from and
// ringing to end, each harmonic fading at its own rate, added into x at RipRate
func accTone(x []float64, midi, from, end int, level float64, harmonics int) {
	f0 := NoteFreq(midi)
	for h := 1; h <= harmonics; h++ {
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

// TestANoteTheOthersAccountForGoes: a short note reported at the octave of a
// note still ringing, whose harmonics the ringing note's own account for, goes,
// the lower note having sounded alone before so its template knows its second
// harmonic; the ringing note and a note of its own sound struck beside it stay.
func TestANoteTheOthersAccountForGoes(t *testing.T) {
	const frames = 1000
	x := make([]float64, frames*RipHop)
	reported := []ripNote{{start: 50, end: 300, midi: 57, vel: 0.5}, {start: 350, end: 600, midi: 57, vel: 0.5}, {start: 650, end: 950, midi: 57, vel: 0.5}, {start: 700, end: 950, midi: 64, vel: 0.5}, {start: 700, end: 760, midi: 69, vel: 0.5}}
	for _, n := range reported {
		if n.midi != 69 {
			accTone(x, n.midi, n.start, n.end, 0.3, 8)
		}
	}
	f := accFrames(frames, reported, nil)
	at := 700*RipPitches + 69 - RipMinMIDI
	f.Strike[at] = 0.8 // the head's doubt of a partial, as it reads one in a recording
	for t := 700; t < 760; t++ {
		f.Salience[t*RipPitches+69-RipMinMIDI] = 0.75
	}
	bass, melody := f.account(x, DefaultRipParams(), nil, reported)
	if got := accPitches(slices.Concat(bass, melody)); !slices.Equal(got, []int{57, 57, 57, 64}) {
		t.Errorf("notes kept %v, want the three A3s and the E4 and not the A4 their sound accounts for", got)
	}
}

// TestAStrikeWhoseSoundIsItsOwnComesBack: a pitch the recording plays whose
// strike the decoder dropped is a candidate, and comes back when the kept
// notes leave its sound unexplained, while a strike on a silent pitch does not,
// nor one in a cell a kept note's glide passes through, the decoder's to judge.
func TestAStrikeWhoseSoundIsItsOwnComesBack(t *testing.T) {
	const frames = 400
	x := make([]float64, frames*RipHop)
	accTone(x, 57, 50, 350, 0.3, 8)
	accTone(x, 62, 150, 350, 0.3, 8)
	reported := []ripNote{{start: 50, end: 350, midi: 57, vel: 0.5}}
	f := accFrames(frames, slices.Concat(reported, []ripNote{{start: 150, end: 350, midi: 62}, {start: 150, end: 350, midi: 71}}), nil)
	for t := 150; t < 350; t++ { // the pitch head's doubt of a pitch that never sounds
		f.Salience[t*RipPitches+71-RipMinMIDI] = 0.4
	}
	bass, melody := f.account(x, DefaultRipParams(), nil, reported)
	if got := accPitches(slices.Concat(bass, melody)); !slices.Equal(got, []int{57, 62}) {
		t.Errorf("notes kept %v, want the A3 reported and the D4 struck in the recording, not the B4 struck on silence", got)
	}
	reported[0].cells = []ripVisit{{midi: 57, start: 50, end: 150}, {midi: 62, start: 150, end: 350}}
	bass, melody = f.account(x, DefaultRipParams(), nil, reported)
	if got := accPitches(slices.Concat(bass, melody)); !slices.Equal(got, []int{57}) {
		t.Errorf("notes kept %v, want the A3 alone when its sound passes through the D4's cell", got)
	}
}

// TestTwoNotesOfOnePitchAtOneStartAreOne: the strike's note and the bass
// line's own note of one pitch at one start merge into one, the longer end kept.
func TestTwoNotesOfOnePitchAtOneStartAreOne(t *testing.T) {
	const frames = 400
	x := make([]float64, frames*RipHop)
	accTone(x, 40, 50, 350, 0.3, 8)
	reported := []ripNote{{start: 50, end: 120, midi: 40, vel: 0.6}, {start: 51, end: 300, midi: 40, vel: 0.4}}
	f := accFrames(frames, reported, nil)
	bass, melody := f.account(x, DefaultRipParams(), reported, nil)
	if len(bass) != 1 || len(melody) != 0 || bass[0].start != 50 || bass[0].end != 300 || bass[0].vel != 0.6 {
		t.Errorf("bass %+v and %d melody notes, want one E2 from 50 to 300 at the strike's velocity", bass, len(melody))
	}
}

// TestARingingNoteSoundsOnThroughADroppedRestrike: a note the decoder cut at a
// strike of its pitch sounds on to the dropped note's end when that strike was
// no new note, and is left alone where the dropped note started later.
func TestARingingNoteSoundsOnThroughADroppedRestrike(t *testing.T) {
	kept := []*accNote{{ripNote: ripNote{start: 50, end: 150, midi: 60}}, {ripNote: ripNote{start: 400, end: 450, midi: 60}}, {ripNote: ripNote{start: 50, end: 150, midi: 64}}}
	dropped := []*accNote{{ripNote: ripNote{start: 150, end: 300, midi: 60}}, {ripNote: ripNote{start: 470, end: 500, midi: 60}}}
	accRingOn(kept, dropped)
	if kept[0].end != 300 || kept[1].end != 450 || kept[2].end != 150 {
		t.Errorf("ends %d %d %d, want 300 (sounding on), 450 (the drop started later) and 150 (another pitch)", kept[0].end, kept[1].end, kept[2].end)
	}
}

// TestAHeldNoteTheFitCannotSeeStaysOnTheHeadsWord: a soft line held at the
// octave of a loud note, whose harmonics cover all of its own, stays, as the fit
// has no evidence of its own there, while a short note under the same cover is
// judged.
func TestAHeldNoteTheFitCannotSeeStaysOnTheHeadsWord(t *testing.T) {
	const frames = 800
	x := make([]float64, frames*RipHop)
	accDrone(x, 57, 50, 750, 0.5, 16)  // A3, loud and held
	accDrone(x, 69, 100, 400, 0.02, 8) // A4 held soft under it
	accNoise(x, 1e-3)                  // a room's floor
	reported := []ripNote{{start: 50, end: 750, midi: 57, vel: 0.5}, {start: 100, end: 400, midi: 69, vel: 0.3}, {start: 500, end: 520, midi: 69, vel: 0.3}}
	f := accFrames(frames, reported, nil)
	for _, n := range reported[1:] { // the heads' doubt of a soft line
		f.Strike[n.start*RipPitches+69-RipMinMIDI] = 0.8
		for u := n.start; u < n.end; u++ {
			f.Salience[u*RipPitches+69-RipMinMIDI] = 0.6
		}
	}
	bass, melody := f.account(x, DefaultRipParams(), nil, reported)
	if got := accPitches(slices.Concat(bass, melody)); !slices.Equal(got, []int{57, 69}) || melody[1].end != 400 {
		t.Errorf("notes kept %v, want the A3 and the held A4 alone, not the short A4", got)
	}
}

// TestTheMemoryTellsARingFromAStrike: a note reported on a pitch still sounding
// reads no step in its memory where the recording only rings on, even through a
// louder neighbour's attack a semitone up, whose partials the memory takes out,
// and a step as large as the new sound where the pitch is struck again.
func TestTheMemoryTellsARingFromAStrike(t *testing.T) {
	const frames = 600
	ring := make([]float64, frames*RipHop)
	accDrone(ring, 57, 50, 550, 0.3, 8)
	neighbour := slices.Clone(ring)
	accTone(neighbour, 58, 300, 550, 0.5, 8)
	struck := slices.Clone(ring)
	accTone(struck, 57, 300, 550, 0.3, 8)
	reported := []ripNote{{start: 50, end: 300, midi: 57}, {start: 300, end: 550, midi: 57}}
	step := func(x []float64, extra ...ripNote) float64 {
		notes := accNotes(slices.Concat(reported, extra), false, 0, frames)
		accMeasure(x, notes)
		for _, n := range notes {
			if n.start == 300 && n.midi == 57 {
				return n.step
			}
		}
		return -1
	}
	if s := step(ring); s > 0.3 {
		t.Errorf("a ring sounding on reads a step of %.2f, want under 0.3", s)
	}
	if s := step(neighbour, ripNote{start: 300, end: 550, midi: 58}); s > 0.3 {
		t.Errorf("a ring through a neighbour's attack reads a step of %.2f, want under 0.3", s)
	}
	if s := step(struck); s < 0.8 {
		t.Errorf("a pitch struck again reads a step of %.2f, want over 0.8", s)
	}
}

// accScoop is a recording of frames frames that plays F4 from frame from for
// twelve frames, rises to G4 over thirty and holds it to end, with the frames a
// front end hears of it and the one note of G4 the decoder follows it as
func accScoop(frames, from, end int) (x []float64, f *RipFrames, followed ripNote) {
	x = make([]float64, frames*RipHop)
	for h := 1; h <= 8; h++ {
		var phase float64
		for i := from * RipHop; i < end*RipHop; i++ {
			at := float64(i)/RipHop - float64(from)
			phase += 2 * math.Pi * NoteFreq(65) * math.Pow(2, min(max(at-12, 0)/30, 1)*2/12) * float64(h) / RipRate
			x[i] += 0.3 / float64(h) * math.Exp(-at*RipHop/RipRate*float64(h)) * math.Sin(phase)
		}
	}
	cells := []ripVisit{{midi: 65, start: from, end: from + 20}, {midi: 66, start: from + 20, end: from + 35}, {midi: 67, start: from + 35, end: end}}
	f = accFrames(frames, []ripNote{{start: from, end: from + 20, midi: 65}}, nil)
	for _, v := range cells[1:] {
		for t := v.start; t < v.end; t++ {
			f.Salience[t*RipPitches+v.midi-RipMinMIDI] = 0.9
		}
	}
	return x, f, ripNote{start: from, end: end, midi: 67, vel: 0.5, cells: cells}
}

// TestAFollowedSoundIsJudgedWhereItWasStruck: a note the decoder followed up two
// cells from its strike and placed at the pitch it settles on is measured in the
// cell it was struck in, where its sound is, and stays whole; the same note over
// a recording that plays nothing in that cell goes whole.
func TestAFollowedSoundIsJudgedWhereItWasStruck(t *testing.T) {
	const frames, from, end = 400, 50, 140
	scoop, f, followed := accScoop(frames, from, end)
	if _, melody := f.account(scoop, DefaultRipParams(), nil, []ripNote{followed}); len(melody) != 1 || melody[0].midi != 67 || melody[0].end != end || len(melody[0].cells) != 3 {
		t.Errorf("a scoop from F4 kept as %v, want the one note of G4 to its end with its cells", melody)
	}
	held := make([]float64, frames*RipHop)
	accTone(held, 67, from+35, end, 0.3, 8)
	if bass, melody := f.account(held, DefaultRipParams(), nil, []ripNote{followed}); len(bass)+len(melody) != 0 {
		t.Errorf("a followed sound whose first cell the recording never plays kept as %v", melody)
	}
}

// TestAFollowedSoundAccountsForTheCellsItMovedOnTo: a note heard an octave over
// the pitch a scoop settles on, as it lands there, is the scoop's own harmonics
// and goes, the scoop fitted in that cell too; one the recording plays stays.
func TestAFollowedSoundAccountsForTheCellsItMovedOnTo(t *testing.T) {
	const frames, from, end = 400, 50, 140
	scoop, f, followed := accScoop(frames, from, end)
	over := ripNote{start: from + 40, end: end, midi: 79, vel: 0.5}
	for t := over.start; t < over.end; t++ {
		f.Salience[t*RipPitches+over.midi-RipMinMIDI] = 0.9
	}
	f.Strike[over.start*RipPitches+over.midi-RipMinMIDI] = 0.95
	if _, melody := f.account(scoop, DefaultRipParams(), nil, []ripNote{followed, over}); len(melody) != 1 || melody[0].midi != 67 {
		t.Errorf("a ghost an octave over a scoop's landing kept: %v", accPitches(melody))
	}
	accTone(scoop, over.midi, over.start, over.end, 0.3, 8)
	if _, melody := f.account(scoop, DefaultRipParams(), nil, []ripNote{followed, over}); len(melody) != 2 {
		t.Errorf("a note played an octave over a scoop's landing read as %v", accPitches(melody))
	}
}
