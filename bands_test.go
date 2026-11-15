package audio

import (
	"math"
	"math/rand/v2"
	"testing"
)

// TestABandStreamGivesBandFramesWhateverTheBlocks: in blocks of one sample, of
// seven, of a thousand, of a second and of random sizes, a stream gives frame t
// once sample t·RipHop+RipBandsAhead is in and the rest at Close, the frames
// BandFrames reads of the whole recording bit for bit: a voice's note, two keys a
// semitone apart and a low string struck from silence.
func TestABandStreamGivesBandFramesWhateverTheBlocks(t *testing.T) {
	saw := &VoiceDef{Name: "v", Wave: "saw", Attack: 0.01, Sustain: 1, Release: 0.1}
	str := &VoiceDef{Name: "s", Wave: "string", Ring: 1, Bright: 0.2, Attack: 0.001, Release: 0.1}
	x := make([]float64, 5*RipRate)
	for i, v := range Resample(RenderNote(saw, 45, 0.8, 1.5, 0.1), AudioSampleRate, RipRate) {
		x[i] += v
	}
	for i := 2 * RipRate; i < 4*RipRate; i++ {
		s := float64(i) / RipRate
		x[i] += 0.3*math.Sin(2*math.Pi*NoteFreq(60)*s) + 0.3*math.Sin(2*math.Pi*NoteFreq(61)*s)
	}
	copy(x[4*RipRate+77:], Resample(RenderNote(str, 24, 0.8, 1, 0), AudioSampleRate, RipRate))
	whole, peak := BandFrames(x, RipRate), 0.0
	for _, v := range x {
		peak = max(peak, math.Abs(v))
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for _, block := range []func() int{func() int { return 1 }, func() int { return 7 }, func() int { return 1000 }, func() int { return RipRate }, func() int { return rng.IntN(RipRate) + 1 }} {
		s, n := NewBandStream(), 0
		var got [][]float64
		for n < len(x) {
			k := min(block(), len(x)-n)
			got, n = append(got, s.Write(x[n:n+k])...), n+k
			if want := max(n-RipBandsAhead+RipHop, 0) / RipHop; len(got) != want {
				t.Fatalf("%d frames given after %d samples, want %d", len(got), n, want)
			}
		}
		if got = append(got, s.Close()...); len(got) != len(whole) {
			t.Fatalf("%d frames given in all, BandFrames %d", len(got), len(whole))
		}
		for f, row := range got {
			for b, v := range row {
				if float32(math.Log1p(100*v/(peak*peak))) != whole[f][b] {
					t.Fatalf("frame %d band %d reads %g in a stream, %g whole", f, b, v, whole[f][b])
				}
			}
		}
	}
}

// TestEachBandIsHeardAtTheShortestWindowWhoseBinsFallThreeASemitone: the fine
// plane hears band k at the lowest level whose window's bins, RipRate/RipN apart
// halved at each level, span three over the semitone from its centre to band
// k+3's, the last level any band under, so ripLevelBand is the rule's
func TestEachBandIsHeardAtTheShortestWindowWhoseBinsFallThreeASemitone(t *testing.T) {
	for k := range RipBands {
		level := 0
		for level < ripBandLevels-1 && BandHz(float64(k+3))-BandHz(float64(k)) < 3*float64(RipRate)/RipN/float64(int(1)<<level) {
			level++
		}
		if got := ripBandLevel(k); got != level {
			t.Fatalf("band %d (%.1f Hz) is heard at level %d, the rule's %d", k, BandHz(float64(k)), got, level)
		}
	}
}

// TestAStruckLowNoteRisesWhereTheMelFramesDo: a low string struck off the frame
// grid, from silence and under a note held a fourth over it, rises most in the
// bands' dB, both planes' summed, within a frame of where it rises most in the
// mel frames', from A0 to E2
func TestAStruckLowNoteRisesWhereTheMelFramesDo(t *testing.T) {
	str := &VoiceDef{Name: "s", Wave: "string", Ring: 1, Bright: 0.2, Attack: 0.001, Release: 0.1}
	held := &VoiceDef{Name: "h", Wave: "saw", Cutoff: 2000, Attack: 0.01, Sustain: 1, Release: 0.1}
	db := func(v float32) float64 { return 10 * math.Log10(math.Expm1(float64(v))/100+1e-8) }
	// rise is the frame within six of near whose bands rose most from the one before
	rise := func(frames [][]float32, near int) int {
		at, most := 0, -1.0
		for f := near - 6; f <= near+6; f++ {
			var r float64
			for b, v := range frames[f] {
				r += max(db(v)-db(frames[f-1][b]), 0)
			}
			if r > most {
				at, most = f, r
			}
		}
		return at
	}
	for _, midi := range []int{21, 24, 28, 33, 40} {
		for _, under := range []bool{false, true} {
			for _, start := range []int{2*RipRate + 77, 2*RipRate + 200} {
				x := make([]float64, 4*RipRate)
				if under {
					copy(x, Resample(RenderNote(held, midi+5, 0.5, 4, 0), AudioSampleRate, RipRate))
				}
				for i, v := range Resample(RenderNote(str, midi, 0.8, 1, 0.5), AudioSampleRate, RipRate) {
					x[min(start+i, len(x)-1)] += v
				}
				near := start / RipHop
				if b, m := rise(BandFrames(x, RipRate), near), rise(MelFrames(x, RipRate), near); b < m-1 || b > m+1 {
					t.Errorf("key %d struck at sample %d, under a held fourth %v: the bands rise most at frame %d, the mel frames at %d", midi, start, under, b, m)
				}
			}
		}
	}
}
