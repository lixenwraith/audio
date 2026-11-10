// Command snapshot writes down what a build plays: the patterns, sounds and
// voices the engine holds once the bank is registered, with any documents laid
// over it, and a render of each. Two snapshots compare with diff -r, and one
// build writes the same bytes every run.
package main

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/lixenwraith/audio"
	"github.com/lixenwraith/audio/bank"
)

// What the renders play: a voice's notes at each velocity, held and then let
// ring for so many seconds, a pattern for so many bars, under one seed
var (
	notes      = []int{36, 48, 60, 72}
	velocities = []float64{0.4, 0.8}
)

const (
	noteHold, noteTail = 0.5, 1.0
	bars               = 4
	seed               = 1
)

func main() {
	wav := flag.Bool("wav", false, "write each render as a WAV beside the listings")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: snapshot [-wav] <dir> [document.toml ...]")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	dir := flag.Arg(0)

	sounds, err := bank.Sounds()
	check(err)
	patterns, err := bank.Patterns()
	check(err)
	cfg := audio.DefaultAudioConfig()
	cfg.ForceBackend = audio.BackendNameNull
	cfg.BaseSounds, cfg.BaseVoices, cfg.BasePatterns = sounds.Sound, sounds.Voice, patterns
	// a player's documents, as vif lays them: one that reads as music is the
	// music document, any other the sound document, its includes beside it
	var notMusic error
	for _, path := range flag.Args()[1:] {
		data, err := os.ReadFile(path)
		check(err)
		_, err = audio.LoadPatternFile(data)
		switch {
		case err == nil && cfg.PatternTOML == nil:
			cfg.PatternTOML = data
		case err != nil && cfg.SoundFS == nil:
			cfg.SoundFS, cfg.SoundFile = os.DirFS(filepath.Dir(path)), filepath.Base(path)
			notMusic = fmt.Errorf("%s was read as a sound document, since as music: %w", path, err)
		default:
			check(fmt.Errorf("%s: the engine takes one music and one sound document", path))
		}
	}
	eng, err := audio.NewAudioEngine(cfg)
	check(err)
	check(eng.Start())
	defer eng.Stop()
	if err := eng.SpecError(); err != nil {
		check(errors.Join(err, notMusic))
	}
	check(os.MkdirAll(dir, 0o755))

	// the listings come first: a render of melody_gen rewrites its pattern
	var pats, snds, index strings.Builder
	held, events := audio.RegisteredPatterns(), 0
	for _, p := range held {
		dump(&pats, p.Name, reflect.ValueOf(p.Def()))
		for _, t := range p.Tracks {
			events += len(t.Events)
		}
	}
	for _, d := range audio.RegisteredSounds() {
		dump(&snds, "sound "+d.Name, reflect.ValueOf(d))
	}
	for _, v := range audio.RegisteredVoices() {
		dump(&snds, "voice "+v.Name, reflect.ValueOf(v))
	}

	index.WriteString("# render, samples, peak dB, first half second's RMS dB, sha256 of the samples\n")
	render := func(name string, buf []float64, err error) {
		check(err)
		name = strings.Map(fileRune, name)
		index.WriteString(entry(name, buf))
		if *wav {
			f, err := os.Create(filepath.Join(dir, name+".wav"))
			check(err)
			check(audio.WriteWAV(f, buf))
			check(f.Close())
		}
	}
	for _, d := range audio.RegisteredSounds() {
		buf, err := audio.RenderPreview(d, audio.SoundShape{})
		render("sound-"+d.Name, buf, err)
	}
	for _, v := range audio.RegisteredVoices() {
		for _, n := range notes {
			for _, vel := range velocities {
				render(fmt.Sprintf("voice-%s-%d-%v", v.Name, n, vel), audio.RenderNote(v, n, vel, noteHold, noteTail), nil)
			}
		}
	}
	// a pattern alone in its role's slot, one of no role in the free layer
	for _, p := range held {
		var slots [audio.MusicSlots]audio.PatternID
		slot := 2
		if p.Role != audio.RoleNone {
			slot = int(p.Role - audio.RoleRhythm)
		}
		slots[slot] = p.ID
		buf, err := eng.RenderMusic(slots, audio.DefaultBPM, p.Meter, bars, audio.DefaultRootNote, audio.ScalePhrygian, nil, seed)
		render("pattern-"+p.Name, buf, err)
	}
	// the song: the patterns by name, a slot each, four at a time
	slices.SortStableFunc(held, func(a, b *audio.Pattern) int { return cmp.Compare(a.Name, b.Name) })
	var timeline [][audio.MusicSlots]audio.PatternID
	for four := range slices.Chunk(held, audio.MusicSlots) {
		var slots [audio.MusicSlots]audio.PatternID
		for i, p := range four {
			slots[i] = p.ID
		}
		for range bars {
			timeline = append(timeline, slots)
		}
	}
	song, err := eng.RenderTimeline(timeline, audio.DefaultBPM, audio.Meter{}, audio.DefaultRootNote, audio.ScalePhrygian, nil, seed)
	render("song", song, err)

	sum := sha256.New()
	for _, f := range []struct{ name, text string }{{"patterns.txt", pats.String()}, {"sounds.txt", snds.String()}, {"index.txt", index.String()}} {
		check(os.WriteFile(filepath.Join(dir, f.name), []byte(f.text), 0o644))
		sum.Write([]byte(f.text))
	}
	fmt.Printf("%d patterns, %d events, %d sounds, %d voices, %d samples in the song; sha256 %x\n",
		len(held), events, len(audio.RegisteredSounds()), len(audio.RegisteredVoices()), len(song), sum.Sum(nil))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "snapshot:", err)
		os.Exit(1)
	}
}

