package audio

import (
	"cmp"
	"math"
	"slices"
	"sync"
)

// kit binds the instruments to what they play: each drum to its rendered takes,
// the full-velocity layer aliasing the cache, and each tonal instrument to its
// voice. Voices read through it at trigger time, which is what lets a hot-reload
// reach every voice in every PatternPlayer.
type kit struct {
	ids    [InstrumentCount]SoundID
	takes  [InstrumentCount]drumTakes
	voices []*VoiceDef // by InstrumentType; a drum's and an unset voice's nil
	tuning float64     // the ratio every tonal note's frequency is scaled by; zero reads as one, A440
	only   []bool      // by InstrumentType, the instruments that sound; nil for all
}

// drumTakes is a drum's rendered set: its variants once a velocity layer, the
// layers ascending as their velocities
type drumTakes struct {
	vel    []float64
	layers [][]floatBuffer
}

// take is variant n of the drum's layer nearest vel, nil when it has none: a
// harder hit plays a brighter take as well as a louder one
func (k *kit) take(i InstrumentType, vel float64, n uint32) floatBuffer {
	t := &k.takes[i]
	l := 0
	for l+1 < len(t.layers) && vel > (t.vel[l]+t.vel[l+1])/2 {
		l++
	}
	if l >= len(t.layers) || len(t.layers[l]) == 0 {
		return nil
	}
	return t.layers[l][n%uint32(len(t.layers[l]))]
}

// sounds says whether an instrument plays on this kit: every one unless only
// names some, as a render of one line of a song does
func (k *kit) sounds(i InstrumentType) bool {
	return k == nil || k.only == nil || (i >= 0 && int(i) < len(k.only) && k.only[i])
}

// ratio is the tuning's frequency ratio, one when unset and for the nil kit
// tests play on
func (k *kit) ratio() float64 {
	if k == nil || k.tuning <= 0 {
		return 1
	}
	return k.tuning
}

var drumSoundNames = [InstrumentCount]string{
	InstrKick:  "kick",
	InstrSnare: "snare",
	InstrHihat: "hihat",
	InstrClap:  "clap",
}

// buildKit renders the drums' velocity layers from the frozen registry, each
// drum the sound names gives it, under the shapes the cache was rendered with, a
// drum a goroutine
func buildKit(c *soundCache, shapes map[string]SoundShape, names [InstrClap + 1]string) *kit {
	k := &kit{voices: voiceTable()}
	var wg sync.WaitGroup
	for i := InstrumentType(0); i <= InstrClap; i++ {
		id := SoundIDByName(names[i])
		k.ids[i] = id
		if d := SoundDefByName(names[i]); d != nil {
			wg.Go(func() { k.takes[i] = renderTakes(d, shapes[d.Name], c.variants(id)) })
		}
	}
	wg.Wait()
	return k
}

// offlineDrum is a drum's takes kept from one offline render for the next, good
// while the registry holds the same definition under the same shape
type offlineDrum struct {
	d     *SoundDef
	shape SoundShape
	takes drumTakes
}

// offlineTakes is a drum's takes for an offline render, rendered again only when
// its definition or shape changed: a corpus renders thousands of loops on one kit
func (ae *AudioEngine) offlineTakes(i InstrumentType, d *SoundDef, shape SoundShape) drumTakes {
	ae.kitMu.Lock()
	defer ae.kitMu.Unlock()
	if o := &ae.offline[i]; o.d != d || o.shape != shape {
		*o = offlineDrum{d: d, shape: shape, takes: renderTakes(d, shape, nil)}
	}
	return ae.offline[i].takes
}

// rebind points the kit at a re-rendered set. Mix-goroutine confined. Voices
// already ringing hold their own buffer alias and complete on the old take;
// the next trigger picks up the new one.
func (k *kit) rebind(id SoundID, t drumTakes) {
	if id == SoundNone {
		return
	}
	for i := range k.ids {
		if k.ids[i] == id {
			k.takes[i] = t
		}
	}
}

// voice is the instrument's voice, the default sine for one no voice defines; a
// nil kit, which tests play on, has none.
func (k *kit) voice(i InstrumentType) *VoiceDef {
	if k != nil && i >= 0 && int(i) < len(k.voices) && k.voices[i] != nil {
		return k.voices[i]
	}
	return &defaultVoice
}

