package audio

import (
	"math"
	"sync"
)

// The frames the rip's models hear (BandFrames): RipBands bands three a semitone,
// band k at MIDI RipBandsLowest+k/3, from G#0 under the piano's lowest key to the
// top of the band, in two planes: fine, each band through the shortest window whose
// bins fall three a semitone at it, and sharp, every band through the RipN window
// the analysis reads. RipLayout names this layout, which weight files and corpora state.
const (
	RipBands       = 315
	RipBandsLowest = 20
	RipLayout      = 1
	// a level is RipN samples at RipRate halved level times, the window's bins
	// RipRate/RipN apart halved as often; ripLevelBand is the lowest band whose
	// semitone up, from its centre to the band three over, spans three of them at
	// level 0, and each level down serves the 36 bands under the one above, the
	// last all the rest
	ripBandLevels = 6
	ripLevelBand  = 158
	ripBandPad    = 2 // a window's transform is twice its length, zeros after it
	// RipBandsAhead is the samples past a frame's centre a BandStream hears before
	// it gives the frame: the last its longest window reads, through the halvings
	RipBandsAhead = (RipN/2+resampleTaps/2)<<(ripBandLevels-1) - resampleTaps/2
)

// BandHz is the frequency of band k, a fraction between bands
func BandHz(k float64) float64 { return 440 * math.Pow(2, (RipBandsLowest+k/3-69)/12) }

// ripBandLevel is the level the fine plane hears band k at
func ripBandLevel(k int) int { return min(max((ripLevelBand-k+35)/36, 0), ripBandLevels-1) }

// ripBand is a band's weights over its level's bins from first: a triangle from
// the band under to the band over, a bin either side at least, scaled so a unit
// cosine at the band's centre reads one
type ripBand struct {
	first int
	w     []float64
}

// ripBands is every band of each plane, fine then sharp
var ripBands = sync.OnceValue(func() (b [2][RipBands]ripBand) {
	for k := range RipBands {
		b[0][k], b[1][k] = newRipBand(k, ripBandLevel(k)), newRipBand(k, 0)
	}
	return b
})

func newRipBand(k, level int) ripBand {
	n, rate := RipN*ripBandPad, float64(RipRate)/float64(int(1)<<level)
	bin, c := rate/float64(n), BandHz(float64(k))
	lo, hi := max(c-BandHz(float64(k-1)), bin), max(BandHz(float64(k+1))-c, bin)
	b := ripBand{first: int(math.Ceil((c - lo) / bin))}
	for i := b.first; i <= min(int((c+hi)/bin), n/2); i++ {
		if f := float64(i) * bin; f <= c {
			b.w = append(b.w, (f-c+lo)/lo)
		} else {
			b.w = append(b.w, (c+hi-f)/hi)
		}
	}
	re, im := make([]float64, n), make([]float64, n)
	for i := range RipN {
		re[i] = math.Cos(2*math.Pi*c*float64(i-RipN/2)/rate) * (0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/RipN))
	}
	fft(re, im)
	var unit float64
	for i, w := range b.w {
		unit += w * (re[b.first+i]*re[b.first+i] + im[b.first+i]*im[b.first+i])
	}
	for i := range b.w {
		b.w[i] /= unit
	}
	return b
}

// BandFrames is a recording as the frames the rip's models hear, on the rip's frame
// grid: each frame the fine plane's RipBands values then the sharp plane's, each
// band's energy as log1p of a hundred times it, the recording's peak at one so a
// level change is not a different sound.
func BandFrames(x []float64, rate int) [][]float32 {
	x = resample(x, rate, RipRate)
	var peak float64
	for _, v := range x {
		peak = max(peak, math.Abs(v))
	}
	s := NewBandStream()
	e := append(s.Write(x), s.Close()...)
	p2 := peak * peak
	if p2 == 0 {
		p2 = 1
	}
	out := make([][]float32, len(e))
	for t, row := range e {
		out[t] = make([]float32, len(row))
		for b, v := range row {
			out[t][b] = float32(math.Log1p(100 * v / p2))
		}
	}
	return out
}