// fileRune keeps a render's name to what a file name holds anywhere
func fileRune(r rune) rune {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r) {
		return r
	}
	return '_'
}

// entry is a render's line of the index: its length in samples, its peak and
// its first half second's RMS in dB re full scale, and the sha256 of its
// samples as little-endian float64 bits
func entry(name string, buf []float64) string {
	var peak, sq float64
	var b [8]byte
	h, head := sha256.New(), min(len(buf), audio.AudioSampleRate/2)
	for i, v := range buf {
		peak = max(peak, math.Abs(v))
		if i < head {
			sq += v * v
		}
		binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
		h.Write(b[:])
	}
	db := func(x float64) float64 { return 20 * math.Log10(x) }
	return fmt.Sprintf("%s %d %.2f %.2f %x\n", name, len(buf), db(peak), db(math.Sqrt(sq/float64(max(head, 1)))), h.Sum(nil))
}

// dump lists a table under path: the values it sets on one line, keyed as its
// document keys them, then each table and list it holds on lines of their own.
// A list's element carries its index unless it holds values alone, as an event
// does, so adding one moves no other line.
func dump(w *strings.Builder, path string, v reflect.Value) {
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	type sub struct {
		path string
		v    reflect.Value
	}
	line := []string{path + ":"}
	var subs []sub
	for f, fv := range v.Fields() {
		key, opts, _ := strings.Cut(f.Tag.Get("toml"), ",")
		if !f.IsExported() || fv.IsZero() && (opts == "omitempty" || fv.Kind() == reflect.Pointer) {
			continue
		}
		key = cmp.Or(key, f.Name)
		switch at := path + "." + key; {
		case table(f.Type):
			subs = append(subs, sub{at, fv})
		case f.Type.Kind() == reflect.Slice && table(f.Type.Elem()):
			for i := range fv.Len() {
				if leaf(f.Type.Elem()) {
					subs = append(subs, sub{at, fv.Index(i)})
				} else {
					subs = append(subs, sub{at + "[" + strconv.Itoa(i) + "]", fv.Index(i)})
				}
			}
		default:
			line = append(line, key+"="+value(fv))
		}
	}
	fmt.Fprintln(w, strings.Join(line, " "))
	for _, s := range subs {
		dump(w, s.path, s.v)
	}
}

func elem(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func table(t reflect.Type) bool { return elem(t).Kind() == reflect.Struct }

// leaf reports a table that holds no table or list of them
func leaf(t reflect.Type) bool {
	for f := range elem(t).Fields() {
		if table(f.Type) || f.Type.Kind() == reflect.Slice && table(f.Type.Elem()) {
			return false
		}
	}
	return true
}

// value writes a value so that it reads back exactly: a float in the fewest
// digits that give its bits again
func value(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return strconv.Quote(v.String())
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, v.Type().Bits())
	case reflect.Slice:
		parts := make([]string, v.Len())
		for i := range parts {
			parts[i] = value(v.Index(i))
		}
		return "[" + strings.Join(parts, " ") + "]"
	}
	return fmt.Sprint(v.Interface())
}
