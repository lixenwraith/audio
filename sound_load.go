package audio

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"reflect"
	"slices"
	"strings"

	"github.com/lixenwraith/toml"
	"github.com/lixenwraith/toml/macro"
)

// LoadSoundsTOML parses one document held in memory, so an include, which needs
// a filesystem, is refused; the engine reads through LoadSoundsFS. The result
// holds every sound and voice that validated; err is a join of the ones that did
// not, so one bad spec does not discard the file.
func LoadSoundsTOML(data []byte) (*SoundSpecFile, error) {
	f, err := decodeSpecFile(data)
	if err != nil {
		return nil, err
	}
	if len(f.Include) > 0 {
		return nil, errors.New("sound spec: include requires LoadSoundsFS")
	}
	return partitionValid(f)
}

// LoadSoundsFS loads documents from a filesystem capability, resolving nested
// includes. Includes are loaded depth-first and before the including file's own
// definitions, so an including file overrides what it includes.
func LoadSoundsFS(fsys fs.FS, names ...string) (*SoundSpecFile, error) {
	l := &specLoader{fsys: fsys, seen: make(map[string]bool), out: &SoundSpecFile{}}
	for _, n := range names {
		if err := l.load(path.Clean(n), 0); err != nil {
			return nil, err
		}
	}
	return partitionValid(l.out)
}

// MarshalSoundsFile re-emits a document whole, its include directives intact.
// MarshalSounds and MarshalVoices emit entries of one kind and no includes, which
// flattens a multi-file spec set on first save. Arrays keep their order; scalar
// keys are sorted. Reloading the output yields identical audio.
func MarshalSoundsFile(f *SoundSpecFile) ([]byte, error) {
	return toml.Marshal(*f)
}

// SaveSoundsFile is MarshalSoundsFile over src, the bytes the document was
// loaded from, as SavePatternFile is MarshalPatternFile's; include is f's, set
// in place where it differs from src's.
func SaveSoundsFile(src []byte, f *SoundSpecFile) ([]byte, error) {
	return saveOver(src, f, decodeSpecFile, "include", "sound", "voice")
}

func MarshalSounds(defs []*SoundDef) ([]byte, error) {
	return toml.Marshal(SoundSpecFile{Sound: defs})
}

func MarshalVoices(defs []*VoiceDef) ([]byte, error) {
	return toml.Marshal(SoundSpecFile{Voice: defs})
}

// savedEntry is one table of a document's arrays on its way to a save
type savedEntry struct {
	key  string
	tree map[string]any // as Marshal writes it
	note []byte         // the comment above it
	text []byte         // as the save writes it
}

// saveOver writes f's entries over the source they were loaded from: src
// without the arrays keys name, then each entry anew, an invocation of src's
// macros where one expands to it, under the comment its name had, then the
// comment that closed src. What does not load back to what Marshal writes of f
// is written as Marshal writes it, an entry and then the whole.
func saveOver[F any](src []byte, f *F, load func([]byte) (*F, error), keys ...string) ([]byte, error) {
	flat, err := toml.Marshal(*f)
	if err != nil {
		return nil, err
	}
	tree, err := toml.NewParser(flat).Parse()
	doc, derr := toml.ParseDocument(src)
	if err != nil || derr != nil {
		return flat, nil
	}
	was, notes := doc.Tree(), make([][][]byte, len(keys))
	for i, k := range keys {
		notes[i] = doc.Comments(toml.Key{k})
	}
	// A key flat holds as a value is the session's, as include is: set where
	// src's differs, deleted where flat has none. One holding tables goes: its
	// entries follow.
	for _, k := range keys {
		v, ok := tree[k]
		_, entries := v.([]map[string]any)
		_, tables := doc.Tree()[k].([]map[string]any)
		if !ok || entries || tables {
			if _, err := doc.Delete(toml.Key{k}); err != nil {
				return flat, nil
			}
		}
		if ok && !entries && !reflect.DeepEqual(doc.Tree()[k], v) {
			if err := doc.Set(toml.Key{k}, v); err != nil {
				return flat, nil
			}
		}
	}
	// One newline ends what stays, so a saved document's is the same bytes again;
	// clipped, since Bytes is src itself until an edit
	frame := bytes.TrimRight(doc.Bytes(), " \t\r\n")
	if len(frame) > 0 {
		frame = append(slices.Clip(frame), '\n')
	}
	// Define takes [macros] out of the tree it is given, so it gets a copy
	set, fold := macro.NewSet(), false
	if defs := maps.Clone(doc.Tree()); defs["macros"] != nil {
		fold = set.Define(defs) == nil
	}
	block := func(key string, v any) []byte {
		text, _ := toml.Marshal(map[string]any{key: []any{v}})
		return text
	}
	var ents []savedEntry
	for i, k := range keys {
		// A table's comment follows the entry the table expands to, by name; a
		// table that expands to none or to several loses its own
		above := map[string][][]byte{}
		tables, _ := was[k].([]map[string]any)
		for j, c := range notes[i] {
			if len(c) == 0 || j >= len(tables) {
				continue
			}
			exp, _ := set.Expand(map[string]any{k: []any{tables[j]}})
			if one, _ := exp[k].([]any); len(one) == 1 {
				above[entryName(one[0])] = append(above[entryName(one[0])], c)
			}
		}
		list, _ := tree[k].([]map[string]any)
		for _, t := range list {
			e, v := savedEntry{key: k, tree: t}, any(t)
			if q := above[entryName(t)]; len(q) > 0 {
				e.note, above[entryName(t)] = q[0], q[1:]
			}
			// each entry alone, so that an edit moves no entry but its own
			if fold {
				if folded, err := set.Fold(t); err == nil {
					v = folded
				}
			}
			e.text = block(k, v)
			ents = append(ents, e)
		}
	}
	// The comment that closed src stays the file's last, below the entries. A
	// source of comments alone has a head and no close.
	closed := src
	if len(was) == 0 {
		closed = nil
	}
	var foot []byte
	if len(ents) > 0 {
		frame, foot = ending(frame, closed)
	}
	write := func() []byte {
		out := slices.Clone(frame)
		for _, e := range ents {
			out = append(under(out, e.note), e.text...)
		}
		if len(foot) > 0 {
			out = under(out, foot)
		}
		return out
	}
	same := func(out []byte) bool {
		got, err := reloaded(load, out)
		return err == nil && bytes.Equal(got, flat)
	}
	// Nothing of src stayed where the bytes are flat's: there is nothing to load
	out := write()
	if bytes.Equal(out, flat) {
		return flat, nil
	}
	if !same(out) {
		// Only a document that differs has each entry loaded alone
		texts, plain := make([][]byte, len(ents)), make([][]byte, len(ents))
		for i, e := range ents {
			texts[i], plain[i] = e.text, block(e.key, e.tree)
		}
		for _, i := range strays(frame, texts, plain, load) {
			ents[i].text = plain[i]
		}
		if out = write(); !same(out) {
			return flat, nil
		}
	}
	return out, nil
}

