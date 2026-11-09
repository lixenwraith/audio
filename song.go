package audio

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/lixenwraith/toml"
)

// ScaleNames are the scales by the names a document and a key command use
var ScaleNames = [ScaleCount]string{
	ScalePhrygian:      "phrygian",
	ScaleMinor:         "minor",
	ScaleHarmonicMinor: "harmonic_minor",
	ScaleDorian:        "dorian",
	ScaleMinorPent:     "minor_pent",
	ScaleMajor:         "major",
}

// ScaleByName is the scale a name stands for
func ScaleByName(name string) (ScaleID, bool) {
	i := slices.Index(ScaleNames[:], name)
	return ScaleID(i), i >= 0
}

// ScaleName is a scale's name, "unknown" past the table
func ScaleName(s ScaleID) string {
	if s >= 0 && s < ScaleCount {
		return ScaleNames[s]
	}
	return "unknown"
}

// SongDef is a timeline: the patterns each slot plays bar by bar at a tempo
// and in a key, a ripped recording's form once its bars match a dictionary.
// A bar names a pattern per slot, "" for silence, a longer pattern playing on
// until named again or cleared; its list may begin with a meter word ("2/8")
// and a tempo word ("72bpm"), each holding until a later bar names another.
type SongDef struct {
	Name   string  `toml:"name"`
	Desc   string  `toml:"desc,omitempty"`
	BPM    int     `toml:"bpm"`
	Meter  string  `toml:"meter,omitempty"` // its bars' time signature, 4/4 when absent
	Root   int     `toml:"root"`
	Scale  string  `toml:"scale"`
	Chords []int   `toml:"chords,omitempty"` // the chord degrees the key cycles, as key takes them
	Tuning float64 `toml:"tuning,omitempty"` // cents off A440 it plays at, a recording's
	// Kit names the sound a drum plays in the song by the drum's name, a rip's
	// matched kit; a drum it leaves out plays its own
	Kit  map[string]string `toml:"kit,omitempty"`
	Bars [][]string        `toml:"bars"`
}

// Clone is a deep copy
func (d *SongDef) Clone() *SongDef {
	c := *d
	c.Chords = slices.Clone(d.Chords)
	c.Kit = maps.Clone(d.Kit)
	c.Bars = make([][]string, len(d.Bars))
	for i, b := range d.Bars {
		c.Bars[i] = slices.Clone(b)
	}
	return &c
}

// ScaleID is the song's scale, phrygian when unnamed
func (d *SongDef) ScaleID() ScaleID {
	if s, ok := ScaleByName(d.Scale); ok {
		return s
	}
	return ScalePhrygian
}

// Signature is the song's meter, 4/4 when unnamed or unread
func (d *SongDef) Signature() Meter {
	m, _ := ParseMeter(d.Meter)
	return m
}

// BarHead is what a bar plays in: its meter and tempo, the song's unless the
// bar or one before it named others
type BarHead struct {
	Meter Meter
	BPM   int
}

// barWords parts a bar's leading meter and tempo words from its pattern names:
// a word with a slash is a meter, digits ending in bpm a tempo; nil and 0 for
// none named
func barWords(bar []string) (m *Meter, bpm int, patterns []string, err error) {
	patterns = bar
	for len(patterns) > 0 {
		w := patterns[0]
		switch {
		case strings.Contains(w, "/"):
			mm, perr := ParseMeter(w)
			if perr != nil {
				return nil, 0, nil, perr
			}
			m = &mm
		case strings.HasSuffix(w, "bpm"):
			n, aerr := strconv.Atoi(strings.TrimSuffix(w, "bpm"))
			if aerr != nil || n < MinBPM || n > MaxBPM {
				return nil, 0, nil, fmt.Errorf("tempo %q: want %d to %d bpm", w, MinBPM, MaxBPM)
			}
			bpm = n
		default:
			return m, bpm, patterns, nil
		}
		patterns = patterns[1:]
	}
	return m, bpm, patterns, nil
}

