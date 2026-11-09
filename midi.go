package audio

import (
	"cmp"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"math/bits"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// midiDivision is the ticks per quarter note the writers write, a tick under
// two milliseconds at any tempo the sequencer plays
const midiDivision = 480

const (
	midiDrums        = 9      // the channel General MIDI keeps for percussion, which has no pitch
	midiChannels     = 15     // the channels besides the drums'
	midiDefaultTempo = 500000 // microseconds a quarter note before a file's first tempo, 120 bpm
	midiBendTicks    = 10     // ticks between a written glide's bends, a hundredth of a second at 120 bpm
	midiBendRange    = 2.0    // General MIDI's bend range in semitones until a file sets one
)

// midiInstruments is each shipped instrument as General MIDI writes and reads it:
// a drum's keys on the drum channel, the first the one written; a voice's
// program, written, and the families of eight programs read back as it, so a
// program no row writes plays on the shipped voice of its family. A ride,
// tambourine or shaker reads as the hihat, the line stapes plays it on.
var midiInstruments = [InstrumentCount]struct {
	keys     []byte
	program  byte
	families []byte
}{
	InstrKick:  {keys: []byte{36, 35}},
	InstrSnare: {keys: []byte{38, 40, 37}},                             // head, rim, cross-stick
	InstrHihat: {keys: []byte{42, 44, 46, 22, 26, 51, 59, 53, 54, 82}}, // closed, pedal, open, edges, ride, tambourine, shaker
	InstrClap:  {keys: []byte{39}},
	InstrBass:  {program: 32, families: []byte{4}},                   // acoustic bass: the basses
	InstrPiano: {program: 0, families: []byte{0, 1, 14}},             // grand piano: pianos, chromatic and pitched percussion
	InstrPad:   {program: 89, families: []byte{2, 5, 6, 11, 12, 15}}, // warm pad: organs, strings, ensembles, pads, effects
	InstrLead:  {program: 81, families: []byte{7, 8, 9, 10}},         // sawtooth lead: brass, reeds, pipes, synth leads
	InstrPluck: {program: 24, families: []byte{3, 13}},               // nylon guitar: guitars, plucked ethnic
}

// midiVoice is the shipped voice a program plays on, its family's row
func midiVoice(program byte) InstrumentType {
	for i := InstrBass; i < InstrumentCount; i++ {
		if slices.Contains(midiInstruments[i].families, program/8) {
			return i
		}
	}
	return InstrPiano
}

// midiDrum is the kit piece under pieces a drum key plays, -1 for none
func midiDrum(key byte, pieces InstrumentType) InstrumentType {
	for d := InstrKick; d < pieces; d++ {
		if slices.Contains(midiInstruments[d].keys, key) {
			return d
		}
	}
	return -1
}

// midiProgram is the program a tonal instrument is written with: a shipped
// voice's own, and for one a document added the shipped voice's nearest it, of
// its wave first, then by its genes
func midiProgram(instr InstrumentType) byte {
	if instr < InstrumentCount {
		return midiInstruments[instr].program
	}
	voiceMu.RLock()
	v := &VoiceDef{}
	if int(instr) < len(voiceDefs) && voiceDefs[instr] != nil {
		v = voiceDefs[instr]
	}
	shipped := slices.Clone(voiceDefs[InstrBass:InstrumentCount])
	voiceMu.RUnlock()
	best, far := InstrLead, math.Inf(1)
	genes := v.Genes()
	for i, sv := range shipped {
		if sv == nil {
			continue
		}
		var d float64
		if cmp.Or(sv.Wave, "sine") != cmp.Or(v.Wave, "sine") {
			d = float64(VoiceGeneCount) // a wave apart outweighs every gene
		}
		for k, g := range sv.Genes() {
			d += (g - genes[k]) * (g - genes[k])
		}
		if d < far {
			best, far = InstrBass+InstrumentType(i), d
		}
	}
	return midiInstruments[best].program
}

// midiNote is a note of a track in ticks, before the tempo map, with the program
// its channel was set to at its start, -1 where none was, and its glide as a
// step's (Step.Bend)
type midiNote struct {
	on, off      int64
	ch, key, vel byte
	prog         int
	bend, bendAt float64
}

// plays is the program n sounds on: the one set, General MIDI's first before any is
func (n midiNote) plays() byte { return byte(max(n.prog, 0)) }

// midiPart is one track's notes on one channel: what ReadMIDI maps to a line
type midiPart struct {
	name  string
	track int
	notes []midiNote
}

// midiTempo is a tempo change: from tick on, sec seconds into the file, each
// tick lasts perTick seconds
type midiTempo struct {
	tick         int64
	sec, perTick float64
}

// midiMeter is a time signature from tick on: num beats a bar of 1/den notes
type midiMeter struct {
	tick     int64
	num, den int
}

// midiKey is a key signature from tick on: its sharps, flats negative, and
// whether it signs the minor
type midiKey struct {
	tick  int64
	sf    int
	minor bool
}

// midiChange is a program change on a channel from tick on
type midiChange struct {
	tick        int64
	ch, program byte
}

// midiTrack is what a track chunk holds: its notes by channel, and the tempo
// changes, signatures and program changes in it
type midiTrack struct {
	parts    []midiPart
	tempos   [][2]int64 // tick, microseconds a quarter note
	meters   []midiMeter
	keys     []midiKey
	programs []midiChange
}

// MIDIScore is a Standard MIDI File as ReadMIDI reads it: the transcript in
// seconds under its tempo map, and what of its grid a rip needs.
type MIDIScore struct {
	Notes   []RipNote
	Hits    []RipHit
	BPM     float64   // quarter notes a minute at the start, 120 when the file names none
	Changes int       // tempo changes after the start
	Meter   Meter     // the time signature the music starts in, 4/4 when the file names none
	Meters  int       // time signatures changed to after the first bar
	Left    int       // parts past the three lines, left out of them
	Further []RipNote // those parts' notes, as an under line's, for what wants the whole arrangement
	// Voices are the shipped voices the melody, under and bass parts' programs
	// play on (midiVoice), General MIDI's piano where a file sets none
	Voices [3]InstrumentType
	// Struck are the drum channel's other strikes (a tom, a crash, a clap),
	// Drum -1: onsets of no line
	Struck []RipHit

	clock     []midiTempo
	division  int
	line, end int64 // the tick that time signature starts on, and the last note's end
	parts     []midiPart
	meters    []midiMeter
	key       *midiKey // the first key signature, nil when the file signs none
}

// seconds is where a tick falls under the tempo map, a tick before the file at
// the tempo it starts with
func (m *MIDIScore) seconds(tick float64) float64 {
	i := sort.Search(len(m.clock), func(i int) bool { return float64(m.clock[i].tick) > max(tick, 0) }) - 1
	return m.clock[i].sec + (tick-float64(m.clock[i].tick))*m.clock[i].perTick
}

// ticks is seconds' tick, seconds inverted
func (m *MIDIScore) ticks(sec float64) float64 {
	i := sort.Search(len(m.clock), func(i int) bool { return m.clock[i].sec > max(sec, 0) }) - 1
	return float64(m.clock[i].tick) + (sec-m.clock[i].sec)/m.clock[i].perTick
}

// ErrMIDIUnlined is ReadMIDI's refusal of a file with no line to read: no
// notes, and no kick, snare or hihat
var ErrMIDIUnlined = errors.New("midi: no notes, and no kick, snare or hihat")

// ReadMIDI reads a Standard MIDI File of type 0 or 1 as a transcript in seconds
// under its tempo map, the drum channel's kicks, snares and hats as hits. A
// track named melody, under or bass is that line; of the other parts (a track's
// notes on one channel), the lowest is the bass, whole, when its median is under
// E3, and the rest the melody, then the under; parts past those are left out.
func ReadMIDI(r io.Reader) (*MIDIScore, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	be := binary.BigEndian
	if len(data) < 14 || string(data[:4]) != "MThd" {
		return nil, errors.New("midi: not a Standard MIDI File")
	}
	size := int(be.Uint32(data[4:]))
	if size < 6 || size > len(data)-8 {
		return nil, errors.New("midi: short header")
	}
	format, tracks, division := be.Uint16(data[8:]), int(be.Uint16(data[10:])), int(be.Uint16(data[12:]))
	switch {
	case format > 1:
		return nil, fmt.Errorf("midi: type %d, want 0 or 1", format)
	case division&0x8000 != 0 || division == 0:
		return nil, errors.New("midi: SMPTE time division unsupported, want ticks per quarter note")
	}
	var all midiTrack
	for off, read := 8+size, 0; off+8 <= len(data) && read < tracks; {
		id, length := string(data[off:off+4]), int(be.Uint32(data[off+4:]))
		body := data[off+8:]
		length = min(length, len(body))
		off += 8 + length
		if id != "MTrk" {
			continue
		}
		t, err := readMIDITrack(body[:length], read)
		if err != nil {
			return nil, fmt.Errorf("midi: track %d: %w", read+1, err)
		}
		all.parts, all.tempos, all.meters = append(all.parts, t.parts...), append(all.tempos, t.tempos...), append(all.meters, t.meters...)
		all.keys, all.programs = append(all.keys, t.keys...), append(all.programs, t.programs...)
		read++
	}
	slices.SortStableFunc(all.tempos, func(a, b [2]int64) int { return cmp.Compare(a[0], b[0]) })
	m := &MIDIScore{division: division, clock: []midiTempo{{perTick: midiDefaultTempo / 1e6 / float64(division)}}, parts: all.parts, meters: all.meters}
	for _, c := range all.tempos {
		last := m.clock[len(m.clock)-1]
		perTick := float64(c[1]) / 1e6 / float64(division)
		if c[0] > 0 && perTick != last.perTick {
			m.Changes++
		}
		m.clock = append(m.clock, midiTempo{tick: c[0], sec: last.sec + float64(c[0]-last.tick)*last.perTick, perTick: perTick})
	}
	start := m.clock[sort.Search(len(m.clock), func(i int) bool { return m.clock[i].tick > 0 })-1]
	m.BPM = 60 / start.perTick / float64(division)
	m.line, m.Meter = firstBar(m.meters, division)
	for _, mt := range m.meters {
		if mt.tick > m.line && meterOf(mt.num, mt.den) != m.Meter {
			m.Meters++
		}
	}
	if len(all.keys) > 0 {
		k := slices.MinFunc(all.keys, func(a, b midiKey) int { return cmp.Compare(a.tick, b.tick) })
		m.key = &k
	}
	// a channel plays the program last set on it in any track
	var programs [16][]midiChange
	slices.SortStableFunc(all.programs, func(a, b midiChange) int { return cmp.Compare(a.tick, b.tick) })
	for _, c := range all.programs {
		programs[c.ch] = append(programs[c.ch], c)
	}
	for _, p := range m.parts {
		for i, n := range p.notes {
			cs := programs[n.ch]
			p.notes[i].prog = -1
			if k := sort.Search(len(cs), func(k int) bool { return cs[k].tick > n.on }); k > 0 {
				p.notes[i].prog = int(cs[k-1].program)
			}
		}
	}
	seconds := func(tick int64) float64 { return m.seconds(float64(tick)) }
	var tonal []midiPart
	for _, p := range m.parts {
		for _, n := range p.notes {
			m.end = max(m.end, n.off, n.on+1)
		}
		if p.notes[0].ch != midiDrums {
			tonal = append(tonal, p)
			continue
		}
		for _, n := range p.notes {
			h := RipHit{Start: seconds(n.on), Drum: midiDrum(n.key, InstrClap), Vel: float64(n.vel) / 127}
			if h.Drum >= 0 {
				m.Hits = append(m.Hits, h)
			} else {
				m.Struck = append(m.Struck, h)
			}
		}
	}
	m.Voices = [3]InstrumentType{midiVoice(0), midiVoice(0), midiVoice(0)}
	for i, line := range midiLinesOf(tonal) {
		if line < 0 {
			m.Left++
			for _, n := range tonal[i].notes {
				m.Further = append(m.Further, RipNote{Start: seconds(n.on), End: seconds(n.off), MIDI: int(n.key), Vel: float64(n.vel) / 127, Second: true, Bright: RipUnmeasured, Bend: n.bend, BendAt: n.bendAt, Program: n.prog})
			}
			continue
		}
		m.Voices[line] = midiVoice(tonal[i].notes[0].plays())
		for _, n := range tonal[i].notes {
			m.Notes = append(m.Notes, RipNote{Start: seconds(n.on), End: seconds(n.off), MIDI: int(n.key), Vel: float64(n.vel) / 127, Bass: line == 2, Second: line == 1, Bright: RipUnmeasured, Bend: n.bend, BendAt: n.bendAt, Program: n.prog})
		}
	}
	if len(m.Notes) == 0 && len(m.Hits) == 0 {
		return nil, ErrMIDIUnlined
	}
	slices.SortStableFunc(m.Notes, func(a, b RipNote) int { return cmpFloat(a.Start, b.Start) })
	slices.SortStableFunc(m.Hits, func(a, b RipHit) int { return cmpFloat(a.Start, b.Start) })
	slices.SortStableFunc(m.Struck, func(a, b RipHit) int { return cmpFloat(a.Start, b.Start) })
	return m, nil
}

// firstBar is where the first time signature that holds for more than a bar
// starts, and that signature: a bar of its own before it, as a pickup is
// written, is not the music's first. A file with none is in 4/4 from its start.
func firstBar(meters []midiMeter, division int) (int64, Meter) {
	slices.SortStableFunc(meters, func(a, b midiMeter) int { return cmp.Compare(a.tick, b.tick) })
	if len(meters) == 0 || meters[0].tick > 0 {
		meters = append([]midiMeter{{num: 4, den: 4}}, meters...)
	}
	for i, mt := range meters {
		bar := int64(4 * division * mt.num / mt.den)
		if i+1 == len(meters) || mt.tick+bar < meters[i+1].tick {
			return mt.tick, meterOf(mt.num, mt.den)
		}
	}
	panic("unreachable: the last signature holds to the end")
}

// foldMeter is the meter num/den reads in at fold times the file's tempo: a bar
// as long, its note fold times as many steps, so 3/8 at twice its tempo is 3/4;
// false when no meter the grid plays holds that bar
func foldMeter(num, den int, fold float64) (Meter, bool) {
	n, d := float64(num), float64(den)/fold
	for d > StepsPerBar && math.Mod(n, 2) == 0 {
		n, d = n/2, d/2
	}
	for d < 1 {
		n, d = n*2, d*2
	}
	m := Meter{int(n), int(d)}
	if float64(m.Num) != n || float64(m.Den) != d || m.Num < 1 || m.check() != nil {
		return Meter{}, false
	}
	return meterOf(m.Num, m.Den), true
}

// Rip is the score as a rip of it: notes quantized on its own ticks, its first tempo
// folded by octaves into the sequencer's range with its first meter read at that
// tempo (foldMeter; 4/4 when the grid plays none), bars from where that signature
// starts with a pickup bar for notes before it, and the key keyOf its pitch classes.
// A note outside the rip's range is dropped, and counted.
func (m *MIDIScore) Rip() (*Rip, int) {
	const fine = 64 // the grid's units a tick, so a folded beat is whole
	at := func(sec float64) int { return int(math.Round(m.ticks(sec) * fine)) }
	quarter, bpm := float64(m.division*fine), m.BPM
	for bpm < MinBPM {
		quarter, bpm = quarter/2, bpm*2
	}
	for bpm > MaxBPM {
		quarter, bpm = quarter*2, bpm/2
	}
	meter, _ := foldMeter(m.Meter.Beats(), m.Meter.norm().Den, float64(m.division*fine)/quarter)
	beat := quarter * float64(meter.Beat()) / StepsPerBeat
	r := &Rip{BPM: int(math.Round(bpm)), Meter: meter}
	var c, b [12]float64
	var notes []ripNote
	dropped := 0
	for _, n := range m.Notes {
		if n.MIDI < RipMinMIDI || n.MIDI >= RipMaxMIDI {
			dropped++
			continue
		}
		r.Notes = append(r.Notes, n)
		notes = append(notes, ripNote{start: at(n.Start), end: at(n.End), midi: n.MIDI, vel: n.Vel, bend: n.Bend, bendAt: n.BendAt})
		c[n.MIDI%12] += n.End - n.Start
		if n.MIDI < RipBassTop {
			b[n.MIDI%12] += n.End - n.Start
		}
	}
	r.Root, r.Scale = keyOf(c, b)
	var hits [InstrClap][]ripHit
	for _, h := range m.Hits {
		hits[h.Drum] = append(hits[h.Drum], ripHit{frame: at(h.Start), strength: h.Vel})
	}
	per := meter.Beats()
	bars := max(int(math.Ceil(float64(m.end-m.line)*fine/(float64(per)*beat))), 1)
	g := ripGrid{m: meter}
	for i := 0; i <= per*bars; i += per {
		g.lines = append(g.lines, int(m.line)*fine+int(math.Round(float64(i)*beat)))
	}
	g = g.pickup(notes, hits)
	r.Bars = make([]RipBar, g.bars())
	r.Beats = make([]float64, per*g.bars()+1)
	for i := range r.Beats {
		r.Beats[i] = m.seconds(float64(g.beat(i)) / fine)
	}
	r.Downbeat = r.Beats[0]
	for d, hs := range hits {
		for _, h := range hs {
			g.hit(r, InstrumentType(d), h.frame, h.strength)
		}
	}
	for i, n := range notes {
		part := func(b *RipBar) *[]Step { return &b.Melody }
		switch {
		case r.Notes[i].Bass:
			part = func(b *RipBar) *[]Step { return &b.Bass }
		case r.Notes[i].Second:
			part = func(b *RipBar) *[]Step { return &b.Under }
		}
		g.place(r, n, part)
	}
	r.Progression = make([]int, g.bars())
	for bar := range r.Progression {
		lo, hi := r.Beats[per*bar], r.Beats[per*(bar+1)]
		var chroma [12]float64
		for _, n := range r.Notes {
			chroma[n.MIDI%12] += max(min(n.End, hi)-max(n.Start, lo), 0)
		}
		r.Progression[bar] = chordOf(chroma, r.Root, r.Scale)
	}
	return r, dropped
}

// Beats is the score's beats and downbeats under its time signatures and tempo
// map, placed on a recording by at (a truth's Agreement.At): each signature's
// beats and bars from where it starts, cut where the next starts, from the beat
// and the bar holding the first note or hit to the last's end; a downbeat from
// the first full bar (firstBar) on, as a pickup's line is none
func (m *MIDIScore) Beats(at func(float64) float64) (beats, downbeats []float64) {
	first := float64(m.end)
	for _, p := range m.parts {
		for _, n := range p.notes {
			first = min(first, float64(n.on))
		}
	}
	meters := m.meters
	if len(meters) == 0 || meters[0].tick > 0 {
		meters = append([]midiMeter{{num: 4, den: 4}}, meters...)
	}
	for i, mt := range meters {
		end := m.end
		if i+1 < len(meters) {
			end = min(end, meters[i+1].tick)
		}
		beat := 4 * float64(m.division) / float64(mt.den)
		for k := 0; float64(mt.tick)+float64(k)*beat < float64(end); k++ {
			t := float64(mt.tick) + float64(k)*beat
			if t+beat > first {
				beats = append(beats, at(m.seconds(t)))
			}
			if k%mt.num == 0 && t >= float64(m.line) && t+float64(mt.num)*beat > first {
				downbeats = append(downbeats, at(m.seconds(t)))
			}
		}
	}
	return beats, downbeats
}

// Song reads the score as patterns and a song under name: a pattern a part a
// bar of its time signature's steps at the folded tempo (foldMeter), the drum
// channel the beat, a bass program's parts the bass and the rest the melody, a
// track each, glides kept, named as compress names them; the song plays them in
// order, in the key signed or its notes'. It says what it could not keep.
func (m *MIDIScore) Song(name string) ([]*PatternDef, *SongDef, []string) {
	var said []string
	// a step is the file's sixteenth, by the octaves that bring its first tempo
	// into the sequencer's range as the rip folds its beat
	bpm, perStep := m.BPM, float64(m.division)/StepsPerBeat
	for bpm < MinBPM {
		bpm, perStep = bpm*2, perStep/2
	}
	for bpm > MaxBPM {
		bpm, perStep = bpm/2, perStep*2
	}
	if f := float64(m.division) / StepsPerBeat / perStep; f > 1 {
		said = append(said, fmt.Sprintf("its %.0f bpm played at %.0f, a sixteenth %.0f steps", m.BPM, bpm, f))
	} else if f < 1 {
		said = append(said, fmt.Sprintf("its %.0f bpm played at %.0f, a sixteenth half a step or less", m.BPM, bpm))
	}
	at := func(tick int64) int { return int(math.Round(float64(tick) / perStep)) }

	// a line is a track's notes on one channel in one program, a track of the
	// shipped voice the program plays on; a drum's, every drum part's of it
	type line struct {
		part        RipPart
		instr, desc string
		notes       []midiNote
	}
	var lines []line
	var drums [InstrClap + 1][]midiNote
	unkit := map[byte]int{}                  // the hits of a drum key no piece plays
	nearest := map[InstrumentType][]string{} // the programs no row writes, by the voice they play on
	for _, p := range m.parts {
		if p.notes[0].ch == midiDrums {
			for _, n := range p.notes {
				if d := midiDrum(n.key, InstrClap+1); d >= 0 {
					drums[d] = append(drums[d], n)
				} else {
					unkit[n.key]++
				}
			}
			continue
		}
		of := map[byte]int{}
		for _, n := range p.notes {
			prog := n.plays()
			i, ok := of[prog]
			if !ok {
				v := midiVoice(prog)
				if word := strconv.Itoa(int(prog)); prog != midiInstruments[v].program && !slices.Contains(nearest[v], word) {
					nearest[v] = append(nearest[v], word)
				}
				part, desc := RipMelody, strings.TrimSpace(p.name)
				if v == InstrBass {
					part = RipBass
				}
				if desc == v.String() {
					desc = ""
				}
				i, of[prog] = len(lines), len(lines)
				lines = append(lines, line{part: part, instr: v.String(), desc: desc})
			}
			lines[i].notes = append(lines[i].notes, n)
		}
	}
	for d, ns := range drums {
		if len(ns) > 0 {
			lines = append(lines, line{part: RipBeat, instr: InstrumentType(d).String(), notes: ns})
		}
	}
	var tracks [RipParts]int
	left := 0
	lines = slices.DeleteFunc(lines, func(l line) bool {
		tracks[l.part]++
		if tracks[l.part] > MaxPatternTracks {
			left++
		}
		return tracks[l.part] > MaxPatternTracks
	})

	// the bars: each signature's from where it starts, in the meter the folded
	// tempo reads it in, cut where the next starts within one; a pickup shorter
	// than the music's first bar is one of its bars, rests ahead of it
	type bar struct {
		start, steps int
		meter        Meter
	}
	var bars []bar
	end := 0
	for _, l := range lines {
		for _, n := range l.notes {
			end = max(end, at(n.on)+1)
		}
	}
	meters := m.meters
	if len(meters) == 0 || meters[0].tick > 0 {
		meters = append([]midiMeter{{num: 4, den: 4}}, meters...)
	}
	fold := float64(m.division) / StepsPerBeat / perStep
	main, ok := foldMeter(m.Meter.Beats(), m.Meter.norm().Den, fold)
	for i, start, told := 0, 0, -1; start < end; {
		for i+1 < len(meters) && at(meters[i+1].tick) <= start {
			i++
		}
		mt := meters[i]
		bm, kept := foldMeter(mt.num, mt.den, fold)
		if told != i {
			told = i
			switch {
			case !kept:
				said = append(said, fmt.Sprintf("%d/%d from bar %d is no meter a pattern holds at %.0f bpm: read in bars of 4/4", mt.num, mt.den, len(bars), bpm))
			case bm != meterOf(mt.num, mt.den):
				said = append(said, fmt.Sprintf("%d/%d from bar %d is %s at %.0f bpm", mt.num, mt.den, len(bars), bm, bpm))
			}
		}
		steps := bm.Steps()
		if i+1 < len(meters) {
			if next := at(meters[i+1].tick); next < start+steps {
				steps = next - start // cut where the next signature starts: a bar of its own meter
			}
		}
		bars = append(bars, bar{start, steps, bm})
		start += steps
	}
	if !ok {
		main = Meter{}
	}
	first := at(m.line)
	if k := sort.Search(len(bars), func(k int) bool { return bars[k].start >= first }); k > 0 && first < main.Steps() && k < len(bars) {
		bars = slices.Replace(bars, 0, k, bar{first - main.Steps(), main.Steps(), main})
	}
	barAt := func(step int) int {
		return max(sort.Search(len(bars), func(i int) bool { return bars[i].start > step })-1, 0)
	}
	// each bar's tempo: the clock's at its line, folded as the first tempo was
	tempos := make([]int, len(bars))
	for bi, br := range bars {
		t := m.clock[0].perTick
		for _, c := range m.clock {
			if at(c.tick) <= br.start {
				t = c.perTick
			}
		}
		tempos[bi] = min(max(int(math.Round(60/t/float64(m.division)*bpm/m.BPM)), MinBPM), MaxBPM)
	}

	var c, b [12]float64
	for _, l := range lines {
		for _, n := range l.notes {
			if l.part != RipBeat {
				c[n.key%12] += float64(n.off - n.on)
				if int(n.key) < RipBassTop {
					b[n.key%12] += float64(n.off - n.on)
				}
			}
		}
	}
	root, scale := keyOf(c, b)
	if m.key != nil {
		root, scale = m.key.of(c)
	}

	// each line's notes on its bars' steps, one a step and pitch, the louder kept
	events := make([][][]StepDef, len(bars))
	for i := range events {
		events[i] = make([][]StepDef, len(lines))
	}
	merged, unheld, long := 0, 0, 0
	for li, l := range lines {
		for _, n := range l.notes {
			step := at(n.on)
			bi := barAt(step)
			// three places hold a note-on's velocity exactly, and read as authored
			s := StepDef{Pos: step - bars[bi].start, Vel: math.Round(float64(n.vel)/127*1000) / 1000}
			if l.part != RipBeat {
				dur := int(math.Round(float64(n.off-n.on) / perStep))
				if dur > MaxPatternLen {
					long++
				}
				s.Dur = min(max(dur, 1), MaxPatternLen)
				s.Bend, s.BendAt = n.bend, n.bendAt
				if s.Deg, s.Oct, s.Acc = PitchStep(root, scale, int(n.key)); StepPitchBounds(s.Deg, s.Oct, s.Acc) != nil {
					unheld++
					continue
				}
			}
			evs := &events[bi][li]
			if j := slices.IndexFunc(*evs, func(e StepDef) bool { return e.Pos == s.Pos && e.Deg == s.Deg && e.Oct == s.Oct && e.Acc == s.Acc }); j >= 0 {
				merged++
				if (*evs)[j].Vel < s.Vel {
					(*evs)[j] = s
				}
				continue
			}
			if len(*evs) == MaxPatternEvents {
				unheld++
				continue
			}
			*evs = append(*evs, s)
		}
	}

	song := &SongDef{Name: name, BPM: int(math.Round(bpm)), Meter: main.Doc(), Root: root, Scale: ScaleName(scale), Bars: make([][]string, len(bars))}
	var pats []*PatternDef
	var named [RipParts]map[string]string // a pattern's name by what it holds
	head := BarHead{main, song.BPM}
	for bi, br := range bars {
		// a bar of another meter or tempo names it, and holds it for the bars after
		var words []string
		bm := br.meter
		if br.steps != bm.Steps() {
			bm = meterOf(br.steps, StepsPerBar)
		}
		if bm != head.Meter {
			words, head.Meter = append(words, bm.String()), bm
		}
		if tempos[bi] != head.BPM {
			words, head.BPM = append(words, strconv.Itoa(tempos[bi])+"bpm"), tempos[bi]
		}
		song.Bars[bi] = append(words, make([]string, MusicSlots)...)
		for part := range RipParts {
			p := &PatternDef{Steps: br.steps, Meter: br.meter.Doc(), Role: Role(RipPartSlots[part] + 1).String(), Groups: []string{name}}
			sounds := false
			for li, l := range lines {
				if l.part == part {
					evs := events[bi][li]
					slices.SortStableFunc(evs, func(a, b StepDef) int { return a.Pos - b.Pos })
					p.Track = append(p.Track, TrackDef{Instr: l.instr, Desc: l.desc, Event: evs})
					sounds = sounds || len(evs) > 0
				}
			}
			if !sounds {
				continue
			}
			if named[part] == nil {
				named[part] = map[string]string{}
			}
			key := fmt.Sprint(p.Steps, p.Meter, p.Track)
			if p.Name = named[part][key]; p.Name == "" {
				p.Name = fmt.Sprintf("%s_%s_%s", name, RipPartNames[part], letters(len(named[part])))
				named[part][key] = p.Name
				pats = append(pats, p)
			}
			song.Bars[bi][len(song.Bars[bi])-MusicSlots+RipPartSlots[part]] = p.Name
		}
	}

	for v := InstrBass; v < InstrumentCount; v++ {
		if len(nearest[v]) > 0 {
			said = append(said, fmt.Sprintf("program(s) %s on the %s, the shipped voice of their family", strings.Join(nearest[v], ", "), v))
		}
	}
	if len(unkit) > 0 {
		keys := slices.Sorted(maps.Keys(unkit))
		words, hits := make([]string, len(keys)), 0
		for i, k := range keys {
			words[i], hits = fmt.Sprintf("%d ×%d", k, unkit[k]), hits+unkit[k]
		}
		said = append(said, fmt.Sprintf("%d hit(s) on keys the kit has no piece for left out: %s", hits, strings.Join(words, ", ")))
	}
	for _, n := range []struct {
		count int
		what  string
	}{
		{left, fmt.Sprintf("part(s) past a pattern's %d tracks left out", MaxPatternTracks)},
		{merged, "note(s) on a step and pitch another holds kept once, the louder"},
		{long, fmt.Sprintf("note(s) longer than %d steps cut to %[1]d", MaxPatternLen)},
		{unheld, "note(s) a pattern's step cannot hold left out"},
	} {
		if n.count > 0 {
			said = append(said, fmt.Sprintf("%d %s", n.count, n.what))
		}
	}
	return pats, song, said
}

// midiLinesOf gives each part its line in lineOf's order, -1 for one left out
func midiLinesOf(parts []midiPart) []int {
	lines := make([]int, len(parts))
	var taken [3]bool
	var rest []int
	for i, p := range parts {
		lines[i] = slices.Index(ripLineNames[:], strings.ToLower(strings.TrimSpace(p.name)))
		if lines[i] >= 0 {
			taken[lines[i]] = true
		} else {
			rest = append(rest, i)
		}
	}
	median := func(i int) int {
		keys := make([]int, len(parts[i].notes))
		for j, n := range parts[i].notes {
			keys[j] = int(n.key)
		}
		slices.Sort(keys)
		return keys[len(keys)/2]
	}
	if len(rest) > 0 && !taken[2] {
		low := slices.MinFunc(rest, func(a, b int) int { return cmp.Compare(median(a), median(b)) })
		if median(low) < RipBassTop {
			lines[low], taken[2] = 2, true
			rest = slices.DeleteFunc(rest, func(i int) bool { return i == low })
		}
	}
	for _, i := range rest {
		if l := slices.Index(taken[:2], false); l >= 0 {
			lines[i], taken[l] = l, true
		}
	}
	return lines
}

// readMIDITrack reads the track chunk numbered track: its notes by channel with
// their glides, each part named by the track, and its tempo changes, signatures
// and program changes. A note-off ends the earliest note of its channel and pitch
// sounding, under the sustain pedal too, the pedal's ring being the sound's; a
// note still sounding when the track ends ends with it.
func readMIDITrack(b []byte, track int) (midiTrack, error) {
	var t midiTrack
	var name string
	var notes []midiNote
	var heard []midiHeard
	open := map[[2]byte][]int{} // channel and pitch: the notes sounding, oldest first
	var bend, span [16]float64  // each channel's bend and its range, in semitones
	var rpn [16][2]byte         // each channel's registered parameter
	for ch := range span {
		span[ch], rpn[ch] = midiBendRange, [2]byte{127, 127}
	}
	var tick int64
	var running byte
	for i := 0; i < len(b); {
		delta, n := readVLQ(b[i:])
		if n == 0 {
			return t, errors.New("a delta time cut short")
		}
		i += n
		tick += int64(delta)
		if i >= len(b) {
			return t, errors.New("an event cut short")
		}
		status := b[i]
		if status < 0x80 {
			if running == 0 {
				return t, fmt.Errorf("a data byte %#x with no status before it", status)
			}
			status = running
		} else {
			i++
		}
		switch {
		case status == 0xFF || status == 0xF0 || status == 0xF7:
			kind := byte(0)
			if status == 0xFF {
				if i >= len(b) {
					return t, errors.New("a meta event cut short")
				}
				kind, i = b[i], i+1
			}
			size, n := readVLQ(b[i:])
			if n == 0 || size > len(b)-i-n {
				return t, errors.New("a meta or system exclusive event cut short")
			}
			body := b[i+n : i+n+size]
			i += n + size
			running = 0
			switch {
			case status != 0xFF:
			case kind == 0x03 && name == "":
				name = string(body)
			case kind == 0x51 && size == 3:
				t.tempos = append(t.tempos, [2]int64{tick, int64(body[0])<<16 | int64(body[1])<<8 | int64(body[2])})
			case kind == 0x58 && size >= 2 && body[0] > 0 && body[1] < 8:
				t.meters = append(t.meters, midiMeter{tick: tick, num: int(body[0]), den: 1 << body[1]})
			case kind == 0x59 && size == 2 && int8(body[0]) >= -7 && int8(body[0]) <= 7:
				t.keys = append(t.keys, midiKey{tick: tick, sf: int(int8(body[0])), minor: body[1] == 1})
			case kind == 0x2F:
				i = len(b)
			}
		case status >= 0xF0:
			return t, fmt.Errorf("status %#x in a file", status)
		default:
			running = status
			kind, width := status&0xF0, 2
			if kind == 0xC0 || kind == 0xD0 {
				width = 1
			}
			if i+width > len(b) {
				return t, errors.New("a channel message cut short")
			}
			ch, key, vel := status&0x0F, b[i]&0x7F, b[i+width-1]&0x7F
			i += width
			at := [2]byte{ch, key}
			switch {
			case kind == 0x90 && vel > 0:
				open[at] = append(open[at], len(notes))
				notes = append(notes, midiNote{on: tick, off: -1, ch: ch, key: key, vel: vel})
				heard = append(heard, midiHeard{from: bend[ch], at: tick})
			case kind == 0x80 || kind == 0x90:
				if q := open[at]; len(q) > 0 {
					notes[q[0]].glide(tick, bend[ch], heard[q[0]])
					open[at] = q[1:]
				}
			case kind == 0xC0:
				t.programs = append(t.programs, midiChange{tick: tick, ch: ch, program: key})
			case kind == 0xB0 && (key == 101 || key == 100):
				rpn[ch][101-key] = vel
			case kind == 0xB0 && (key == 99 || key == 98):
				rpn[ch] = [2]byte{127, 127} // a non-registered parameter deselects it
			case kind == 0xB0 && key == 6 && rpn[ch] == [2]byte{}:
				span[ch] = float64(vel)
			case kind == 0xB0 && key == 38 && rpn[ch] == [2]byte{}:
				span[ch] = math.Floor(span[ch]) + float64(vel)/100
			case kind == 0xE0:
				bend[ch] = float64(int(key)|int(vel)<<7-8192) / 8192 * span[ch]
				for k, q := range open {
					if k[0] != ch {
						continue
					}
					for _, i := range q {
						switch h := &heard[i]; {
						case h.moved:
						case bend[ch] == h.from:
							h.at = tick
						default:
							h.moved = true
						}
					}
				}
			}
		}
	}
	for i := range notes {
		if notes[i].off < 0 {
			notes[i].glide(tick, bend[notes[i].ch], heard[i])
		}
	}
	of := map[byte]int{}
	for _, n := range notes {
		p, ok := of[n.ch]
		if !ok {
			p, of[n.ch] = len(t.parts), len(t.parts)
			t.parts = append(t.parts, midiPart{name: name, track: track})
		}
		t.parts[p].notes = append(t.parts[p].notes, n)
	}
	return t, nil
}

// midiHeard is what a sounding note has heard of its channel's bend: the bend at
// its start, the last tick it stood there, and whether it has left it since
type midiHeard struct {
	from  float64
	at    int64
	moved bool
}

// glide ends the note at off with its channel's bend at bend: one that moved a
// quarter tone or more glides by the difference, to the hundredth, from the share
// of its length where the bend last stood at its start
func (n *midiNote) glide(off int64, bend float64, h midiHeard) {
	n.off = off
	if d := bend - h.from; math.Abs(d) >= 0.5 && off > n.on {
		n.bend = min(max(math.Round(d*100)/100, -MaxStepBend), MaxStepBend)
		n.bendAt = min(math.Round(float64(h.at-n.on)/float64(off-n.on)*100)/100, 0.99)
	}
}

// readVLQ reads a variable-length quantity: its value and its bytes, none when
// it is cut short or runs past the four bytes a file allows
func readVLQ(b []byte) (int, int) {
	v := 0
	for i := 0; i < len(b) && i < 4; i++ {
		v = v<<7 | int(b[i]&0x7F)
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	return 0, 0
}

// appendVLQ appends v as a variable-length quantity
func appendVLQ(b []byte, v int64) []byte {
	var tmp [5]byte
	i := len(tmp) - 1
	tmp[i] = byte(v & 0x7F)
	for v >>= 7; v > 0; v >>= 7 {
		i--
		tmp[i] = byte(v&0x7F) | 0x80
	}
	return append(b, tmp[i:]...)
}

// midiEvent is an event a writer puts in a track: its tick, its place among the
// events of one tick (meta, program, a glide's bend, note-off, a note's first
// bend, note-on) and its bytes
type midiEvent struct {
	tick int64
	rank int
	data []byte
}

func midiMeta(tick int64, kind byte, body ...byte) midiEvent {
	return midiEvent{tick: tick, data: append(appendVLQ([]byte{0xFF, kind}, int64(len(body))), body...)}
}

func midiTempoAt(bpm float64) midiEvent {
	us := int(math.Round(60e6 / bpm))
	return midiMeta(0, 0x51, byte(us>>16), byte(us>>8), byte(us))
}

func midiNoteOn(tick int64, ch, key, vel byte) midiEvent {
	return midiEvent{tick: tick, rank: 5, data: []byte{0x90 | ch, key, vel}}
}

func midiNoteOff(tick int64, ch, key byte) midiEvent {
	return midiEvent{tick: tick, rank: 3, data: []byte{0x80 | ch, key, 0}}
}

func midiProgramAt(ch, program byte) midiEvent {
	return midiEvent{rank: 1, data: []byte{0xC0 | ch, program}}
}

// midiVel is a velocity of 0 to 1 as a note-on's, never the 0 that ends a note
func midiVel(v float64) byte { return byte(min(max(math.Round(v*127), 1), 127)) }

// writeSMF writes tracks as a Standard MIDI File of type 1, midiDivision ticks a
// quarter, each track's events in time and the track ended at its last
func writeSMF(w io.Writer, tracks [][]midiEvent) error {
	be := binary.BigEndian
	out := be.AppendUint16(append([]byte("MThd"), 0, 0, 0, 6, 0, 1), uint16(len(tracks)))
	out = be.AppendUint16(out, midiDivision)
	for _, events := range tracks {
		slices.SortStableFunc(events, func(a, b midiEvent) int { return cmp.Or(cmp.Compare(a.tick, b.tick), cmp.Compare(a.rank, b.rank)) })
		var trk []byte
		var at int64
		for _, e := range events {
			trk = append(appendVLQ(trk, e.tick-at), e.data...)
			at = e.tick
		}
		trk = append(trk, 0, 0xFF, 0x2F, 0)
		out = be.AppendUint32(append(out, "MTrk"...), uint32(len(trk)))
		out = append(out, trk...)
	}
	_, err := w.Write(out)
	return err
}

// WriteMIDI writes a transcript as a Standard MIDI File of type 1 at bpm in meter
// m: its lines as tracks named melody, under and bass on channels 1 to 3 and the
// lead's, lead's and bass's programs, the tempo and a meter other than 4/4 on
// the first, its hits as a track named drums on the drum channel, a sixteenth
// each. A note lasts a tick at least; ReadMIDI reads it back within half a tick.
func WriteMIDI(w io.Writer, notes []RipNote, hits []RipHit, bpm int, m Meter) error {
	if bpm <= 0 {
		return fmt.Errorf("midi: tempo %d bpm", bpm)
	}
	var tracks [4][]midiEvent // melody, under, bass, drums
	for l, voice := range [3]InstrumentType{InstrLead, InstrLead, InstrBass} {
		tracks[l] = []midiEvent{midiMeta(0, 0x03, []byte(ripLineNames[l])...), midiProgramAt(byte(l), midiInstruments[voice].program)}
	}
	tracks[0] = append(tracks[0], midiTempoAt(float64(bpm)))
	if m.Doc() != "" {
		tracks[0] = append(tracks[0], midiMeta(0, 0x58, m.midi()...))
	}
	tracks[3] = []midiEvent{midiMeta(0, 0x03, []byte("drums")...)}
	perSec := float64(bpm) / 60 * midiDivision
	for _, n := range notes {
		if n.MIDI < 0 || n.MIDI > 127 || n.Start < 0 {
			return fmt.Errorf("midi: a note at %.3fs of pitch %d: want a pitch 0 to 127 at or after 0s", n.Start, n.MIDI)
		}
		on := int64(math.Round(n.Start * perSec))
		off := max(int64(math.Round(n.End*perSec)), on+1)
		l := lineOf(n)
		tracks[l] = append(tracks[l], midiNoteOn(on, byte(l), byte(n.MIDI), midiVel(n.Vel)), midiNoteOff(off, byte(l), byte(n.MIDI)))
	}
	for _, h := range hits {
		if h.Drum < 0 || h.Drum >= InstrClap || h.Start < 0 {
			return fmt.Errorf("midi: a hit at %.3fs of drum %d: want a kick, snare or hihat at or after 0s", h.Start, h.Drum)
		}
		on, key := int64(math.Round(h.Start*perSec)), midiInstruments[h.Drum].keys[0]
		tracks[3] = append(tracks[3], midiNoteOn(on, midiDrums, key, midiVel(h.Vel)), midiNoteOff(on+midiDivision/StepsPerBeat, midiDrums, key))
	}
	return writeSMF(w, tracks[:3+min(len(hits), 1)])
}

// midi is the meter as a time signature's bytes: its beats, its note's power of
// two, the MIDI clocks a click of that note and the 32nds a quarter
func (m Meter) midi() []byte {
	m = m.norm()
	return []byte{byte(m.Num), byte(bits.TrailingZeros(uint(m.Den))), byte(96 / m.Den), 8}
}

// keySignature is a key as a key signature's bytes: a major scale as its own,
// every other as the minor on its root
func keySignature(root int, scale ScaleID) []byte {
	tonic, minor := root%12, byte(0)
	if scale != ScaleMajor {
		tonic, minor = (tonic+3)%12, 1 // the relative major's sharps
	}
	sf := tonic * 7 % 12 // a fifth a sharp
	if sf > 6 {
		sf -= 12
	}
	return []byte{byte(int8(sf)), minor}
}

// of is the key's root, in the octave the engine's harmony root lives in, and
// of the scales on it the one whose tones hold the most of a chroma c and whose
// others the least, the signature's own on a tie
func (k midiKey) of(c [12]float64) (int, ScaleID) {
	tonic, scale := (k.sf*7%12+12)%12, ScaleMajor
	if k.minor {
		tonic, scale = (tonic+9)%12, ScaleMinor
	}
	best := math.Inf(-1)
	for _, s := range []ScaleID{scale, ScaleMajor, ScaleMinor, ScaleDorian, ScalePhrygian, ScaleHarmonicMinor, ScaleMinorPent} {
		var in [12]bool
		for _, iv := range scaleTable[s] {
			in[(tonic+iv)%12] = true
		}
		var score float64
		for pc, v := range c {
			if in[pc] {
				score += v
			} else {
				score -= v
			}
		}
		if score > best {
			best, scale = score, s
		}
	}
	return tonic + 3*12, scale
}

// WritePatternMIDI writes a pattern as WriteSongMIDI writes a song in its meter
// that plays it once through, at bpm in the key and chords given
func WritePatternMIDI(w io.Writer, p *PatternDef, bpm, root int, scale ScaleID, chords []int) error {
	slot := 1
	if r := slices.Index(roleNames[:], p.Role); r > int(RoleNone) {
		slot = Role(r).slot()
	}
	d := &SongDef{Name: p.Name, BPM: bpm, Meter: p.Meter, Root: root, Scale: ScaleName(scale), Chords: chords}
	bar := d.Signature().Steps()
	for range max((p.Steps+bar-1)/bar, 1) {
		bar := make([]string, MusicSlots)
		bar[slot] = p.Name
		d.Bars = append(d.Bars, bar)
	}
	return WriteSongMIDI(w, d, func(string) *PatternDef { return p })
}

// WriteSongMIDI writes a song as a Standard MIDI File of type 1: a track of its
// tempo, key and meter, then a track a pattern track by slot, instrument and
// order, a slot's pattern playing at the song's step: a voice on its program
// (midiProgram), a drum on the drum channel, each note as long as its step and
// its glide as pitch bend (midiBends).
func WriteSongMIDI(w io.Writer, d *SongDef, pattern func(string) *PatternDef) error {
	if err := ValidateSong(d); err != nil {
		return err
	}
	pats := map[string]*Pattern{}
	for b := range d.Bars {
		for _, n := range d.Patterns(b) {
			if n == "" || pats[n] != nil {
				continue
			}
			def := pattern(n)
			if def == nil {
				return fmt.Errorf("song %q: %q is no pattern", d.Name, n)
			}
			p, err := def.Pattern()
			if err != nil {
				return err
			}
			pats[n] = p
		}
	}
	const perStep = midiDivision / StepsPerBeat
	heads := d.Heads()
	conductor := []midiEvent{midiMeta(0, 0x03, []byte(d.Name)...), midiTempoAt(float64(heads[0].BPM)), midiMeta(0, 0x59, keySignature(d.Root, d.ScaleID())...), midiMeta(0, 0x58, heads[0].Meter.midi()...)}
	type lane struct {
		slot  int
		instr InstrumentType
		nth   int // of the tracks of its instrument in its pattern
	}
	var lanes []lane
	names := map[lane]string{}
	notes := map[lane][]midiPlayed{}
	held := map[lane]map[byte]int{} // a lane's last note of each key
	h := newHarmony()
	h.set(d.Root, d.ScaleID(), d.Chords)
	at := 0
	for b := range d.Bars {
		steps := heads[b].Meter.Steps()
		if b > 0 {
			h.advanceBar()
			if heads[b].Meter != heads[b-1].Meter {
				conductor = append(conductor, midiMeta(int64(at*perStep), 0x58, heads[b].Meter.midi()...))
			}
			if heads[b].BPM != heads[b-1].BPM {
				e := midiTempoAt(float64(heads[b].BPM))
				e.tick = int64(at * perStep)
				conductor = append(conductor, e)
			}
		}
		for slot, n := range d.Patterns(b) {
			p := pats[n]
			if p == nil {
				continue
			}
			nth := map[InstrumentType]int{}
			for _, tr := range p.Tracks {
				ln := lane{slot, tr.Instr, nth[tr.Instr]}
				nth[tr.Instr]++
				if _, ok := names[ln]; !ok {
					lanes = append(lanes, ln)
					names[ln], held[ln] = cmp.Or(tr.Desc, InstrumentName(tr.Instr)), map[byte]int{}
				}
				ns := notes[ln]
				for j := range steps {
					local, tick := (at+j)%p.Steps, int64((at+j)*perStep)
					for _, e := range tr.Events {
						if e.Pos != local || e.Vel <= 0 {
							continue
						}
						n := midiPlayed{on: tick, off: tick + perStep, vel: midiVel(e.Vel)}
						if tr.Instr.IsDrum() {
							n.key = midiInstruments[tr.Instr].keys[0]
						} else {
							n.key = byte(h.resolve(e.Deg, e.Oct, e.Acc, tr.FollowChord))
							n.off = tick + int64(max(e.Dur, 1)*perStep)
							n.end, n.bend, n.bendAt = n.off, e.Bend, e.BendAt
						}
						// a key sounding again ends its note before, as a reader pairs a
						// note-off with the earliest of its key; on one tick, once, the louder
						if i, ok := held[ln][n.key]; ok && ns[i].off > tick {
							if ns[i].on == tick {
								ns[i].vel, ns[i].off = max(ns[i].vel, n.vel), max(ns[i].off, n.off)
								continue
							}
							ns[i].off = tick
						}
						held[ln][n.key] = len(ns)
						ns = append(ns, n)
					}
				}
				notes[ln] = ns
			}
		}
		at += steps
	}
	// a channel a program, the drums' kept apart; a lane that glides has one of
	// its own while five stay for the shipped voices' programs, since a channel
	// bends every note it sounds
	channels := map[byte]byte{}
	used := 0
	channel := func() byte {
		c := byte(used)
		if used++; c >= midiDrums {
			c++
		}
		return c
	}
	tracks := map[lane][]midiEvent{}
	for _, ln := range lanes {
		evs := []midiEvent{midiMeta(0, 0x03, []byte(names[ln])...)}
		if ln.instr.IsDrum() {
			tracks[ln] = append(evs, midiNotes(midiDrums, notes[ln])...)
			continue
		}
		program, reach := midiProgram(ln.instr), 0.0
		for _, n := range notes[ln] {
			reach = max(reach, math.Abs(n.bend))
		}
		ch, ok := channels[program]
		switch {
		case reach > 0 && used < midiChannels-int(InstrumentCount-InstrBass):
			ch = channel()
		case !ok:
			ch = channel()
			channels[program] = ch
		}
		evs = append(evs, midiProgramAt(ch, program))
		if reach > 0 {
			evs = append(evs, midiBends(ch, reach, notes[ln])...)
		}
		tracks[ln] = append(evs, midiNotes(ch, notes[ln])...)
	}
	slices.SortStableFunc(lanes, func(a, b lane) int { return cmp.Compare(a.slot, b.slot) })
	all := [][]midiEvent{conductor}
	for _, ln := range lanes {
		all = append(all, tracks[ln])
	}
	return writeSMF(w, all)
}

// midiPlayed is a note a writer writes: its ticks, key and velocity, and its
// glide, bend semitones by end from bendAt of its length
type midiPlayed struct {
	on, off, end int64
	key, vel     byte
	bend, bendAt float64
}

// midiNotes is each note's note-on and note-off on channel ch
func midiNotes(ch byte, notes []midiPlayed) []midiEvent {
	evs := make([]midiEvent, 0, 2*len(notes))
	for _, n := range notes {
		evs = append(evs, midiNoteOn(n.on, ch, n.key, n.vel), midiNoteOff(n.off, ch, n.key))
	}
	return evs
}

// midiBends is a gliding lane's pitch bend on channel ch: its range by RPN 0, the
// least of 2, 12 and 24 semitones that reaches its largest bend, then from each
// onset to the next the glide of its first note that bends, a marker at the start
// pitch where it begins and a bend each midiBendTicks to its end, centred again
// at the next onset; ReadMIDI reads each note's back to the hundredth
func midiBends(ch byte, reach float64, notes []midiPlayed) []midiEvent {
	span := 24.0
	if reach <= 2 {
		span = 2
	} else if reach <= 12 {
		span = 12
	}
	cc := func(c, v byte) midiEvent { return midiEvent{rank: 1, data: []byte{0xB0 | ch, c, v}} }
	evs := []midiEvent{cc(101, 0), cc(100, 0), cc(6, byte(span)), cc(38, 0), cc(101, 127), cc(100, 127)}
	bend := func(tick int64, rank int, semis float64) midiEvent {
		v := min(max(int(math.Round(8192+semis/span*8192)), 0), 16383)
		return midiEvent{tick: tick, rank: rank, data: []byte{0xE0 | ch, byte(v & 0x7F), byte(v >> 7)}}
	}
	bent := false
	for i := 0; i < len(notes); {
		on := notes[i].on
		j, g := i, -1
		for ; j < len(notes) && notes[j].on == on; j++ {
			if g < 0 && notes[j].bend != 0 {
				g = j
			}
		}
		next := int64(math.MaxInt64)
		if j < len(notes) {
			next = notes[j].on
		}
		i = j
		if bent {
			evs, bent = append(evs, bend(on, 4, 0)), false
		}
		if g < 0 {
			continue
		}
		n := notes[g]
		from := on + int64(math.Round(n.bendAt*float64(n.end-on)))
		if from >= next {
			continue // the next onset comes before it moves
		}
		if from > on {
			evs = append(evs, bend(from, 2, 0))
		}
		for t := from; t < n.end; {
			if t = min(t+midiBendTicks, n.end); t > next {
				break
			}
			evs = append(evs, bend(t, 2, n.bend*float64(t-from)/float64(n.end-from)))
		}
		bent = true
	}
	return evs
}
