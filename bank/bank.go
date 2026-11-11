// Package bank is the set vif and stapes ship: the sound effects, the drum kit,
// the tonal voices and the music patterns, embedded. Another embedder may ignore
// it and hand the engine its own; the tonal voices are the part meant to be
// shared. Each call parses afresh, so no two callers share a definition.
package bank

import (
	"embed"

	"github.com/lixenwraith/audio"
)

//go:embed *.toml
var files embed.FS

// soundFiles is the sound document's load order, a later file overriding an
// earlier one by name
var soundFiles = []string{"sfx.toml", "drums.toml", "voices.toml"}

// Sounds parses the shipped sound document: the sounds and the tonal voices.
func Sounds() (*audio.SoundSpecFile, error) {
	return audio.LoadSoundsFS(files, soundFiles...)
}

// Patterns parses the shipped music, whose roles, groups and tiers are the drawn
// arrangement; any pattern it drops is a broken build.
func Patterns() ([]*audio.Pattern, error) {
	data, err := MusicTOML()
	if err != nil {
		return nil, err
	}
	return audio.LoadPatternsTOML(data)
}

// MusicTOML is the shipped music document as written, its macros and comments
// with it: what a document seeded from Patterns is saved over
// (audio.SavePatternFile) to stay the bank's own. Each call returns a copy.
func MusicTOML() ([]byte, error) {
	return files.ReadFile("music.toml")
}
