package audio

import (
	"math"
	"slices"
	"testing"
)

// TestStruckSoundPlaysAsADrum: a sound from struck genes registers, a kit binds a
// drum to it, and a hit of it rendered alone is what the drum's voice plays at
// the hit's velocity, take by take in each velocity layer
func TestStruckSoundPlaysAsADrum(t *testing.T) {
	ae := startNullEngine(t)
	d := StruckFromGenes(slices.Repeat([]float64{0.5}, StruckGeneCount))
	d.Name = "struck"
	if _, err := ae.DefineSound(d); err != nil {
		t.Fatal(err)
	}
	if err := ae.SetKit(map[string]string{"snare": "struck"}); err != nil {
		t.Fatal(err)
	}
	var k kit
	k.bind(ae.drumSet(ae.config.SoundShapes))
	for _, vel := range []float64{0.4, 0.7, 1} {
		v := NewDrumVoice(&k, InstrSnare)
		for n := range 7 {
			v.Trigger(VoiceParams{Velocity: vel})
			var played []float64
			for s := v.Sample(); v.Active(); s = v.Sample() {
				played = append(played, s)
			}
			hit, err := RenderHit(d, SoundShape{}, vel, n)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(hit, played) {
				t.Fatalf("velocity %v take %d: the hit alone is not what the drum played", vel, n)
			}
		}
	}
}

// TestStruckGenesKeepTheirSources: each range sits within the bound doc/audio.md
// §5 gives it, and every corner of the genome is a sound the engine renders
func TestStruckGenesKeepTheirSources(t *testing.T) {
	g := func(i int) struckGene { return struckGenes[i] }
	top := float64(RipRate) / 2 // where the hit's frames end, and the frequency a sound may name
	walk := 1 + StruckFromGenes(nil).PitchWalk/2
	drive := voiceGenes[slices.IndexFunc(voiceGenes[:], func(v voiceGene) bool { return v.name == "drive" })]
	for _, c := range []struct {
		source string
		ok     bool
	}{
		{"a frequency starts where the frames do", g(struckPitch).min == toneEdges[0] && g(struckNoiseHz).min == toneEdges[0]},
		{"the plate's top mode walked up ends under the frames' top", g(struckPitch).max*plateModes[len(plateModes)-1]*walk < top},
		{"the band raised by its stroke ends under the frames' top", g(struckNoiseHz).max*1.1 < top},
		{"the widest band is the frames' span", g(struckNoiseWidth).max <= math.Log2(top/toneEdges[0])},
		{"a ring shorter than a frame is the attack's", g(struckDecay).min <= RipFrameSec && g(struckNoiseDecay).min <= RipFrameSec},
		{"the fifth burst starts within the hit's frames", 4*(struckBurstLen+g(struckGap).max) <= toneReach*RipFrameSec},
		{"the drive is a voice's", g(struckDrive).min == drive.min && g(struckDrive).max == drive.max && g(struckDrive).log == drive.log},
	} {
		if !c.ok {
			t.Errorf("%s: no", c.source)
		}
	}
	for c := range 1 << StruckGeneCount {
		genes := make([]float64, StruckGeneCount)
		for i := range genes {
			genes[i] = float64(c >> i & 1)
		}
		d := StruckFromGenes(genes)
		d.Name = "corner"
		if err := ValidateSound(d); err != nil {
			t.Fatalf("corner %0*b: %v", StruckGeneCount, c, err)
		}
	}
}
