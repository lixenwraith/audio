package audio

import (
	"math"
	"math/cmplx"
	"slices"
)

// fft is an in-place iterative radix-2 transform of re and im, whose length is a
// power of two.
func fft(re, im []float64) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		ang := -2 * math.Pi / float64(size)
		wr, wi := math.Cos(ang), math.Sin(ang)
		for start := 0; start < n; start += size {
			cr, ci := 1.0, 0.0
			for k := range half {
				a, b := start+k, start+k+half
				tr := re[b]*cr - im[b]*ci
				ti := re[b]*ci + im[b]*cr
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a], im[a] = re[a]+tr, im[a]+ti
				cr, ci = cr*wr-ci*wi, cr*wi+ci*wr
			}
		}
	}
}

// spectrogram hands visit each frame's magnitude spectrum, n/2+1 bins, under a
// Hann window of n samples every hop; frames are centred, so frame t is the
// signal at sample t*hop and the first frame starts at zero.
func spectrogram(x []float64, n, hop int, visit func(frame int, mag []float64)) {
	s := newSTFT(n)
	for t := 0; t*hop < len(x); t++ {
		visit(t, s.at(x, t*hop))
	}
}

// stft is one frame's transform under a Hann window of n samples, its buffers
// kept from frame to frame
type stft struct{ window, re, im, mag []float64 }

func newSTFT(n int) *stft {
	s := &stft{window: make([]float64, n), re: make([]float64, n), im: make([]float64, n), mag: make([]float64, n/2+1)}
	for i := range s.window {
		s.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))
	}
	return s
}

// at is the magnitude spectrum of x under the window centred on sample c, the
// samples past either end silent; the slice is reused by the next call
func (s *stft) at(x []float64, c int) []float64 {
	start := c - len(s.window)/2
	for i, w := range s.window {
		if j := start + i; j >= 0 && j < len(x) {
			s.re[i] = x[j] * w
		} else {
			s.re[i] = 0
		}
		s.im[i] = 0
	}
	fft(s.re, s.im)
	for k := range s.mag {
		s.mag[k] = math.Hypot(s.re[k], s.im[k])
	}
	return s.mag
}

// slidingMedian is the median of the last size values pushed, the values kept
// sorted so a push costs a shift of size elements.
type slidingMedian struct {
	ring   []float64
	sorted []float64
	pos, n int
}

func newSlidingMedian(size int) *slidingMedian {
	return &slidingMedian{ring: make([]float64, size), sorted: make([]float64, 0, size)}
}

// push adds v, dropping the oldest value once size are held, and returns the
// median of what is held, the upper middle of an even count.
func (m *slidingMedian) push(v float64) float64 {
	if m.n == len(m.ring) {
		old := m.ring[m.pos]
		i := lowerBound(m.sorted, old)
		copy(m.sorted[i:], m.sorted[i+1:])
		m.sorted = m.sorted[:len(m.sorted)-1]
	} else {
		m.n++
	}
	m.ring[m.pos] = v
	m.pos = (m.pos + 1) % len(m.ring)
	i := lowerBound(m.sorted, v)
	m.sorted = append(m.sorted, 0)
	copy(m.sorted[i+1:], m.sorted[i:])
	m.sorted[i] = v
	return m.sorted[len(m.sorted)/2]
}