// setVoice installs a voice, growing the table for an instrument a document
// added. Mix-goroutine confined; a note already sounding keeps the spec it began on.
func (k *kit) setVoice(i InstrumentType, d *VoiceDef) {
	if i < 0 {
		return
	}
	for int(i) >= len(k.voices) {
		k.voices = append(k.voices, nil)
	}
	k.voices[i] = d
}

// kitDrums is the sound each drum plays and its takes, what SetKit hands the mixer
type kitDrums struct {
	ids   [InstrClap + 1]SoundID
	takes [InstrClap + 1]drumTakes
}

func (k *kit) bind(d *kitDrums) {
	for i := range d.ids {
		k.ids[i], k.takes[i] = d.ids[i], d.takes[i]
	}
}

// ToneBands is how many bands a rip reads a drum's tone in, toneEdges' bands
const ToneBands = 12

// The tone's bands, a hit's frames (toneN samples at RipRate every RipHop) and
// the window its level is read over
var toneEdges = [ToneBands + 1]float64{30, 60, 100, 160, 250, 400, 630, 1000, 1600, 2500, 4000, 6300, 11000}

const (
	toneN      = 1024
	toneReach  = 8    // frames from a hit's own its added power is read over
	toneLevel  = 0.06 // seconds a hit's level is read over, and a lone hihat's distance from any other drum
	toneFewest = 4    // hits a drum is read over at the fewest
)

// RipTone is how a drum sounds in a recording, read over the quarter of its hits
// with the least sounding before them: the median power a hit adds in each tone
// band in dB, and the median A-weighted level over 60 ms from a hit, a hihat's
// from the hits no other drum lands within 60 ms of. Hits and Levels count the
// hits read, none where fewer than four were.
type RipTone struct {
	Bands        [ToneBands]float64
	Level        float64
	Hits, Levels int
}

// kitMatch is how each drum's measured difference becomes its eq, the rules the
// ear test left: a band under hold takes the first band's value at or over it, a
// band under from stays flat, one over top gains limit dB at most, and the whole
// scales by scale; lo and hi bound the drum's level against the kick's, in dB.
var kitMatch = [InstrClap]struct{ from, hold, top, limit, scale, lo, hi float64 }{
	InstrKick:  {top: 630, limit: 8, scale: 1},
	InstrSnare: {hold: 100, scale: 0.875, lo: -2, hi: 5},
	InstrHihat: {from: 1000, scale: 1, lo: -20, hi: -8},
}

// kitFloor and kitCeiling bound every band of a matched drum's eq, in dB
const kitFloor, kitCeiling = -24, 30

// toneCentre is band b's centre in Hz, the geometric mean of its edges
func toneCentre(b int) float64 { return math.Sqrt(toneEdges[b] * toneEdges[b+1]) }

// drumTones reads each drum's tone in x at RipRate and its level in y at rate,
// the same sound, its hits starting at their seconds
func drumTones(x, y []float64, rate int, at [InstrClap][]float64) [InstrClap]RipTone {
	var out [InstrClap]RipTone
	for d, times := range at {
		frames := make([]int, len(times))
		for i, t := range times {
			frames[i] = int(math.Round(t / RipFrameSec))
		}
		out[d].Bands, out[d].Hits = tone(x, frames)
	}
	drumLevels(aWeight(y, rate), rate, at, &out)
	return out
}

// hitStarts moves each hit, in seconds, to the start of the frame within four of it
// whose RipHop samples of x at RipRate hold the most more energy than the frame's
// before: where the hit starts, which a front end places a frame or two early
func hitStarts(x []float64, at [InstrClap][]float64) (out [InstrClap][]float64) {
	energy := func(u int) (e float64) {
		for _, v := range x[min(u*RipHop, len(x)):min((u+1)*RipHop, len(x))] {
			e += v * v
		}
		return e
	}
	for d, times := range at {
		for _, t := range times {
			f := int(math.Round(t / RipFrameSec))
			best, rise := f, math.Inf(-1)
			for u := max(f-4, 1); u <= f+4; u++ {
				if r := energy(u) - energy(u-1); r > rise {
					best, rise = u, r
				}
			}
			out[d] = append(out[d], float64(best)*RipFrameSec)
		}
	}
	return out
}