// ending parts frame, what stays of src, at the comment lines that end it under
// a blank line. Those that end src too closed the file: they are foot, to go
// below the entries. The others stay, and each run of blank lines above or
// among either is cut to one: a table written below a comment takes it on a
// tie, so with the runs cut once the bytes hold at every save after.
func ending(frame, src []byte) (rest, foot []byte) {
	const blank = " \t\r\n"
	f, s := bytes.TrimRight(frame, blank), bytes.TrimRight(src, blank)
	alike := 0
	for alike < len(f) && alike < len(s) && f[len(f)-1-alike] == s[len(s)-1-alike] {
		alike++
	}
	lines := bytes.SplitAfter(f, []byte("\n"))
	empty := func(i int) bool { return len(bytes.TrimSpace(lines[i])) == 0 }
	// lines[loose:] are those comments from the blank line above them, and
	// lines[closed:] the ones that end src too
	loose, closed, at := len(lines), len(lines), len(f)
	for i := len(lines) - 1; i >= 0 && (empty(i) || bytes.TrimSpace(lines[i])[0] == '#'); i-- {
		if at -= len(lines[i]); empty(i) {
			loose = i
		} else if (i == 0 || empty(i-1)) && len(f)-at <= alike {
			closed = i
		}
	}
	if loose = min(loose, closed); loose == len(lines) {
		return frame, nil
	}
	// cut is lines[from:to], one newline ending them as it ends what stays;
	// without runs, as Comments gives a table's: no blank line around them and
	// each run of blank lines among them cut to one
	cut := func(from, to int, runs bool) (out []byte) {
		for i := from; i < to; i++ {
			if runs || !empty(i) || len(out) > 0 && !empty(i-1) {
				out = append(out, lines[i]...)
			}
		}
		if out = bytes.TrimRight(out, blank); len(out) > 0 {
			out = append(out, '\n')
		}
		return out
	}
	rest = cut(0, loose, true)
	if stay := cut(loose, closed, false); len(stay) > 0 {
		rest = under(rest, stay)
	}
	return rest, cut(closed, len(lines), false)
}

// entryName is the name a table of a document's arrays holds, "" for none
func entryName(v any) string {
	t, _ := v.(map[string]any)
	name, _ := t["name"].(string)
	return name
}

// under is doc with text below it, a blank line between where doc holds any
func under(doc, text []byte) []byte {
	if len(doc) > 0 {
		doc = append(doc, '\n')
	}
	return append(doc, text...)
}

// reloaded is data as Marshal writes what load reads of it, before any
// validation: the same bytes for two documents that mean the same
func reloaded[F any](load func([]byte) (*F, error), data []byte) ([]byte, error) {
	f, err := load(data)
	if err != nil {
		return nil, err
	}
	return toml.Marshal(*f)
}