func lowerBound(s []float64, v float64) int {
	lo, hi := 0, len(s)
	for lo < hi {
		mid := (lo + hi) / 2
		if s[mid] < v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Resample is x at rate to from rate from, as the rip reads a recording
func Resample(x []float64, from, to int) []float64 { return resample(x, from, to) }

// resample converts x from one rate to another by linear interpolation, through
// a windowed-sinc low-pass first when the rate falls, so the top of the band does
// not fold into it: a resampler given the whole recording.
func resample(x []float64, from, to int) []float64 {
	if from == to {
		return x
	}
	r := newResampler(from, to)
	return r.close(r.write(make([]float64, 0, int(float64(len(x))*float64(to)/float64(from))+1), x))
}

// resampleTaps is the low-pass's length when the rate falls
const resampleTaps = 31

// resampler converts a recording from one rate to another as it arrives: each
// sample out once the samples it reads have arrived and the recording, however it
// ends, holds it, so the samples out are resample's of the whole recording.
type resampler struct {
	ratio float64   // input samples an output sample steps
	h     []float64 // the low-pass when the rate falls, nil when it rises
	sum   float64
	in    []float64 // input from sample in0 on: what the low-pass still reads
	in0   int
	y     []float64 // the low-passed input from sample y0 on: what the interpolation still reads
	y0    int
	n     int // input samples written
	out   int // samples out so far
}

func newResampler(from, to int) *resampler {
	r := &resampler{ratio: float64(from) / float64(to)}
	if to < from {
		cutoff := 0.45 * float64(to) / float64(from) // of the source rate; 0.5 is its Nyquist
		r.h = make([]float64, resampleTaps)
		for i := range r.h {
			t := float64(i - resampleTaps/2)
			w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(resampleTaps-1))
			if t == 0 {
				r.h[i] = 2 * cutoff * w
			} else {
				r.h[i] = math.Sin(2*math.Pi*cutoff*t) / (math.Pi * t) * w
			}
			r.sum += r.h[i]
		}
	}
	return r
}

// write takes the next samples in and appends the samples out they finish to out
func (r *resampler) write(out, x []float64) []float64 {
	if r.ratio == 1 { // the same rate: the samples as they are
		return append(out, x...)
	}
	r.in = append(r.in, x...)
	r.n += len(x)
	r.filter(r.n - resampleTaps/2)
	return r.interpolate(out, false)
}

// close appends the samples out the end of the recording finishes
func (r *resampler) close(out []float64) []float64 {
	if r.ratio == 1 {
		return out
	}
	r.filter(r.n)
	return r.interpolate(out, true)
}

// filter low-passes the input up to sample end, each output reading the taps
// within the recording, then drops the input no later output reads
func (r *resampler) filter(end int) {
	if r.h == nil {
		r.y = append(r.y, r.in...)
		r.in, r.in0 = r.in[:0], r.n
		return
	}
	for i := r.y0 + len(r.y); i < end; i++ {
		var acc float64
		for k, c := range r.h {
			if j := i + k - resampleTaps/2; j >= 0 && j < r.n {
				acc += c * r.in[j-r.in0]
			}
		}
		r.y = append(r.y, acc/r.sum)
	}
	if drop := r.y0 + len(r.y) - resampleTaps/2 - r.in0; drop > 0 {
		r.in, r.in0 = r.in[:copy(r.in, r.in[drop:])], r.in0+drop
	}
}

// interpolate appends each sample out whose filtered samples are in and that the
// recording, at its length so far, holds; at the end, the last reads its sample
// alone
func (r *resampler) interpolate(out []float64, end bool) []float64 {
	have := r.y0 + len(r.y)
	for n := int(float64(r.n) / r.ratio); r.out < n; r.out++ {
		pos := float64(r.out) * r.ratio
		j := int(pos)
		frac := pos - float64(j)
		var v float64
		switch {
		case j+1 < have:
			v = r.y[j-r.y0]*(1-frac) + r.y[j+1-r.y0]*frac
		case !end:
			return out
		case j < have:
			v = r.y[j-r.y0]
		}
		out = append(out, v)
	}
	if drop := int(float64(r.out)*r.ratio) - r.y0; drop > 0 {
		drop = min(drop, len(r.y))
		r.y, r.y0 = r.y[:copy(r.y, r.y[drop:])], r.y0+drop
	}
	return out
}

// aWeight is x at rate through the A-weighting curve (IEC 61672), its analog
// poles taken to the rate by the bilinear transform: three biquads, 0 dB at 1 kHz
func aWeight(x []float64, rate int) []float64 {
	y := slices.Clone(x)
	newAWeighting(rate).filter(y)
	return y
}

// aWeighting is aWeight as a recording arrives, its sections' states kept from
// block to block
type aWeighting struct {
	sec  [3][5]float64 // each biquad's b0, b1, b2, a1 and a2
	s    [3][2]float64
	gain float64 // at 1 kHz, divided out
}

func newAWeighting(rate int) *aWeighting {
	w := func(hz float64) float64 { return 2 * math.Pi * hz }
	p1, p2, p3, p4 := w(20.598997), w(107.65265), w(737.86223), w(12194.217)
	k := 2 * float64(rate)
	z := cmplx.Exp(complex(0, -w(1000)/float64(rate))) // z⁻¹ at 1 kHz
	a := &aWeighting{gain: 1}
	// each section's numerator and denominator, s² s¹ s⁰
	for i, sec := range [3][2][3]float64{
		{{1, 0, 0}, {1, 2 * p1, p1 * p1}},
		{{1, 0, 0}, {1, p2 + p3, p2 * p3}},
		{{0, 0, 1}, {1, 2 * p4, p4 * p4}},
	} {
		var c [2][3]float64
		for j, q := range sec {
			c[j] = [3]float64{q[0]*k*k + q[1]*k + q[2], 2 * (q[2] - q[0]*k*k), q[0]*k*k - q[1]*k + q[2]}
		}
		b0, b1, b2 := c[0][0]/c[1][0], c[0][1]/c[1][0], c[0][2]/c[1][0]
		a1, a2 := c[1][1]/c[1][0], c[1][2]/c[1][0]
		a.gain *= cmplx.Abs((complex(b0, 0) + complex(b1, 0)*z + complex(b2, 0)*z*z) / (1 + complex(a1, 0)*z + complex(a2, 0)*z*z))
		a.sec[i] = [5]float64{b0, b1, b2, a1, a2}
	}
	return a
}

// filter takes the next samples y through the curve, in place
func (a *aWeighting) filter(y []float64) {
	for i, c := range a.sec {
		b0, b1, b2, a1, a2 := c[0], c[1], c[2], c[3], c[4]
		s1, s2 := a.s[i][0], a.s[i][1]
		for j, v := range y {
			o := b0*v + s1
			s1, s2 = b1*v-a1*o+s2, b2*v-a2*o
			y[j] = o
		}
		a.s[i] = [2]float64{s1, s2}
	}
	for j := range y {
		y[j] /= a.gain
	}
}