// tone is the median over the quietest quarter of hits of the band power each
// adds, its frames from its own less the five ending four before it, in dB
func tone(x []float64, hits []int) (bands [ToneBands]float64, n int) {
	last := (len(x) - toneN + RipHop - 1) / RipHop
	s := newSTFT(toneN)
	bin := make([]int, toneN/2+1)
	for k := range bin {
		f := float64(k) * RipRate / toneN
		bin[k] = -1
		for b := range ToneBands {
			if toneEdges[b] <= f && f < toneEdges[b+1] {
				bin[k] = b
			}
		}
	}
	mean := func(from, to int) (p [ToneBands]float64) {
		for t := from; t < to; t++ {
			for k, m := range s.at(x, t*RipHop+toneN/2) {
				if bin[k] >= 0 {
					p[bin[k]] += m * m / float64(to-from)
				}
			}
		}
		return p
	}
	type read struct {
		t      int
		before [ToneBands]float64
		sum    float64
	}
	var reads []read
	for _, t := range hits {
		if t < 10 || t+toneReach >= last {
			continue
		}
		r := read{t: t, before: mean(t-9, t-4)}
		for _, p := range r.before {
			r.sum += p
		}
		reads = append(reads, r)
	}
	if len(reads) < toneFewest {
		return bands, 0
	}
	slices.SortStableFunc(reads, func(a, b read) int { return cmp.Compare(a.sum, b.sum) })
	reads = reads[:max(len(reads)/4, toneFewest)]
	added := make([][ToneBands]float64, len(reads))
	for i, r := range reads {
		after := mean(r.t, r.t+toneReach)
		for b := range after {
			added[i][b] = max(after[b]-r.before[b], 1e-9)
		}
	}
	for b := range bands {
		v := make([]float64, len(added))
		for i := range added {
			v[i] = added[i][b]
		}
		bands[b] = 10 * math.Log10(median(v))
	}
	return bands, len(reads)
}

// drumLevels sets each drum's level in y at rate, A-weighted, its hits at their
// seconds, ascending: the median power over 60 ms from each of the quarter of its
// hits quietest over the 70 ms ending 10 ms before them, a hihat's alone
func drumLevels(y []float64, rate int, at [InstrClap][]float64, out *[InstrClap]RipTone) {
	dur := float64(len(y)) / float64(rate)
	power := func(from, to float64) float64 {
		a, b := max(int(from*float64(rate)), 0), min(max(int(to*float64(rate)), 1), len(y))
		var e float64
		for _, v := range y[min(a, b):b] {
			e += v * v
		}
		return 10 * math.Log10(e/float64(max(b-a, 1))+1e-12)
	}
	near := func(t float64, others []float64) bool {
		i, _ := slices.BinarySearch(others, t)
		return i < len(others) && others[i]-t <= toneLevel || i > 0 && t-others[i-1] <= toneLevel
	}
	type read struct{ t, before float64 }
	for d, times := range at {
		var reads []read
		for _, t := range times {
			if t > 0.1 && t < dur-0.1 && (d != int(InstrHihat) || !near(t, at[InstrKick]) && !near(t, at[InstrSnare])) {
				reads = append(reads, read{t, power(t-0.08, t-0.01)})
			}
		}
		if len(reads) < toneFewest {
			out[d].Level, out[d].Levels = 0, 0
			continue
		}
		slices.SortStableFunc(reads, func(a, b read) int { return cmp.Compare(a.before, b.before) })
		reads = reads[:max(len(reads)/4, toneFewest)]
		lv := make([]float64, len(reads))
		for i, r := range reads {
			lv[i] = power(r.t, r.t+toneLevel)
		}
		out[d].Level, out[d].Levels = median(lv), len(reads)
	}
}

// median is the middle of v, the mean of the two middles of an even count,
// sorting v in place
func median(v []float64) float64 {
	slices.Sort(v)
	n := len(v)
	return (v[(n-1)/2] + v[n/2]) / 2
}

// matched reports a drum whose tone the rip read and the registry holds a sound for
func (r *Rip) matched(d InstrumentType) bool {
	return r.Tone[d].Hits > 0 && SoundDefByName(drumSoundNames[d]) != nil
}