// BandStream is BandFrames as a recording plays, its samples as they are: Write
// takes its next samples at RipRate and returns the frames they finish, frame t
// once sample t·RipHop+RipBandsAhead is in, and Close those its end finishes, each
// band's energy before the logarithm. BandFrames' values are float32(log1p(100·
// energy/peak²)) of these, the peak the recording's loudest sample.
type BandStream struct {
	level  [ripBandLevels]bandLevel
	t      int // the next frame
	window []float64
	re, im []float64
	power  [2][]float64
}

// bandLevel is a level's samples from x0 on, n made so far, and what halves the
// samples of the level above into them, nil at level 0
type bandLevel struct {
	x     []float64
	x0, n int
	half  *resampler
}

// NewBandStream starts a stream at the recording's first sample
func NewBandStream() *BandStream {
	n := RipN * ripBandPad
	s := &BandStream{window: newSTFT(RipN).window, re: make([]float64, n), im: make([]float64, n), power: [2][]float64{make([]float64, n/2+1), make([]float64, n/2+1)}}
	for j := 1; j < ripBandLevels; j++ {
		s.level[j].half = newResampler(2, 1)
	}
	return s
}

// Write takes the next samples at RipRate and returns the frames they finish
func (s *BandStream) Write(x []float64) [][]float64 {
	s.take(x, false)
	var out [][]float64
	for ; s.t*RipHop+RipBandsAhead <= s.level[0].n; s.t++ {
		out = append(out, s.frame(s.t))
	}
	for j := range s.level { // what the next frame's windows read on
		l := &s.level[j]
		if drop := s.t*RipHop>>j - RipN/2 - l.x0; drop > 0 {
			drop = min(drop, len(l.x))
			l.x, l.x0 = l.x[:copy(l.x, l.x[drop:])], l.x0+drop
		}
	}
	return out
}

// Close returns the frames whose windows run past the recording's end, silent there
func (s *BandStream) Close() [][]float64 {
	s.take(nil, true)
	var out [][]float64
	for ; s.t*RipHop < s.level[0].n; s.t++ {
		out = append(out, s.frame(s.t))
	}
	return out
}

// take takes samples into every level, each level's halving finished when end
func (s *BandStream) take(x []float64, end bool) {
	for j := range s.level {
		l := &s.level[j]
		if l.half != nil {
			y := l.half.write(make([]float64, 0, len(x)/2+1), x)
			if end {
				y = l.half.close(y)
			}
			x = y
		}
		l.x, l.n = append(l.x, x...), l.n+len(x)
	}
}

// frame is frame t's energy under each band of both planes: two levels' windows
// through one transform, the one as its real part and the next as its imaginary
// part, their power spectra parted after
func (s *BandStream) frame(t int) []float64 {
	bands, out, n := ripBands(), make([]float64, 2*RipBands), RipN*ripBandPad
	for j := 0; j < ripBandLevels; j += 2 {
		s.windowed(s.re, j, t)
		s.windowed(s.im, j+1, t)
		fft(s.re, s.im)
		for i := range n/2 + 1 {
			zr, zi, wr, wi := s.re[i], s.im[i], s.re[(n-i)%n], s.im[(n-i)%n]
			s.power[0][i] = ((zr+wr)*(zr+wr) + (zi-wi)*(zi-wi)) / 4
			s.power[1][i] = ((zr-wr)*(zr-wr) + (zi+wi)*(zi+wi)) / 4
		}
		for k := range RipBands {
			if l := ripBandLevel(k) - j; l == 0 || l == 1 {
				out[k] = bands[0][k].energy(s.power[l])
			}
			if j == 0 {
				out[RipBands+k] = bands[1][k].energy(s.power[0])
			}
		}
	}
	return out
}

// windowed sets to level j's samples under frame t's window, zeros after them,
// and all zeros for a level past the last
func (s *BandStream) windowed(to []float64, j, t int) {
	clear(to)
	if j >= ripBandLevels {
		return
	}
	l, start := &s.level[j], t*RipHop>>j-RipN/2
	for i, w := range s.window {
		if k := start + i; k >= 0 && k < l.n {
			to[i] = l.x[k-l.x0] * w
		}
	}
}

// energy is the band's weighted sum of a power spectrum
func (b ripBand) energy(power []float64) float64 {
	var e float64
	for i, w := range b.w {
		e += w * power[b.first+i]
	}
	return e
}