// strays lists the entries of a, by index, that alone under frame do not load
// to what b's do: the entries a save writes as Marshal does
func strays[F any](frame []byte, a, b [][]byte, load func([]byte) (*F, error)) []int {
	var out []int
	for i := range a {
		x, xerr := reloaded(load, under(slices.Clone(frame), a[i]))
		y, yerr := reloaded(load, under(slices.Clone(frame), b[i]))
		if xerr != nil || yerr != nil || !bytes.Equal(x, y) {
			out = append(out, i)
		}
	}
	return out
}

type specLoader struct {
	fsys fs.FS
	seen map[string]bool
	n    int
	out  *SoundSpecFile
}

func (l *specLoader) load(name string, depth int) error {
	if depth > MaxIncludeDepth {
		return fmt.Errorf("sound spec %q: include depth exceeds %d", name, MaxIncludeDepth)
	}
	if !fs.ValidPath(name) {
		return fmt.Errorf("sound spec: invalid path %q", name)
	}
	if l.seen[name] {
		return fmt.Errorf("sound spec: include cycle at %q", name)
	}
	if l.n++; l.n > MaxSoundIncludes {
		return fmt.Errorf("sound spec: more than %d files", MaxSoundIncludes)
	}
	l.seen[name] = true

	data, err := fs.ReadFile(l.fsys, name)
	if err != nil {
		return fmt.Errorf("sound spec %q: %w", name, err)
	}
	f, err := decodeSpecFile(data)
	if err != nil {
		return fmt.Errorf("%q: %w", name, err)
	}
	for _, inc := range f.Include {
		ref, err := resolveInclude(name, inc)
		if err != nil {
			return err
		}
		if err := l.load(ref, depth+1); err != nil {
			return err
		}
	}
	l.out.Sound = append(l.out.Sound, f.Sound...)
	l.out.Voice = append(l.out.Voice, f.Voice...)
	return nil
}

// resolveInclude keeps every reference inside the supplied fs.FS root.
func resolveInclude(base, ref string) (string, error) {
	if path.IsAbs(ref) || strings.HasPrefix(ref, "..") {
		return "", fmt.Errorf("sound spec %q: include %q escapes root", base, ref)
	}
	p := path.Clean(path.Join(path.Dir(base), ref))
	if !fs.ValidPath(p) {
		return "", fmt.Errorf("sound spec %q: include %q escapes root", base, ref)
	}
	return p, nil
}

// decodeSpecFile parses once to map[string]any, expands the document's own macros,
// rejects unknown keys, then decodes. The decoder ignores unknown keys, which would
// turn a typo into a silent zero value; the strict pass reflects over the struct
// tags so there is no schema to keep in sync.
func decodeSpecFile(data []byte) (*SoundSpecFile, error) {
	raw, err := toml.NewParser(data).Parse()
	if err != nil {
		return nil, fmt.Errorf("sound spec: %w", err)
	}
	if raw, err = macro.ExpandDocument(raw); err != nil {
		return nil, fmt.Errorf("sound spec: %w", err)
	}
	if err := checkKeys("", raw, reflect.TypeOf(SoundSpecFile{})); err != nil {
		return nil, fmt.Errorf("sound spec: %w", err)
	}
	var f SoundSpecFile
	if err := toml.Decode(raw, &f); err != nil {
		return nil, fmt.Errorf("sound spec: %w", err)
	}
	return &f, nil
}

func checkKeys(at string, v any, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		fields := make(map[string]reflect.Type, t.NumField())
		for f := range t.Fields() {
			if f.PkgPath != "" {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			fields[name] = f.Type
		}
		for k, sub := range m {
			ft, ok := fields[k]
			if !ok {
				return fmt.Errorf("%s: unknown key %q", pathOr(at, "document"), k)
			}
			if err := checkKeys(at+"."+k, sub, ft); err != nil {
				return err
			}
		}
	case reflect.Slice:
		switch a := v.(type) {
		case []any:
			for i, e := range a {
				if err := checkKeys(fmt.Sprintf("%s[%d]", at, i), e, t.Elem()); err != nil {
					return err
				}
			}
		case []map[string]any:
			for i := range a {
				if err := checkKeys(fmt.Sprintf("%s[%d]", at, i), a[i], t.Elem()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func pathOr(s, dflt string) string {
	if s == "" {
		return dflt
	}
	return strings.TrimPrefix(s, ".")
}

// partitionValid keeps the sounds and voices that validate and joins the errors
// of those that do not.
func partitionValid(in *SoundSpecFile) (*SoundSpecFile, error) {
	out := &SoundSpecFile{Include: in.Include}
	var errs []error
	for _, d := range in.Sound {
		if d == nil {
			continue
		}
		if err := ValidateSound(d); err != nil {
			errs = append(errs, err)
			continue
		}
		out.Sound = append(out.Sound, d)
	}
	for _, d := range in.Voice {
		if d == nil {
			continue
		}
		if err := ValidateVoice(d); err != nil {
			errs = append(errs, err)
			continue
		}
		out.Voice = append(out.Voice, d)
	}
	return out, errors.Join(errs...)
}
