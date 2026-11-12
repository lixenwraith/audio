package audio

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"slices"
	"strings"
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

// struck is the analysis with the planes a notes model adds read off its
// salience, so a test reaches the stages only a strike plane starts, offline and
// live: a pitch is struck where its salience rises, as surely as its share of
// the rise and its level against its register's loudest heard, and let go
// where it falls
type struck struct{}

func (struck) Hear(x []float64, rate int, p RipParams) (*RipFrames, error) {
	return new(struckEar).strike(analysis{}.Hear(x, rate, p))
}

func (struck) Ear(p RipParams, spectrum bool) (RipEar, error) {
	ear, err := analysis{}.Ear(p, spectrum)
	return &struckEar{ear: ear}, err
}

type struckEar struct {
	ear  RipEar
	was  [2][]float32 // the salience two frames and one before the next
	loud [2]float32   // each register's loudest salience heard
}

func (e *struckEar) Hear(x []float64) (*RipFrames, error) { return e.strike(e.ear.Hear(x)) }
func (e *struckEar) Close() (*RipFrames, error)           { return e.strike(e.ear.Close()) }

func (e *struckEar) strike(f *RipFrames, err error) (*RipFrames, error) {
	if err != nil {
		return nil, err
	}
	n := f.frames() * RipPitches
	f.Strike, f.Release, f.Velocity = make([]float32, n), make([]float32, n), make([]float32, n)
	for t := range f.frames() {
		row := f.Salience[t*RipPitches:][:RipPitches]
		for i, v := range row {
			reg := min(i/(RipBassTop-RipMinMIDI), 1)
			e.loud[reg] = max(e.loud[reg], v)
			if e.was[0] == nil || e.loud[reg] <= 0 {
				continue
			}
			level := min(2*v/e.loud[reg], 1)
			switch was := e.was[0][i]; {
			case v > was:
				f.Strike[t*RipPitches+i], f.Velocity[t*RipPitches+i] = (v-was)/v*level, level
			case was > 0:
				f.Release[t*RipPitches+i] = (was - v) / was
			}
		}
		e.was[0], e.was[1] = e.was[1], slices.Clone(row)
	}
	return f, nil
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
	bass, melody, _ := f.account(x, DefaultRipParams(), accAsk{}, nil, reported)
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
	bass, melody, _ := f.account(x, DefaultRipParams(), accAsk{}, nil, reported)
	if got := accPitches(slices.Concat(bass, melody)); !slices.Equal(got, []int{57, 62}) {
		t.Errorf("notes kept %v, want the A3 reported and the D4 struck in the recording, not the B4 struck on silence", got)
	}
	reported[0].cells = []ripVisit{{midi: 57, start: 50, end: 150}, {midi: 62, start: 150, end: 350}}
	bass, melody, _ = f.account(x, DefaultRipParams(), accAsk{}, nil, reported)
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
	bass, melody, _ := f.account(x, DefaultRipParams(), accAsk{}, reported, nil)
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
	bass, melody, _ := f.account(x, DefaultRipParams(), accAsk{}, nil, reported)
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
	if _, melody, _ := f.account(scoop, DefaultRipParams(), accAsk{}, nil, []ripNote{followed}); len(melody) != 1 || melody[0].midi != 67 || melody[0].end != end || len(melody[0].cells) != 3 {
		t.Errorf("a scoop from F4 kept as %v, want the one note of G4 to its end with its cells", melody)
	}
	held := make([]float64, frames*RipHop)
	accTone(held, 67, from+35, end, 0.3, 8)
	if bass, melody, _ := f.account(held, DefaultRipParams(), accAsk{}, nil, []ripNote{followed}); len(bass)+len(melody) != 0 {
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
	if _, melody, _ := f.account(scoop, DefaultRipParams(), accAsk{}, nil, []ripNote{followed, over}); len(melody) != 1 || melody[0].midi != 67 {
		t.Errorf("a ghost an octave over a scoop's landing kept: %v", accPitches(melody))
	}
	accTone(scoop, over.midi, over.start, over.end, 0.3, 8)
	if _, melody, _ := f.account(scoop, DefaultRipParams(), accAsk{}, nil, []ripNote{followed, over}); len(melody) != 2 {
		t.Errorf("a note played an octave over a scoop's landing read as %v", accPitches(melody))
	}
}

// TestAJudgeRulesEveryStrike: under a judge whose networks say yes every
// reported note stays and every strike over a half comes back, in both
// registers, under the decoder's bars and on a pitch still sounding, where it
// ends the kept note before it; under one that says no only the held note stays.
func TestAJudgeRulesEveryStrike(t *testing.T) {
	const frames = 800
	x := make([]float64, frames*RipHop)
	accDrone(x, 57, 50, 750, 0.5, 16)
	accDrone(x, 69, 100, 400, 0.02, 8)
	accNoise(x, 1e-3)
	reported := []ripNote{{start: 50, end: 750, midi: 57, vel: 0.5}, {start: 100, end: 400, midi: 69, vel: 0.3}, {start: 500, end: 520, midi: 69, vel: 0.3}}
	// a strike on a silent pitch, one on the A3 still sounding and one under E3
	f := accFrames(frames, reported, map[int]float32{200*1000 + 62: 0.6, 300*1000 + 57: 0.7, 400*1000 + 40: 0.55})
	for u := 100; u < 780; u++ {
		f.Salience[u*RipPitches+40-RipMinMIDI] = 0.9
	}
	for _, n := range reported[1:] {
		f.Strike[n.start*RipPitches+69-RipMinMIDI] = 0.8
		for u := n.start; u < n.end; u++ {
			f.Salience[u*RipPitches+69-RipMinMIDI] = 0.6
		}
	}
	yes := accAsk{judge: judgeSaying([judgeStages]float64{0.9, 0.9})}
	bass, melody, _ := f.account(x, DefaultRipParams(), yes, nil, slices.Clone(reported))
	if got := accPitches(slices.Concat(bass, melody)); !slices.Equal(got, []int{40, 57, 57, 62, 69, 69}) {
		t.Fatalf("a judge that says yes kept %v, want the three reported and the three strikes dropped", got)
	}
	if melody[0].midi != 57 || melody[0].end != 300 {
		t.Errorf("the A3 ends at %d, want 300, where the strike that came back on it starts", melody[0].end)
	}
	no := accAsk{judge: judgeSaying([judgeStages]float64{0.1, 0.1})}
	bass, melody, _ = f.account(x, DefaultRipParams(), no, nil, slices.Clone(reported))
	if len(bass) != 0 || len(melody) != 1 || melody[0].midi != 69 || melody[0].end != 400 {
		t.Errorf("a judge that says no kept %v and %v, want the held A4 alone", bass, melody)
	}
}

// TestTheStrikesAFollowedSoundTookInAreNoCandidates: a judge is asked once of a
// note the decoder followed across cells, and not of the strikes in the cells
// it moved on to, while a strike beside them is a candidate as any.
func TestTheStrikesAFollowedSoundTookInAreNoCandidates(t *testing.T) {
	const frames, from, end = 400, 50, 140
	scoop, f, followed := accScoop(frames, from, end)
	for at, midi := range map[int]int{from + 22: 66, from + 60: 67, from + 61: 72} {
		f.Strike[at*RipPitches+midi-RipMinMIDI] = 0.7
	}
	yes := accAsk{judge: judgeSaying([judgeStages]float64{0.9, 0.9})}
	_, melody, _ := f.account(scoop, DefaultRipParams(), yes, nil, []ripNote{followed})
	if len(melody) != 2 || melody[0].midi != 67 || len(melody[0].cells) != 3 || melody[1].midi != 72 {
		t.Errorf("a scoop with strikes in its cells kept as %v, want its one note of G4 and the C5 struck beside it", melody)
	}
}

// accStruck is a recording of an A3 held, a D4 the decoder dropped whose sound
// is its own, and strikes on a B4 that never sounds, on the A3 still sounding
// and, under the decoder's bar, on an F4 that never sounds, with the note reported
func accStruck() (x []float64, f *RipFrames, reported []ripNote) {
	const frames = 400
	x = make([]float64, frames*RipHop)
	accTone(x, 57, 50, 350, 0.3, 8)
	accTone(x, 62, 150, 350, 0.3, 8)
	reported = []ripNote{{start: 50, end: 350, midi: 57, vel: 0.5}}
	f = accFrames(frames, slices.Concat(reported, []ripNote{{start: 150, end: 350, midi: 62}, {start: 150, end: 350, midi: 71}}),
		map[int]float32{250*1000 + 57: 0.85, 200*1000 + 65: 0.6})
	for t := 150; t < 350; t++ {
		f.Salience[t*RipPitches+71-RipMinMIDI] = 0.4
	}
	return x, f, reported
}

// TestTheStrikesReadChangesNoNote: asked for the strikes and their rows, the
// accounting keeps the notes it keeps unasked, under the installed scores and
// under a judge; with no judge it lists the reported note and every strike over
// a half with the installed verdict, one the scores never weigh dropped.
func TestTheStrikesReadChangesNoNote(t *testing.T) {
	x, f, reported := accStruck()
	for name, judge := range map[string]*RipJudge{"the installed scores": nil, "a judge": judgeSaying([judgeStages]float64{0.9, 0.9})} {
		bass, melody, none := f.account(x, DefaultRipParams(), accAsk{judge: judge}, nil, slices.Clone(reported))
		readBass, readMelody, strikes := f.account(x, DefaultRipParams(), accAsk{judge: judge, strikes: true, rows: true}, nil, slices.Clone(reported))
		if !reflect.DeepEqual(bass, readBass) || !reflect.DeepEqual(melody, readMelody) || none != nil || len(strikes) != 5 {
			t.Errorf("under %s the read gives %d strikes and the notes %v, unread %v", name, len(strikes), accPitches(readMelody), accPitches(melody))
		}
	}
	_, _, strikes := f.account(x, DefaultRipParams(), accAsk{strikes: true}, nil, slices.Clone(reported))
	var got []string
	for _, st := range strikes {
		got = append(got, fmt.Sprint(st.Frame, st.MIDI, st.Reported, st.Kept, st.Odds >= accCandBar, st.Odds == 0, st.Row == nil))
	}
	want := []string{
		"50 57 true true true false true",     // the A3 reported
		"150 62 false true true false true",   // the D4, which comes back
		"150 71 false false false false true", // the B4 on silence, weighed and refused
		"200 65 false false false true true",  // the F4 under the decoder's bar, never weighed
		"250 57 false false false false true", // the A3 struck while it sounds, the decoder's to judge
	}
	if !slices.Equal(got, want) {
		t.Errorf("the strikes read as frame, pitch, reported, kept, odds over the bar, odds unweighed, no row:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestAStrikesRowIsTheRowItsJudgeRead: the odds a judge gives a strike are its
// network's over the row the read lists for it, and where the judge keeps the
// reported notes the installed scores keep, that row is the one the read lists
// with no judge loaded, which a fit takes.
func TestAStrikesRowIsTheRowItsJudgeRead(t *testing.T) {
	x, f, reported := accStruck()
	ask := accAsk{strikes: true, rows: true}
	_, _, written := f.account(x, DefaultRipParams(), ask, nil, slices.Clone(reported))
	ask.judge = judgeSaying([judgeStages]float64{0.9, 0.5})
	n := &ask.judge.stages[judgeDropped] // by the strike's term: over a logit of 1.3
	n.w, n.b = [3][]float32{make([]float32, RipRowSize), {1}, {1}}, [3][]float32{{-1}, {0}, {-0.3}}
	n.w[0][4] = 1
	_, _, read := f.account(x, DefaultRipParams(), ask, nil, slices.Clone(reported))
	if len(read) != len(written) || len(read) != 5 {
		t.Fatalf("%d strikes read under the judge, %d with none, want 5", len(read), len(written))
	}
	for i, st := range read {
		stage := judgeDropped
		if st.Reported {
			stage = judgeReported
		}
		if len(st.Row) != RipRowSize || !slices.Equal(st.Row, written[i].Row) {
			t.Errorf("the strike at %d on %d: the judge read a row that is not the one written with none loaded", st.Frame, st.MIDI)
		}
		if odds := ask.judge.odds(stage, st.Row); st.Odds != odds || st.Kept != (odds >= accJudgeBar) {
			t.Errorf("the strike at %d on %d has odds %.4f and kept %v, its row gives %.4f", st.Frame, st.MIDI, st.Odds, st.Kept, odds)
		}
	}
	if read[3].Kept || !read[4].Kept {
		t.Errorf("the judge kept the strike of 0.6 (%v) and dropped the one of 0.85 (%v), want them by their strike's term", read[3].Kept, read[4].Kept)
	}
}

// TestARowHoldsTheScoresTermsAndTheHeadsAroundTheStrike: a strike's row opens
// with the terms the installed score weighs, scaled as it scales them, then the
// planes around the strike, a frame at a time, the salience and the flux over
// their levels; a cell off the frames or the pitches is 0.
func TestARowHoldsTheScoresTermsAndTheHeadsAroundTheStrike(t *testing.T) {
	const frames = 40
	f := accFrames(frames, nil, nil)
	code := func(t, midi int) float32 { return float32(1000*t + midi) }
	for t := range frames {
		f.Flux[t] = float64(t + 1)
		for midi := RipMinMIDI; midi < RipMaxMIDI; midi++ {
			i := t*RipPitches + midi - RipMinMIDI
			f.Strike[i], f.Salience[i], f.Release[i], f.Velocity[i] = code(t, midi), 2*code(t, midi), -code(t, midi), code(t, midi)+0.5
		}
	}
	in := accInput{accEvidence: accEvidence{share: 0.5, unF0: 0.25, unOdd: 0.75, rise: 20, loo: 3, level: -30, span: 0.5, step: 0.2}, strike: 0.9, support: 2, low: true, ringing: true}
	const ref, flux = -20.0, 2.0
	refs := [2]float64{2, 4}
	low, high := RipMinMIDI+3, RipMaxMIDI-1
	row := f.row(nil, &accNote{ripNote: ripNote{start: 2, midi: low}}, in, ref, refs, flux)
	if len(row) != RipRowSize || RipRowSize != 544 {
		t.Fatalf("a row of %d values and RipRowSize %d, want 544", len(row), RipRowSize)
	}
	w := accReported
	v := w.bias
	for k, wk := range [...]float64{w.share, w.unF0, w.unOdd, w.rise, w.strike, w.support, w.low, w.level, w.ringing, w.span, w.step} {
		v += wk * float64(row[k])
	}
	if got, want := 1/(1+math.Exp(-v)), w.odds(in, ref); math.Abs(got-want) > 1e-6 {
		t.Errorf("the score over the row's terms gives %.7f, its own odds %.7f", got, want)
	}
	planes := len(accRowPitches)
	at := func(row []float32, plane, dt, d int) float32 { // a plane's cell dt frames and d semitones from the strike
		if plane >= 2 {
			return row[accRowTerms+2*accRowFrames*planes+(plane-2)*accRowFrames+dt+accRowBefore]
		}
		return row[accRowTerms+plane*accRowFrames*planes+(dt+accRowBefore)*planes+slices.Index(accRowPitches[:], d)]
	}
	check := func(name string, got, want float32) {
		if got != want {
			t.Errorf("%s reads %v, want %v", name, got, want)
		}
	}
	check("the strike at its own cell", at(row, 0, 0, 0), code(2, low))
	check("the strike three frames on an octave up", at(row, 0, 3, 12), code(5, low+12))
	check("the strike before the first frame", at(row, 0, -4, 0), 0)
	check("the strike under the lowest pitch", at(row, 0, 0, -24), 0)
	check("the salience over the bass's reference", at(row, 1, 1, 7), 2*code(3, low+7)/2)
	check("the release at its pitch", at(row, 2, 8, 0), -code(10, low))
	check("the velocity at its pitch", at(row, 3, -2, 0), code(0, low)+0.5)
	check("the flux over its level", at(row, 4, 0, 0), 3.0/2)
	in.low = false
	row = f.row(nil, &accNote{ripNote: ripNote{start: frames - 1, midi: high}}, in, ref, refs, flux)
	check("the salience over the melody's reference", at(row, 1, 0, -12), 2*code(frames-1, high-12)/4)
	check("the strike over the highest pitch", at(row, 0, 0, 1), 0)
	check("the flux past the last frame", at(row, 4, 1, 0), 0)
}