// Kit is the registered drums matched to the recording's (Rip.Tone), a sound for
// each drum Song names one for, <name>_<drum>: the drum with an eq of its tone in
// the recording less its tone in a render of the rip's hits, under kitMatch's
// rules, and its level moved to stand against the kick as in the recording,
// within kitMatch's bounds, the loudest at a norm of one at most
func (r *Rip) Kit(name string) []*SoundDef {
	var plain, kit [InstrClap]*SoundDef
	for d := range plain {
		plain[d] = SoundDefByName(drumSoundNames[d])
	}
	hits := r.Hits()
	var at [InstrClap][]float64
	for _, h := range hits {
		at[h.Drum] = append(at[h.Drum], h.Start)
	}
	y := beat(hits, plain)
	x := resample(y, AudioSampleRate, RipRate)
	own := drumTones(x, y, AudioSampleRate, hitStarts(x, at))
	var out []*SoundDef
	for d, p := range plain {
		kit[d] = p
		if !r.matched(InstrumentType(d)) {
			continue
		}
		m := p.Clone()
		m.Name = name + "_" + drumSoundNames[d]
		if own[d].Hits > 0 {
			rule := kitMatch[d]
			eq := Proc{Kind: "eq", Bands: make([]Band, ToneBands)}
			held := 0
			for toneCentre(held) < rule.hold {
				held++
			}
			for b := range eq.Bands {
				c := toneCentre(b)
				v := r.Tone[d].Bands[b] - own[d].Bands[b]
				if c < rule.hold {
					v = r.Tone[d].Bands[held] - own[d].Bands[held]
				}
				if c < rule.from {
					v = 0
				}
				if rule.top > 0 && c > rule.top {
					v = min(v, rule.limit)
				}
				eq.Bands[b] = Band{Freq: c, DB: min(max(v*rule.scale, kitFloor), kitCeiling)}
			}
			m.Chain = append(m.Chain, eq)
		}
		kit[d] = m
		out = append(out, m)
	}
	if len(out) == 0 || r.Tone[InstrKick].Levels == 0 {
		return out
	}
	// read twice: a drum's level holds the tails of the others under it; each read
	// finds the hits' starts again, as an eq moves where a hit's energy rises
	for range 2 {
		var lv [InstrClap]RipTone
		y := beat(hits, kit)
		drumLevels(aWeight(y, AudioSampleRate), AudioSampleRate, hitStarts(resample(y, AudioSampleRate, RipRate), at), &lv)
		for d, m := range kit {
			rule := kitMatch[d]
			if d == int(InstrKick) || m == plain[d] || r.Tone[d].Levels == 0 || lv[d].Levels == 0 || lv[InstrKick].Levels == 0 {
				continue
			}
			want := min(max(r.Tone[d].Level-r.Tone[InstrKick].Level, rule.lo), rule.hi)
			louder(m, want-(lv[d].Level-lv[InstrKick].Level))
		}
	}
	var loud float64
	for _, m := range out {
		if !m.Raw {
			loud = max(loud, 20*math.Log10(m.Norm))
		}
	}
	for _, m := range out {
		if loud > 0 {
			louder(m, -loud)
		}
	}
	return out
}

// louder raises a sound by db: its norm, or a gain after its chain when it is raw
func louder(d *SoundDef, db float64) {
	g := math.Pow(10, db/20)
	if d.Raw {
		d.Chain = append(d.Chain, Proc{Kind: "gain", Amount: g})
		return
	}
	d.Norm = normOf(d.Norm) * g
}

// beat renders hits at AudioSampleRate from the recording's start on drums, a
// drum's take as a drum voice plays it at the hit's velocity
func beat(hits []RipHit, drums [InstrClap]*SoundDef) []float64 {
	var k kit
	for d, s := range drums {
		if s != nil {
			k.takes[d] = renderTakes(s, SoundShape{}, nil)
		}
	}
	var end float64
	for _, h := range hits {
		end = max(end, h.Start)
	}
	out := make([]float64, int((end+1)*AudioSampleRate))
	var n [InstrClap]uint32
	for _, h := range hits {
		if take := k.take(h.Drum, h.Vel, n[h.Drum]); take != nil && h.Start >= 0 {
			mixAt(out, take, int(h.Start*AudioSampleRate), h.Vel)
			n[h.Drum]++
		}
	}
	return out
}