// Patterns is a bar's pattern names, its meter and tempo words left out
func (d *SongDef) Patterns(bar int) []string {
	_, _, p, _ := barWords(d.Bars[bar])
	return p
}

// Heads is each bar's meter and tempo, the song's where none is named
func (d *SongDef) Heads() []BarHead {
	heads := make([]BarHead, len(d.Bars))
	head := BarHead{d.Signature(), d.BPM}
	for i, b := range d.Bars {
		m, bpm, _, _ := barWords(b)
		if m != nil {
			head.Meter = *m
		}
		if bpm > 0 {
			head.BPM = bpm
		}
		heads[i] = head
	}
	return heads
}

// BeatSeconds is the seconds each beat of the song's bars starts at, the
// first bar's downbeat through the last bar's end, in the song's meter at each
// bar's tempo: the grid a render of the song is read on (RipGrid.Beats)
func (d *SongDef) BeatSeconds() []float64 {
	m := d.Signature()
	out := []float64{0}
	for _, h := range d.Heads() {
		bar := float64(SamplesPerBar(h.BPM, m)) / AudioSampleRate
		for j := 1; j <= m.Beats(); j++ {
			out = append(out, out[len(out)-1-(j-1)]+bar*float64(j)/float64(m.Beats()))
		}
	}
	return out
}

// Uniform reports whether every bar plays in the song's meter and tempo
func (d *SongDef) Uniform() bool {
	for _, b := range d.Bars {
		if m, bpm, _, _ := barWords(b); m != nil || bpm > 0 {
			return false
		}
	}
	return true
}

// ValidateSong is what a song must hold to play: a name, a tempo in range, a meter
// the grid plays, a root on the keyboard, a known scale and at least one bar of at
// most MusicSlots names
func ValidateSong(d *SongDef) error {
	var errs []error
	if d.Name == "" {
		errs = append(errs, errors.New("song: name required"))
	}
	if d.BPM < MinBPM || d.BPM > MaxBPM {
		errs = append(errs, fmt.Errorf("song %q: bpm %d out of %d..%d", d.Name, d.BPM, MinBPM, MaxBPM))
	}
	if _, err := ParseMeter(d.Meter); err != nil {
		errs = append(errs, fmt.Errorf("song %q: %w", d.Name, err))
	}
	if d.Root < 0 || d.Root > 127 {
		errs = append(errs, fmt.Errorf("song %q: root %d is not a MIDI note", d.Name, d.Root))
	}
	if _, ok := ScaleByName(d.Scale); !ok {
		errs = append(errs, fmt.Errorf("song %q: unknown scale %q", d.Name, d.Scale))
	}
	if len(d.Bars) == 0 {
		errs = append(errs, fmt.Errorf("song %q: no bars", d.Name))
	}
	if d.Tuning < -MaxTuningCents || d.Tuning > MaxTuningCents || d.Tuning != d.Tuning {
		errs = append(errs, fmt.Errorf("song %q: tuning %v cents outside a semitone of A440", d.Name, d.Tuning))
	}
	for _, drum := range slices.Sorted(maps.Keys(d.Kit)) {
		if i, ok := InstrumentByName(drum); !ok || !i.IsDrum() || d.Kit[drum] == "" {
			errs = append(errs, fmt.Errorf("song %q: kit: %q is no drum, or names no sound", d.Name, drum))
		}
	}
	for i, b := range d.Bars {
		_, _, patterns, err := barWords(b)
		if err != nil {
			errs = append(errs, fmt.Errorf("song %q: bar %d: %w", d.Name, i, err))
		} else if len(patterns) > MusicSlots {
			errs = append(errs, fmt.Errorf("song %q: bar %d names %d patterns, %d slots", d.Name, i, len(patterns), MusicSlots))
		}
	}
	return errors.Join(errs...)
}

