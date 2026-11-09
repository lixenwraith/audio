package audio

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
)

// TestReadMIDIReadsTheSameTranscript: what a song's file adds, programs that
// change, a key and time signatures, a clap and a tom, a drum part on a track of
// its own, moves none of the notes, velocities or hits ReadMIDI reads, which
// truth scores and corpus labels from.
func TestReadMIDIReadsTheSameTranscript(t *testing.T) {
	file := smf(4)
	file = append(file, track(
		0x00, 0xFF, 0x51, 3, 0x07, 0xA1, 0x20, // 120 bpm
		0x00, 0xFF, 0x58, 4, 3, 2, 24, 8, // 3/4
		0x00, 0xFF, 0x59, 2, 1, 1, // E minor
		0x8B, 0x20, 0xFF, 0x58, 4, 6, 3, 12, 8, // 6/8 at 1440
		0x00, 0xFF, 0x2F, 0)...)
	file = append(file, track(
		0x00, 0xFF, 0x03, 5, 'P', 'i', 'a', 'n', 'o',
		0x00, 0xC0, 0, // grand piano on channel 1
		0x00, 0xC1, 33, // a bass on channel 2
		0x00, 0x90, 76, 100, // E5 at 0
		0x00, 0x91, 40, 90, // E2 at 0
		0x83, 0x60, 0x80, 76, 0, // E5's end at 480
		0x00, 0xC0, 40, // a violin on channel 1
		0x00, 0x90, 79, 70, // G5 at 480
		0x81, 0x70, 0x80, 79, 0, // its end at 720
		0x81, 0x70, 0x81, 40, 0, // E2's end at 960
		0x00, 0xFF, 0x2F, 0)...)
	file = append(file, track(
		0x00, 0x99, 36, 120, // a kick at 0
		0x83, 0x60, 0x89, 36, 0,
		0x00, 0x99, 39, 100, // a clap and a tom at 480
		0x00, 45, 80,
		0x81, 0x70, 0x89, 39, 0,
		0x00, 45, 0,
		0x00, 0x99, 42, 60, // a hat at 720
		0x78, 0x89, 42, 0,
		0x00, 0xFF, 0x2F, 0)...)
	file = append(file, track(
		0x87, 0x40, 0x99, 38, 110, // a snare at 960, on a track of its own
		0x78, 0x89, 38, 0,
		0x00, 0xFF, 0x2F, 0)...)
	m, err := ReadMIDI(bytes.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	notes := []RipNote{
		{Start: 0, End: 0.5, MIDI: 76, Vel: 100.0 / 127},
		{Start: 0, End: 1, MIDI: 40, Vel: 90.0 / 127, Bass: true},
		{Start: 0.5, End: 0.75, MIDI: 79, Vel: 70.0 / 127},
	}
	hits := []RipHit{{Start: 0, Drum: InstrKick, Vel: 120.0 / 127}, {Start: 0.75, Drum: InstrHihat, Vel: 60.0 / 127}, {Start: 1, Drum: InstrSnare, Vel: 110.0 / 127}}
	if len(m.Notes) != len(notes) || len(m.Hits) != len(hits) {
		t.Fatalf("read %+v and %+v", m.Notes, m.Hits)
	}
	for i, n := range m.Notes {
		w := notes[i]
		if n.MIDI != w.MIDI || n.Bass != w.Bass || n.Second || n.Bright != RipUnmeasured ||
			math.Abs(n.Start-w.Start) > 1e-9 || math.Abs(n.End-w.End) > 1e-9 || math.Abs(n.Vel-w.Vel) > 1e-9 {
			t.Errorf("note %d: %+v, want %+v", i, n, w)
		}
	}
	for i, h := range m.Hits {
		if w := hits[i]; h.Drum != w.Drum || math.Abs(h.Start-w.Start) > 1e-9 || math.Abs(h.Vel-w.Vel) > 1e-9 {
			t.Errorf("hit %d: %+v, want %+v", i, h, w)
		}
	}
	if m.BPM != 120 || m.Changes != 0 || m.Left != 0 {
		t.Errorf("%.0f bpm, %d change(s), %d part(s) left out", m.BPM, m.Changes, m.Left)
	}
}

// TestMIDIKeepsThePartsPastTheLinesApart: of four parts, the one past melody,
// under and bass is left out of the lines and kept whole as a further part
func TestMIDIKeepsThePartsPastTheLinesApart(t *testing.T) {
	m, err := ReadMIDI(bytes.NewReader(append(smf(1), track(
		0x00, 0x90, 72, 100, 0x00, 0x91, 64, 90, 0x00, 0x92, 36, 90, 0x00, 0x93, 67, 60, // C5, E4, C2 and G4 on four channels
		0x83, 0x60, 0x80, 72, 0, 0x00, 0x81, 64, 0, 0x00, 0x82, 36, 0, 0x00, 0x83, 67, 0,
		0x00, 0xFF, 0x2F, 0)...)))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Notes) != 3 || m.Left != 1 || len(m.Further) != 1 || m.Further[0].MIDI != 67 || !m.Further[0].Second || m.Further[0].End != 0.5 {
		t.Errorf("lines %+v, %d part(s) left out, further %+v; want G4 apart", m.Notes, m.Left, m.Further)
	}
}