// Timeline resolves the song's bars to pattern IDs through lookup, which names a
// registered pattern or returns PatternSilence for one it does not know; a name
// it does not know is an error, "" is silence.
func (d *SongDef) Timeline(lookup func(string) PatternID) ([][MusicSlots]PatternID, error) {
	out := make([][MusicSlots]PatternID, len(d.Bars))
	for i := range d.Bars {
		for slot, name := range d.Patterns(i) {
			if slot >= MusicSlots || name == "" {
				continue
			}
			id := lookup(name)
			if id == PatternSilence {
				return nil, fmt.Errorf("song %q: bar %d slot %d: %q is not a registered pattern", d.Name, i, slot, name)
			}
			out[i][slot] = id
		}
	}
	return out, nil
}

// MarshalSongs emits songs as a document of their own
func MarshalSongs(songs []*SongDef) ([]byte, error) {
	return toml.Marshal(PatternSpecFile{Song: songs})
}

// LoadPatternFile is LoadPatternDefs with the songs the document carries
func LoadPatternFile(data []byte) (*PatternSpecFile, error) {
	f, err := loadPatternSpecFile(data)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// MarshalPatternFile emits patterns and songs together, as save writes them
func MarshalPatternFile(f *PatternSpecFile) ([]byte, error) { return toml.Marshal(*f) }

// Cluster groups n items whose distance stays within threshold: each its own
// group at first, the two nearest groups merged while their mean distance holds
// within it. It returns each item's group and each group's medoid, the member
// nearest the rest, groups numbered by their first item. dist is symmetric and
// zero on the diagonal.
func Cluster(n int, dist func(i, j int) float64, threshold float64) (assign, medoids []int) {
	if n == 0 {
		return nil, nil
	}
	d := make([][]float64, n)
	for i := range d {
		d[i] = make([]float64, n)
		for j := range i {
			d[i][j] = dist(i, j)
			d[j][i] = d[i][j]
		}
	}
	groups := make([][]int, n)
	for i := range groups {
		groups[i] = []int{i}
	}
	linkage := func(a, b []int) float64 {
		var sum float64
		for _, i := range a {
			for _, j := range b {
				sum += d[i][j]
			}
		}
		return sum / float64(len(a)*len(b))
	}
	for len(groups) > 1 {
		bi, bj, best := -1, -1, threshold
		for i := range groups {
			for j := i + 1; j < len(groups); j++ {
				if l := linkage(groups[i], groups[j]); l <= best {
					bi, bj, best = i, j, l
				}
			}
		}
		if bi < 0 {
			break
		}
		groups[bi] = append(groups[bi], groups[bj]...)
		groups = slices.Delete(groups, bj, bj+1)
	}
	slices.SortFunc(groups, func(a, b []int) int { return cmp.Compare(slices.Min(a), slices.Min(b)) })
	assign, medoids = make([]int, n), make([]int, len(groups))
	for g, members := range groups {
		best, bestSum := members[0], 0.0
		for k, i := range members {
			assign[i] = g
			var sum float64
			for _, j := range members {
				sum += d[i][j]
			}
			if k == 0 || sum < bestSum {
				best, bestSum = i, sum
			}
		}
		medoids[g] = best
	}
	return assign, medoids
}

// RipPart is one of the three lines a rip hears: the beat, the melody and the bass
type RipPart int

const (
	RipBeat RipPart = iota
	RipMelody
	RipBass
	RipParts
)

// RipPartNames and RipPartSlots are each part's word in a pattern's name and the
// slot the game plays it in
var (
	RipPartNames = [RipParts]string{"beat", "melody", "bass"}
	RipPartSlots = [RipParts]int{0, 1, 3}
)

// PartBar is bar b with only one part in it, what the parts' distances compare
func (r *Rip) PartBar(part RipPart, b int) *RipBar {
	var out RipBar
	switch part {
	case RipBeat:
		out.Drums = r.Bars[b].Drums
	case RipMelody:
		out.Melody, out.Under = r.Bars[b].Melody, r.Bars[b].Under
	case RipBass:
		out.Bass = r.Bars[b].Bass
	}
	return &out
}

// empty reports a part with nothing in a bar
func (r *Rip) empty(part RipPart, b int) bool {
	pb := r.PartBar(part, b)
	n := len(pb.Melody) + len(pb.Under) + len(pb.Bass)
	for d := range pb.Drums {
		n += len(pb.Drums[d])
	}
	return n == 0
}

// Pattern writes one part over the bars lo to hi as a pattern of that many bars
// of the rip's meter named name, in a group of the rip's own and no tier; nil
// when the part is silent there. A register's track names the voice heard for
// it, or the lead and the bass when none was.
func (r *Rip) Pattern(name, group string, part RipPart, lo, hi int) *PatternDef {
	bar := r.Meter.Steps()
	p := &PatternDef{Name: name, Steps: (hi - lo) * bar, Meter: r.Meter.Doc(), Role: [RipParts]string{"rhythm", "melody", "bass"}[part], Groups: []string{group}}
	if part == RipBeat {
		for d := InstrKick; d < InstrClap; d++ {
			var evs []StepDef
			for b := lo; b < hi; b++ {
				for _, s := range r.Bars[b].Drums[d] {
					evs = append(evs, StepDef{Pos: s.Pos + (b-lo)*bar, Vel: s.Vel})
				}
			}
			if len(evs) > 0 {
				p.Track = append(p.Track, TrackDef{Instr: d.String(), Event: evs})
			}
		}
		if len(p.Track) == 0 {
			return nil
		}
		return p
	}
	// a line's track: its steps over the window through the voice heard or
	// fitted for it, or the shipped one named
	track := func(instr, own string, voice *VoiceDef, pick func(*RipBar) []Step) {
		if voice != nil {
			instr = group + own
		}
		var evs []StepDef
		for b := lo; b < hi; b++ {
			for _, s := range pick(&r.Bars[b]) {
				evs = append(evs, StepDef{Pos: s.Pos + (b-lo)*bar, Vel: s.Vel, Deg: s.Deg, Oct: s.Oct, Acc: s.Acc, Dur: min(s.Dur, MaxPatternLen), Bend: s.Bend, BendAt: s.BendAt})
			}
		}
		if len(evs) > 0 {
			p.Track = append(p.Track, TrackDef{Instr: instr, Event: evs})
		}
	}
	if part == RipBass {
		track("bass", "_bass_voice", r.BassVoice, func(b *RipBar) []Step { return b.Bass })
	} else {
		track("lead", "_melody_voice", r.MelodyVoice, func(b *RipBar) []Step { return b.Melody })
		track("lead", "_under_voice", r.UnderVoice, func(b *RipBar) []Step { return b.Under })
	}
	if len(p.Track) == 0 {
		return nil
	}
	return p
}

// MendPickup returns the rip with a first bar that starts partway given the
// beat of the bar most like it over the steps it has, when one lies within
// threshold: with half a bar to compare, its false hits would stay in the song.
func (r *Rip) MendPickup(threshold float64) *Rip {
	if len(r.Bars) < 2 {
		return r
	}
	from := r.Meter.Steps()
	first := r.Bars[0]
	for d := range first.Drums {
		for _, s := range first.Drums[d] {
			from = min(from, s.Pos)
		}
	}
	for _, line := range [][]Step{first.Melody, first.Under, first.Bass} {
		for _, s := range line {
			from = min(from, s.Pos)
		}
	}
	if from == 0 || from >= r.Meter.Steps() {
		return r
	}
	tail := func(b *RipBar) *RipBar {
		var out RipBar
		for d := range b.Drums {
			for _, s := range b.Drums[d] {
				if s.Pos >= from {
					out.Drums[d] = append(out.Drums[d], s)
				}
			}
		}
		return &out
	}
	mine, best, near := tail(&r.Bars[0]), -1, threshold
	for j := 1; j < len(r.Bars); j++ {
		if d := BarDistance(mine, tail(&r.Bars[j])); d <= near && !r.empty(RipBeat, j) {
			best, near = j, d
		}
	}
	if best < 0 {
		return r
	}
	mended := *r
	mended.Bars = slices.Clone(r.Bars)
	mended.Bars[0].Drums = tail(&r.Bars[best]).Drums
	return &mended
}

// Cluster groups a part's bars within threshold of one another by BarDistance,
// each bar's entry the bar that stands for its group; -1 where the part is
// silent, which no group takes.
func (r *Rip) Cluster(part RipPart, threshold float64) []int {
	var bars []int
	for b := range r.Bars {
		if !r.empty(part, b) {
			bars = append(bars, b)
		}
	}
	assign, medoids := Cluster(len(bars), func(i, j int) float64 {
		return BarDistance(r.PartBar(part, bars[i]), r.PartBar(part, bars[j]))
	}, threshold)
	out := make([]int, len(r.Bars))
	for i := range out {
		out[i] = -1
	}
	for i, b := range bars {
		out[b] = bars[medoids[assign[i]]]
	}
	return out
}

// ripSongTempo is how far a rip's bar must move in tempo, as a share, from the
// tempo its song last named before the bar names its own
const ripSongTempo = 0.03

// Song writes the rip as a dictionary and a timeline: per part, a pattern of
// one bar for every bar that stands for others in assign, named
// <name>_<part>_<letter> in order of first use, and a song named name playing
// them in the rip's order, tempo, meter and key on the kit Kit matches; a bar
// assigned -1 leaves its slot silent, one standing for no group still plays.
func (r *Rip) Song(name string, assign [RipParts][]int) ([]*PatternDef, *SongDef) {
	song := &SongDef{Name: name, BPM: r.BPM, Meter: r.Meter.Doc(), Root: r.Root, Scale: ScaleName(r.Scale), Chords: slices.Clone(r.Progression), Tuning: r.Tuning, Bars: make([][]string, len(r.Bars))}
	// a bar names its tempo where the recording's bars moved by ripSongTempo from the last named
	per, named := r.Meter.Beats(), r.BPM
	for i := range song.Bars {
		song.Bars[i] = make([]string, MusicSlots)
		if (i+1)*per < len(r.Beats) {
			if bpm := int(math.Round(15 * float64(r.Meter.Steps()) / (r.Beats[(i+1)*per] - r.Beats[i*per]))); math.Abs(float64(bpm-named)) >= ripSongTempo*float64(named) && bpm >= MinBPM && bpm <= MaxBPM {
				song.Bars[i] = append([]string{strconv.Itoa(bpm) + "bpm"}, song.Bars[i]...)
				named = bpm
			}
		}
	}
	for d := range InstrClap {
		if r.matched(d) {
			if song.Kit == nil {
				song.Kit = map[string]string{}
			}
			song.Kit[drumSoundNames[d]] = name + "_" + drumSoundNames[d]
		}
	}
	var pats []*PatternDef
	for part := range RipParts {
		named := map[int]string{}
		for b, rep := range assign[part] {
			if b >= len(r.Bars) || rep < 0 || rep >= len(r.Bars) {
				continue
			}
			pn, ok := named[rep]
			if !ok {
				pn = fmt.Sprintf("%s_%s_%s", name, RipPartNames[part], letters(len(named)))
				if p := r.Pattern(pn, name, part, rep, rep+1); p != nil {
					pats = append(pats, p)
				} else {
					pn = ""
				}
				named[rep] = pn
			}
			song.Bars[b][len(song.Bars[b])-MusicSlots+RipPartSlots[part]] = pn
		}
	}
	return pats, song
}

// letters counts a, b, ... z, aa, ab, ... as a spreadsheet's columns
func letters(n int) string {
	s := ""
	for {
		s = string(rune('a'+n%26)) + s
		if n = n/26 - 1; n < 0 {
			return s
		}
	}
}