// TestMIDINotesCarryTheirPrograms: each note read carries the program its
// channel was set to at its start, whatever line it folds into, so an ensemble's
// parts stay apart; a note before any is set carries -1
func TestMIDINotesCarryTheirPrograms(t *testing.T) {
	file := append(smf(2), track(
		0x00, 0xC0, 0, 0x00, 0x90, 72, 100, // a grand piano's C5 at 0
		0x83, 0x60, 0x80, 72, 0,
		0x00, 0xFF, 0x2F, 0)...)
	file = append(file, track(
		0x00, 0x91, 67, 90, // G4 at 0 on channel 2, no program set
		0x83, 0x60, 0x81, 67, 0,
		0x00, 0xC1, 40, 0x00, 0x91, 69, 90, // a violin's A4 at 480
		0x83, 0x60, 0x81, 69, 0,
		0x00, 0xFF, 0x2F, 0)...)
	m, err := ReadMIDI(bytes.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range m.Notes {
		got = append(got, fmt.Sprintf("%d %d", n.MIDI, n.Program))
	}
	slices.Sort(got)
	if want := []string{"67 -1", "69 40", "72 0"}; !slices.Equal(got, want) {
		t.Errorf("notes and programs %q, want %q", got, want)
	}
}

// sounding is what a song plays, a line a note or hit: its instrument and desc,
// the step from the song's start, the pitch under the song's key, the velocity
// as a note-on holds it and the length in steps, sorted
func sounding(t *testing.T, d *SongDef, pattern func(string) *PatternDef) []string {
	t.Helper()
	var out []string
	at, steps := 0, d.Signature().Steps()
	for _, bar := range d.Bars {
		for _, n := range bar {
			if n == "" {
				continue
			}
			p := pattern(n)
			for _, tr := range p.Track {
				instr, _ := InstrumentByName(tr.Instr)
				for j := range steps {
					for _, e := range tr.Event {
						if e.Pos != (at+j)%p.Steps {
							continue
						}
						pitch, dur := 0, 0
						if !instr.IsDrum() {
							pitch, dur = StepPitch(d.Root, d.ScaleID(), e.Deg, e.Oct, e.Acc), max(e.Dur, 1)
						}
						out = append(out, fmt.Sprintf("%s %q at %d: pitch %d vel %d for %d bent %+.2f from %.2f", tr.Instr, tr.Desc, at+j, pitch, int(math.Round(e.Vel*127)), dur, e.Bend, e.BendAt))
					}
				}
			}
		}
		at += steps
	}
	slices.Sort(out)
	return out
}

// lookup is a pattern by name among pats
func lookup(pats ...*PatternDef) func(string) *PatternDef {
	return func(n string) *PatternDef {
		i := slices.IndexFunc(pats, func(p *PatternDef) bool { return p.Name == n })
		if i < 0 {
			return nil
		}
		return pats[i]
	}
}

// TestPatternRoundTripsThroughMIDI: a pattern written as MIDI and loaded back
// holds the same notes, velocities and lengths, a note past its bar's end and a
// pitch off the key among them, and the file says nothing it could not keep
func TestPatternRoundTripsThroughMIDI(t *testing.T) {
	p := &PatternDef{Name: "tune", Steps: 64, Role: "melody", Track: []TrackDef{
		{Instr: "piano", Desc: "right hand", Event: []StepDef{{Pos: 0, Vel: 0.8, Dur: 4}, {Pos: 4, Vel: 0.5, Deg: 2, Acc: 1, Dur: 2}, {Pos: 14, Vel: 1, Deg: 4, Dur: 20}, {Pos: 40, Vel: 0.3, Deg: -1, Oct: 1}, {Pos: 63, Vel: 0.6, Deg: 6, Oct: -1, Dur: 3}}},
		{Instr: "lead", Event: []StepDef{{Pos: 2, Vel: 0.7, Deg: 7, Dur: 6}, {Pos: 18, Vel: 0.4, Deg: 3, Acc: -1}, {Pos: 33, Vel: 0.9, Deg: 9, Dur: 30}}},
		{Instr: "kick", Event: []StepDef{{Pos: 0, Vel: 1}, {Pos: 16, Vel: 0.9}, {Pos: 32, Vel: 0.8}, {Pos: 48, Vel: 0.7}}},
		{Instr: "snare", Event: []StepDef{{Pos: 8, Vel: 0.75}, {Pos: 24, Vel: 0.6}, {Pos: 56, Vel: 0.85}}},
		{Instr: "hihat", Event: []StepDef{{Pos: 2, Vel: 0.3}, {Pos: 6, Vel: 0.35}, {Pos: 50, Vel: 0.4}}},
		{Instr: "clap", Event: []StepDef{{Pos: 24, Vel: 0.5}}},
	}}
	var b bytes.Buffer
	if err := WritePatternMIDI(&b, p, 120, 45, ScaleMinor, nil); err != nil {
		t.Fatal(err)
	}
	m, err := ReadMIDI(&b)
	if err != nil {
		t.Fatal(err)
	}
	pats, song, said := m.Song("back")
	if len(said) > 0 || song.BPM != 120 || len(song.Bars) != 4 {
		t.Errorf("%d bar(s) at %d bpm, said %q", len(song.Bars), song.BPM, said)
	}
	once := &SongDef{Name: "tune", BPM: 120, Root: 45, Scale: "minor", Bars: [][]string{{"", "tune"}, {"", "tune"}, {"", "tune"}, {"", "tune"}}}
	want, got := sounding(t, once, lookup(p)), sounding(t, song, lookup(pats...))
	if !slices.Equal(got, want) {
		t.Errorf("loaded back\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestSongRoundTripsThroughMIDI: a song written as MIDI and loaded back plays the
// same notes, velocities and lengths, a bar each, and a bar it repeats is the
// pattern it played before
func TestSongRoundTripsThroughMIDI(t *testing.T) {
	pats := []*PatternDef{
		{Name: "beat", Steps: 16, Role: "rhythm", Track: []TrackDef{
			{Instr: "kick", Event: []StepDef{{Pos: 0, Vel: 1}, {Pos: 8, Vel: 0.8}}},
			{Instr: "hihat", Event: []StepDef{{Pos: 4, Vel: 0.4}, {Pos: 12, Vel: 0.45}}}}},
		{Name: "fill", Steps: 16, Role: "rhythm", Track: []TrackDef{
			{Instr: "snare", Event: []StepDef{{Pos: 0, Vel: 0.7}, {Pos: 2, Vel: 0.6}, {Pos: 4, Vel: 0.9}}},
			{Instr: "clap", Event: []StepDef{{Pos: 12, Vel: 1}}}}},
		{Name: "hook", Steps: 16, Role: "melody", Track: []TrackDef{
			{Instr: "piano", Event: []StepDef{{Pos: 0, Vel: 0.9, Deg: 4, Dur: 3}, {Pos: 6, Vel: 0.5, Deg: 2, Acc: -1, Dur: 10}}},
			{Instr: "pluck", Desc: "guitar", Event: []StepDef{{Pos: 2, Vel: 0.6, Deg: 0, Oct: 1}, {Pos: 10, Vel: 0.65, Deg: 1, Oct: 1, Dur: 2}}}}},
		{Name: "answer", Steps: 16, Role: "melody", Track: []TrackDef{
			{Instr: "pad", Event: []StepDef{{Pos: 0, Vel: 0.4, Deg: 0, Dur: 16}, {Pos: 0, Vel: 0.4, Deg: 2, Dur: 16}}}}},
		{Name: "low", Steps: 16, Role: "bass", Track: []TrackDef{
			{Instr: "bass", Event: []StepDef{{Pos: 0, Vel: 1, Oct: -1, Dur: 6}, {Pos: 8, Vel: 0.85, Deg: 4, Oct: -2, Dur: 6}}}}},
	}
	d := &SongDef{Name: "ditty", BPM: 96, Root: 43, Scale: "dorian", Bars: [][]string{
		{"beat", "hook", "", "low"}, {"beat", "answer", "", "low"}, {"beat", "hook", "", "low"}, {"fill", "hook", "", ""}}}
	var b bytes.Buffer
	if err := WriteSongMIDI(&b, d, lookup(pats...)); err != nil {
		t.Fatal(err)
	}
	m, err := ReadMIDI(&b)
	if err != nil {
		t.Fatal(err)
	}
	back, song, said := m.Song("back")
	if len(said) > 0 || song.BPM != d.BPM || len(back) != len(pats) {
		t.Errorf("%d pattern(s) at %d bpm, said %q", len(back), song.BPM, said)
	}
	if bars := song.Bars; bars[0][1] != bars[2][1] || bars[0][1] != bars[3][1] || bars[0][0] != bars[2][0] || bars[1][0] != bars[0][0] || bars[3][3] != "" {
		t.Errorf("bars %q, want the hook's and the beat's patterns again where the song repeats them", bars)
	}
	want, got := sounding(t, d, lookup(pats...)), sounding(t, song, lookup(back...))
	if !slices.Equal(got, want) {
		t.Errorf("loaded back\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestAGlideRoundTripsThroughMIDI: a song's glides written as pitch bend load
// back with their bend and bend_at, up, down and at full reach, each lane's
// range the least of 2, 12 and 24 semitones that holds its largest, and each
// gliding lane on a channel no other plays, as a channel bends all it sounds
func TestAGlideRoundTripsThroughMIDI(t *testing.T) {
	pats := []*PatternDef{
		{Name: "sing", Steps: 16, Role: "melody", Track: []TrackDef{
			{Instr: "lead", Event: []StepDef{{Pos: 0, Vel: 0.8, Deg: 2, Dur: 4, Bend: 2, BendAt: 0.75}, {Pos: 4, Vel: 0.7, Deg: 4, Dur: 4, Bend: -1.5},
				{Pos: 8, Vel: 0.6, Deg: 1, Dur: 2}, {Pos: 12, Vel: 0.9, Dur: 4, Bend: 0.5, BendAt: 0.33}}},
			{Instr: "lead", Desc: "under", Event: []StepDef{{Pos: 0, Vel: 0.5, Deg: -3, Dur: 16}}}}},
		{Name: "dive", Steps: 16, Role: "bass", Track: []TrackDef{
			{Instr: "bass", Event: []StepDef{{Pos: 0, Vel: 1, Oct: -1, Dur: 8, Bend: -12, BendAt: 0.5}, {Pos: 8, Vel: 0.9, Oct: -1, Dur: 8, Bend: 7}}}}},
		{Name: "swoop", Steps: 16, Role: "fill", Track: []TrackDef{
			{Instr: "pad", Event: []StepDef{{Pos: 0, Vel: 0.6, Dur: 16, Bend: 24, BendAt: 0.1}}}}},
	}
	d := &SongDef{Name: "glides", BPM: 100, Root: 45, Scale: "minor", Bars: [][]string{{"", "sing", "swoop", "dive"}, {"", "sing", "", "dive"}}}
	var b bytes.Buffer
	if err := WriteSongMIDI(&b, d, lookup(pats...)); err != nil {
		t.Fatal(err)
	}
	// RPN 0's data entry, each channel's range: a bend sensitivity of span semitones
	var spans []int
	for f, i := b.Bytes(), 4; i+2 < b.Len(); i++ {
		if f[i]&0xF0 == 0xB0 && f[i+1] == 6 && f[i-4] == f[i] && f[i-3] == 100 && f[i-2] == 0 {
			spans = append(spans, int(f[i+2]))
		}
	}
	m, err := ReadMIDI(&b)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(spans)
	if !slices.Equal(spans, []int{2, 12, 24}) {
		t.Errorf("bend ranges %v, want 2, 12 and 24", spans)
	}
	for _, p := range m.parts {
		if slices.ContainsFunc(p.notes, func(n midiNote) bool { return n.bend != 0 }) {
			for _, q := range m.parts {
				if q.track != p.track && q.notes[0].ch == p.notes[0].ch {
					t.Errorf("track %d glides on channel %d, which track %d plays too", p.track, p.notes[0].ch, q.track)
				}
			}
		}
	}
	back, song, said := m.Song("back")
	if len(said) > 0 {
		t.Errorf("said %q", said)
	}
	want, got := sounding(t, d, lookup(pats...)), sounding(t, song, lookup(back...))
	if !slices.Equal(got, want) {
		t.Errorf("loaded back\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestMIDIReadsMetersInTheirSteps: 3/4 and 6/8 bars load as patterns of 12 steps
// and 3/8 of 6, each in its meter, the song in its first and each bar of
// another naming it, a tempo change naming the bar's tempo; a meter no pattern
// holds loads in 4/4 and is said
func TestMIDIReadsMetersInTheirSteps(t *testing.T) {
	file := smf(2)
	file = append(file, track(
		0x00, 0xFF, 0x51, 3, 0x07, 0xA1, 0x20, // 120 bpm
		0x00, 0xFF, 0x58, 4, 3, 2, 24, 8, // 3/4, two bars
		0x96, 0x40, 0xFF, 0x58, 4, 6, 3, 12, 8, // 6/8 at 2880
		0x8B, 0x20, 0xFF, 0x58, 4, 3, 3, 12, 8, // 3/8 at 4320
		0x00, 0xFF, 0x51, 3, 0x0F, 0x42, 0x40, // 60 bpm
		0x85, 0x50, 0xFF, 0x58, 4, 5, 5, 3, 8, // 5/32 at 5040
		0x00, 0xFF, 0x2F, 0)...)
	file = append(file, track(
		0x00, 0x90, 60, 100, 0x78, 0x80, 60, 0, // a sixteenth at each bar line
		0x8A, 0x28, 0x90, 60, 100, 0x78, 0x80, 60, 0,
		0x8A, 0x28, 0x90, 60, 100, 0x78, 0x80, 60, 0,
		0x8A, 0x28, 0x90, 60, 100, 0x78, 0x80, 60, 0,
		0x84, 0x58, 0x90, 60, 100, 0x78, 0x80, 60, 0,
		0x00, 0xFF, 0x2F, 0)...)
	m, err := ReadMIDI(bytes.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	pats, song, said := m.Song("odd")
	var steps []int
	for b := range song.Bars {
		steps = append(steps, lookup(pats...)(song.Patterns(b)[RipPartSlots[RipMelody]]).Steps)
	}
	var words []string
	for _, bar := range song.Bars {
		words = append(words, strings.Join(bar[:len(bar)-MusicSlots], " "))
	}
	if !slices.Equal(words, []string{"", "", "6/8", "3/8 60bpm", "4/4"}) {
		t.Errorf("bar words %q, want the meter and tempo each bar changes to", words)
	}
	if !slices.Equal(steps, []int{12, 12, 12, 6, 16}) || len(pats) != 4 {
		t.Errorf("bars of %v steps in %d pattern(s), want 12 12 12 6 16 in 4", steps, len(pats))
	}
	var meters []string
	for _, p := range pats {
		meters = append(meters, p.Meter)
	}
	if song.Meter != "3/4" || !slices.Equal(meters, []string{"3/4", "6/8", "3/8", ""}) {
		t.Errorf("song in %q, patterns in %q", song.Meter, meters)
	}
	if w := "5/32 from bar 4 is no meter a pattern holds at 120 bpm: read in bars of 4/4"; !slices.Contains(said, w) || len(said) != 1 {
		t.Errorf("said %q, want %q alone", said, w)
	}
}

// TestAMeterRoundTripsThroughTheDocumentAndMIDI: a pattern's and a song's meter
// survive the pattern document, and a MIDI file of either loads back in it, the
// bars its own and the score's rip in it
func TestAMeterRoundTripsThroughTheDocumentAndMIDI(t *testing.T) {
	p := &PatternDef{Name: "jig", Steps: 24, Meter: "6/8", Role: "bass", Track: []TrackDef{{Instr: "bass", Event: []StepDef{
		{Pos: 0, Vel: 1, Oct: -1, Dur: 2}, {Pos: 6, Vel: 0.6, Deg: 4, Oct: -1}, {Pos: 12, Vel: 0.9, Oct: -1}, {Pos: 23, Vel: 0.5, Deg: 4, Oct: -1}}}}}
	d := &SongDef{Name: "waltz", BPM: 150, Meter: "3/4", Root: 40, Scale: "major", Bars: [][]string{{"", "", "", "jig"}, {"", "", "", "jig"}}}
	doc, err := MarshalPatternFile(&PatternSpecFile{Pattern: []*PatternDef{p}, Song: []*SongDef{d}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := LoadPatternFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := f.Pattern[0].Pattern(); err != nil || got.Meter != (Meter{6, 8}) || f.Song[0].Signature() != (Meter{3, 4}) {
		t.Errorf("the document read back %v (%v) and %q", got, err, f.Song[0].Meter)
	}
	for _, c := range []struct {
		write   func(*bytes.Buffer) error
		meter   string
		bars    int
		ripBars int
	}{
		{func(b *bytes.Buffer) error { return WritePatternMIDI(b, p, 100, 40, ScaleMajor, nil) }, "6/8", 2, 2},
		{func(b *bytes.Buffer) error { return WriteSongMIDI(b, d, lookup(p)) }, "3/4", 2, 2},
	} {
		var b bytes.Buffer
		if err := c.write(&b); err != nil {
			t.Fatal(err)
		}
		m, err := ReadMIDI(&b)
		if err != nil {
			t.Fatal(err)
		}
		pats, song, said := m.Song("back")
		r, _ := m.Rip()
		if m.Meter.String() != c.meter || song.Meter != c.meter || pats[0].Meter != c.meter || len(song.Bars) != c.bars || pats[0].Steps != 12 || len(said) > 0 {
			t.Errorf("%s: read %s, the song in %q of %d bar(s), a pattern in %q of %d steps, said %q", c.meter, m.Meter, song.Meter, len(song.Bars), pats[0].Meter, pats[0].Steps, said)
		}
		if r.Meter.String() != c.meter || len(r.Bars) != c.ripBars || len(r.Beats) != r.Meter.Beats()*c.ripBars+1 {
			t.Errorf("%s: the score rips in %s, %d bar(s) on %d beats", c.meter, r.Meter, len(r.Bars), len(r.Beats))
		}
	}
}

// TestAScoreInThreeCompressesToItsPatterns: an abcabd phrase of 3/4 bars,
// written and read back, rips one file bar a bar, so compress groups its
// bars into the four patterns it was written from and not the nine 4/4 bars
// cutting across them made
func TestAScoreInThreeCompressesToItsPatterns(t *testing.T) {
	var pats []*PatternDef
	for i, degs := range [][3]int{{0, 2, 4}, {4, 3, 2}, {1, 3, 5}, {5, 4, 0}} {
		var ev []StepDef
		for k, d := range degs {
			ev = append(ev, StepDef{Pos: 4 * k, Vel: 0.8, Deg: d, Oct: 1, Dur: 4})
		}
		pats = append(pats, &PatternDef{Name: string(rune('a' + i)), Steps: 12, Meter: "3/4", Role: "melody", Track: []TrackDef{{Instr: "lead", Event: ev}}})
	}
	d := &SongDef{Name: "phrase", BPM: 120, Meter: "3/4", Root: 40, Scale: "major"}
	for range 6 {
		for _, c := range "abcabd" {
			d.Bars = append(d.Bars, []string{"", string(c), "", ""})
		}
	}
	var b bytes.Buffer
	if err := WriteSongMIDI(&b, d, lookup(pats...)); err != nil {
		t.Fatal(err)
	}
	m, err := ReadMIDI(&b)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := m.Rip()
	groups := r.Cluster(RipMelody, 0.05)
	slices.Sort(groups)
	if r.Meter != (Meter{3, 4}) || len(r.Bars) != len(d.Bars) || len(slices.Compact(groups)) != 4 {
		t.Errorf("ripped in %s, %d bar(s) for %d, %d pattern(s)", r.Meter, len(r.Bars), len(d.Bars), len(slices.Compact(groups)))
	}
}

// TestMIDINotesEndAtTheirKeysUnderThePedal: a note reads as played, ending at its
// key's release whatever the sustain pedal holds, as a score writes it and as
// the corpus labels the loops it rings on
func TestMIDINotesEndAtTheirKeysUnderThePedal(t *testing.T) {
	file := append(smf(1), track(
		0x00, 0x90, 60, 100, 0x00, 0xB0, 64, 127, // C4 and the pedal down
		0x81, 0x70, 0x80, 60, 0, 0x00, 0x90, 64, 100, // C4's key up at 0.25 s, E4
		0x81, 0x70, 0x80, 64, 0, // its key up at 0.5 s
		0x83, 0x60, 0xB0, 64, 0, // the pedal up at 1 s
		0x83, 0x60, 0xFF, 0x2F, 0)...)
	m, err := ReadMIDI(bytes.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range m.Notes {
		got = append(got, fmt.Sprintf("%d %.2f-%.2f", n.MIDI, n.Start, n.End))
	}
	slices.Sort(got)
	if want := []string{"60 0.00-0.25", "64 0.25-0.50"}; !slices.Equal(got, want) {
		t.Errorf("notes %q, want %q", got, want)
	}
}

// TestMIDIProgramsReadAsTheirVoice: every program reads as one shipped voice, of
// its family's row; a voice's own program and a kit piece's keys read back as it
func TestMIDIProgramsReadAsTheirVoice(t *testing.T) {
	for program := range 128 {
		rows := 0
		for v := InstrBass; v < InstrumentCount; v++ {
			if slices.Contains(midiInstruments[v].families, byte(program/8)) {
				rows++
			}
		}
		if rows != 1 {
			t.Errorf("program %d in %d rows, want 1", program, rows)
		}
	}
	for v := InstrBass; v < InstrumentCount; v++ {
		if got := midiVoice(midiProgram(v)); got != v {
			t.Errorf("%s writes program %d, read back as %s", v, midiProgram(v), got)
		}
	}
	for d := InstrKick; d <= InstrClap; d++ {
		for _, k := range midiInstruments[d].keys {
			if got := midiDrum(k, InstrClap+1); got != d {
				t.Errorf("%s's key %d read back as %d", d, k, got)
			}
		}
	}
}

// TestMIDIWritesAnAddedVoiceOnItsNearest: a voice no row writes is written on the
// program of the shipped voice nearest it, of its wave first, then by its genes
func TestMIDIWritesAnAddedVoiceOnItsNearest(t *testing.T) {
	t.Cleanup(ResetRegistries)
	ResetRegistries()
	for _, v := range []*VoiceDef{
		{Name: "piano", Wave: "modal", Partials: 16, Decay: 1.5, Release: 0.5, Level: 0.8},
		{Name: "bass", Wave: "modal", Partials: 4, Decay: 0.3, Sustain: 0.6, Release: 0.1, Level: 1.2},
		{Name: "pad", Wave: "saw", Attack: 0.8, Sustain: 1, Release: 2, Level: 0.5},
		{Name: "lead", Wave: "saw", Attack: 0.005, Sustain: 0.7, Release: 0.1, Level: 0.6},
		{Name: "pluck", Wave: "string", Decay: 0.8, Release: 0.3, Level: 0.7},
	} {
		if _, err := RegisterVoice(v); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		v    *VoiceDef
		near InstrumentType
	}{
		{&VoiceDef{Name: "keys", Wave: "modal", Partials: 14, Decay: 1.2, Release: 0.6, Level: 0.8}, InstrPiano},
		{&VoiceDef{Name: "sub", Wave: "modal", Partials: 3, Decay: 0.4, Sustain: 0.5, Release: 0.1, Level: 1.1}, InstrBass},
		{&VoiceDef{Name: "buzz", Wave: "saw", Attack: 0.01, Sustain: 0.8, Release: 0.15, Level: 0.6}, InstrLead},
		{&VoiceDef{Name: "harp", Wave: "string", Attack: 0.8, Sustain: 1, Release: 2, Level: 0.5}, InstrPluck}, // a pad's genes
	} {
		id, err := RegisterVoice(c.v)
		if err != nil {
			t.Fatal(err)
		}
		if got := midiProgram(id); got != midiInstruments[c.near].program {
			t.Errorf("%s written on program %d, want the %s's %d", c.v.Name, got, c.near, midiInstruments[c.near].program)
		}
	}
}
