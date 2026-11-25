package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sync/atomic"
	"time"
)

// activeSound tracks a playing sound instance
type activeSound struct {
	st     SoundID // per-type polyphony accounting
	buffer floatBuffer
	pos    int
	volume float64
	placing
}

// placing is a Placement as the mixer plays it: each ear's gain, the buffer's
// samples a frame steps over and how far past pos the next read falls, and the
// low-pass's coefficient, 0 for none, with its two poles
type placing struct {
	ears       [2]float64
	step, frac float64
	coef       float64
	poles      [2]float64
}

// placing holds each field to its range, one not a number taking the zero
// value's; the square roots are equal power that leaves the middle as rendered
func (at Placement) placing() placing {
	pan := bounded(at.Pan, 1)
	p := placing{
		ears: [2]float64{math.Sqrt(1 - pan), math.Sqrt(1 + pan)},
		step: math.Pow(2, bounded(at.Pitch, MaxPlacedPitch)/12),
	}
	if at.Cutoff > 0 && at.Cutoff < AudioSampleRate/2 {
		p.coef = 1 - math.Exp(-2*math.Pi*at.Cutoff/AudioSampleRate)
	}
	return p
}

// bounded is v within ±limit, 0 when v is not a number
func bounded(v, limit float64) float64 {
	if v != v {
		return 0
	}
	return min(max(v, -limit), limit)
}

// read takes the sound's next sample, between two where its step is not whole,
// through its low-pass where it has one
func (s *activeSound) read() float64 {
	v := s.buffer[s.pos]
	if s.frac > 0 && s.pos+1 < len(s.buffer) {
		v += (s.buffer[s.pos+1] - v) * s.frac
	}
	if s.coef > 0 {
		s.poles[0] += (v - s.poles[0]) * s.coef
		s.poles[1] += (s.poles[0] - s.poles[1]) * s.coef
		v = s.poles[1]
	}
	s.frac += s.step
	whole := math.Floor(s.frac)
	s.pos += int(whole)
	s.frac -= whole
	return v
}

// Mixer is the audio thread. Fields below the confinement marker are touched
// only by the mix goroutine; all mutation arrives as audioCmd values
type Mixer struct {
	cmds     chan audioCmd
	stopChan chan struct{}
	done     chan struct{}
	started  atomic.Bool
	stopped  atomic.Bool

	paused       atomic.Bool
	musicMuted   atomic.Bool
	musicRunning atomic.Bool // query mirror for IsMusicPlaying

	played  atomic.Uint64
	dropped atomic.Uint64
	errChan chan error

	period time.Duration // one mixer pass, set at construction

	// --- mix-goroutine confined ---
	output    io.Writer
	outBroken bool
	cache     *soundCache
	kit       *kit
	sequencer *Sequencer
	active    []activeSound

	sfxVar   []uint32 // variant rotation counters, indexed by SoundID
	lastPlay []time.Time
	rapidVol []float64

	pauseGain  float64
	duckGain   float64
	limitEnv   float64      // the limiter's peak follower, over both ears
	limitGain  float64      // the gain it has ramped to
	limitBuf   [][2]float64 // the look-ahead delay, a frame's two ears
	limitPos   int
	musicBuf   []float64
	sfxBuf     [2][]float64 // the sfx bus's two ears over one period
	bed        bed
	bedBuf     [2][]float64 // the bed's two ears over one period
	mixBuf     [2][]float64 // the limited mix's two ears; the tap's while it is written
	outBytes   []byte
	musicTap   io.Writer
	musicBytes []byte
}

// bed is the binaural bed: a sine an ear, the left at the carrier less half the
// beat and the right at it plus half, each phase carried on through every change
type bed struct {
	want                 Binaural // what SetBinaural asked
	level, carrier, beat float64  // where each has come to on its way there
	fade                 float64  // the level's move a sample
	phase                [2]float64
}

// set takes a new target. The level crosses to it over the music's own fade-in;
// a bed set off fades on the tones it plays, and one coming from silence starts
// on its carrier and beat rather than gliding to them from the last it played.
func (b *bed) set(want Binaural) {
	if want.Level == 0 {
		want.Carrier, want.Beat = b.want.Carrier, b.want.Beat
	}
	if b.level == 0 {
		b.carrier, b.beat = want.Carrier, want.Beat
	}
	if want.Level != b.want.Level {
		b.fade = math.Abs(want.Level-b.level) / (MusicStartFadeIn.Seconds() * AudioSampleRate)
	}
	b.want = want
}

// restart brings the bed in from silence, as the music it starts with fades in
func (b *bed) restart() {
	b.level = 0
	b.fade = b.want.Level / (MusicStartFadeIn.Seconds() * AudioSampleRate)
}

// render writes one period of the two ears and reports whether the bed sounds;
// off, it writes nothing and moves no state
func (b *bed) render(left, right []float64) bool {
	if b.level == 0 && b.want.Level == 0 {
		return false
	}
	for i := range left {
		if b.level < b.want.Level {
			b.level = min(b.want.Level, b.level+b.fade)
		} else {
			b.level = max(b.want.Level, b.level-b.fade)
		}
		b.carrier += (b.want.Carrier - b.carrier) * bedGlide
		b.beat += (b.want.Beat - b.beat) * bedGlide
		l := b.phase[0] + (b.carrier-b.beat/2)/AudioSampleRate
		r := b.phase[1] + (b.carrier+b.beat/2)/AudioSampleRate
		b.phase = [2]float64{l - math.Floor(l), r - math.Floor(r)}
		left[i] = b.level * math.Sin(2*math.Pi*b.phase[0])
		right[i] = b.level * math.Sin(2*math.Pi*b.phase[1])
	}
	return true
}

// NewMixer creates a mixer writing to out
func NewMixer(out io.Writer, cache *soundCache, k *kit, period time.Duration) *Mixer {
	m := &Mixer{
		period:    period,
		output:    out,
		cache:     cache,
		kit:       k,
		cmds:      make(chan audioCmd, 256),
		stopChan:  make(chan struct{}),
		done:      make(chan struct{}),
		errChan:   make(chan error, 1),
		sequencer: NewSequencer(DefaultBPM, k),
		active:    make([]activeSound, 0, 8),
		pauseGain: 1.0,
		duckGain:  1.0,
	}
	n := cache.count()
	m.sfxVar = make([]uint32, n)
	m.lastPlay = make([]time.Time, n)
	m.rapidVol = make([]float64, n)
	for i := range m.rapidVol {
		m.rapidVol[i] = 1.0
	}
	frames := bufferFrames(period)
	m.musicBuf = make([]float64, frames)
	for ear := range m.mixBuf {
		m.sfxBuf[ear] = make([]float64, frames)
		m.bedBuf[ear] = make([]float64, frames)
		m.mixBuf[ear] = make([]float64, frames)
	}
	m.musicBytes = make([]byte, frames*AudioBytesPerFrame)
	m.outBytes = make([]byte, frames*AudioBytesPerFrame)
	m.limitBuf = make([][2]float64, LimiterLookahead)
	m.limitGain = 1
	return m
}

// Send enqueues a command from any goroutine. Reports false when the queue is
// full and the command was dropped; play commands also bump the drop counter.
func (m *Mixer) Send(c audioCmd) bool {
	if m.stopped.Load() {
		return false
	}
	select {
	case m.cmds <- c:
		return true
	default:
		if c.op == cmdPlay {
			m.dropped.Add(1)
		}
		return false
	}
}

// SwapOutput redirects backend output; applied at the next tick
func (m *Mixer) SwapOutput(w io.Writer) { m.Send(audioCmd{op: cmdSwapOutput, w: w}) }

func (m *Mixer) SetPaused(p bool)        { m.paused.Store(p) }
func (m *Mixer) SetMusicMuted(mute bool) { m.musicMuted.Store(mute) }
func (m *Mixer) IsMusicMuted() bool      { return m.musicMuted.Load() }
func (m *Mixer) Errors() <-chan error    { return m.errChan }

func (m *Mixer) Start() {
	if m.started.CompareAndSwap(false, true) {
		go m.loop()
	}
}

func (m *Mixer) Stop() {
	if m.stopped.CompareAndSwap(false, true) {
		close(m.stopChan)
	}
}

// Wait blocks until the mix goroutine has returned, reporting whether it did so
// within timeout (<= 0 polls); true at once if Start was never called. A true
// return is a happens-before edge against every mixer-confined write. A false one
// means the goroutine is still inside output.Write on a backend that stopped
// draining: the caller must not touch mixer-confined state or reset the registries.
func (m *Mixer) Wait(timeout time.Duration) bool {
	if !m.started.Load() {
		return true
	}
	if timeout <= 0 {
		select {
		case <-m.done:
			return true
		default:
			return false
		}
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-m.done:
		return true
	case <-t.C:
		return false
	}
}

func (m *Mixer) GetStats() (played, dropped uint64) {
	return m.played.Load(), m.dropped.Load()
}

// onePoleCoef converts a smoothing time constant to a per-sample coefficient
func onePoleCoef(tau time.Duration) float64 {
	return 1.0 - math.Exp(-1.0/(tau.Seconds()*float64(AudioSampleRate)))
}

// The mix's ramps a sample: the pause fade's step, the duck's and the bed's
// glide's one-pole coefficients and the limiter's release
var (
	pauseStep = 1.0 / (AudioPauseFade.Seconds() * float64(AudioSampleRate))
	duckAtk   = onePoleCoef(MusicDuckAttack)
	duckRel   = onePoleCoef(MusicDuckRelease)
	limRel    = math.Exp(-1.0 / (LimiterRelease.Seconds() * float64(AudioSampleRate)))
	bedGlide  = onePoleCoef(BinauralGlide)
)

// loop: drain commands, render, write — one pass per buffer tick
func (m *Mixer) loop() {
	defer close(m.done)
	ticker := time.NewTicker(m.period)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopChan:
			return
		case <-ticker.C:
			// select picks at random once a slow tick leaves both cases ready
			if m.stopped.Load() {
				return
			}
			m.drainCmds()
			m.renderTick()
		}
	}
}

func (m *Mixer) drainCmds() {
	for {
		select {
		case c := <-m.cmds:
			m.apply(c)
		default:
			return
		}
	}
}

func (m *Mixer) apply(c audioCmd) {
	switch c.op {
	case cmdPlay:
		m.startSound(c.sound, c.f1, c.place)
	case cmdBPM:
		m.sequencer.SetBPM(c.i1, c.b)
	case cmdMeter:
		m.sequencer.SetMeter(Meter{c.i1, c.i2}, c.b)
	case cmdSwing:
		m.sequencer.SetSwing(c.f1)
	case cmdTuning:
		m.kit.tuning = c.f1
	case cmdMusicVol:
		m.sequencer.SetVolume(c.f1)
	case cmdPattern:
		m.sequencer.SetPattern(int(c.slot), c.pattern, c.i1, c.b)
	case cmdMask:
		m.sequencer.SetMask(int(c.slot), c.pattern, uint32(c.i2))
	case cmdSlotMute:
		m.sequencer.SetSlotMuted(int(c.slot), c.b)
	case cmdHarmony:
		m.sequencer.SetHarmonyCfg(c.i1, ScaleID(c.i2), c.ints)
	case cmdNote:
		m.sequencer.TriggerNote(c.i1, c.f1, c.i2, c.instr)
	case cmdMusicStart:
		if !m.sequencer.IsRunning() {
			m.bed.restart()
		}
		m.sequencer.Start()
		m.musicRunning.Store(true)
	case cmdMusicStop:
		m.sequencer.Stop()
		m.musicRunning.Store(false)
	case cmdMusicReset:
		m.sequencer.Reset()
		m.musicRunning.Store(false)
	case cmdSwapOutput:
		m.output = c.w
		m.outBroken = false
	case cmdSeed:
		m.sequencer.Reseed(c.seed)
	case cmdIntensity:
		m.sequencer.SetIntensity(c.tier, c.i1, c.b, c.reveal)
	case cmdReloadSound:
		m.growTables(c.sound)
		m.cache.replace(c.sound, c.bufs)
		m.kit.rebind(c.sound, c.takes)
	case cmdReloadPattern:
		m.sequencer.ReloadPattern(c.pattern)
	case cmdReloadVoice:
		m.kit.setVoice(c.instr, c.voice)
	case cmdPlayBuffer:
		m.startBuffer(c.buf, c.f1)
	case cmdAutoFill:
		m.sequencer.autoFill = c.b
	case cmdMusicTap:
		m.musicTap = c.w
	case cmdKit:
		m.kit.bind(c.drums)
	case cmdBinaural:
		m.bed.set(c.bed)
	}
}

// startSound applies rapid-fire dampening and polyphony caps, then activates a voice on a rotated variant
func (m *Mixer) startSound(st SoundID, vol float64, at placing) {
	if m.paused.Load() {
		m.dropped.Add(1)
		return
	}
	if st <= 0 || int(st) >= len(m.sfxVar) {
		m.dropped.Add(1)
		return
	}
	vars := m.cache.variants(st)
	if len(vars) == 0 {
		m.dropped.Add(1)
		return
	}
	buf := vars[m.sfxVar[st]%uint32(len(vars))]
	m.sfxVar[st]++

	now := time.Now()
	elapsed := now.Sub(m.lastPlay[st])
	if elapsed < RapidFireCooldown {
		m.rapidVol[st] *= RapidFireDecay
		if m.rapidVol[st] < RapidFireMinVolume {
			m.rapidVol[st] = RapidFireMinVolume
		}
	} else {
		// recovery is in proportion to the gap, so sustained fire at
		// real cadence does not return to full volume every shot
		rec := elapsed.Seconds() / RapidFireRecovery.Seconds()
		if rec > 1 {
			rec = 1
		}
		m.rapidVol[st] += (1.0 - m.rapidVol[st]) * rec
	}
	m.lastPlay[st] = now

	m.admit(activeSound{st: st, buffer: buf, volume: vol * m.rapidVol[st], placing: at})
}

// renderTick renders one buffer: music bus and bed, sfx bus, duck, pause, limit, write
func (m *Mixer) renderTick() {
	left, right := m.mixBuf[0], m.mixBuf[1]
	isPaused := m.paused.Load()

	clear(m.musicBuf)
	clear(m.sfxBuf[0])
	clear(m.sfxBuf[1])

	// Music plays out the pause fade, then freezes: position holds for an aligned
	// resume. The bed sounds while the music does, and is in what the tap records.
	bedOn := false
	if (!isPaused || m.pauseGain > 0) && !m.musicMuted.Load() && m.sequencer.IsRunning() {
		m.sequencer.Generate(m.musicBuf)
		bedOn = m.bed.render(m.bedBuf[0], m.bedBuf[1])
		if m.musicTap != nil {
			tapL, tapR := m.musicBuf, m.musicBuf
			if bedOn {
				tapL, tapR = left, right
				for i, v := range m.musicBuf {
					tapL[i], tapR[i] = v+m.bedBuf[0][i], v+m.bedBuf[1][i]
				}
			}
			floatToBytes(tapL, tapR, m.musicBytes)
			_, _ = m.musicTap.Write(m.musicBytes) // a failed tap costs the recording, not playback
		}
	}

	// Sfx tails always render; pause gain fades them out
	sfxLive := len(m.active) > 0
	if sfxLive {
		m.active = m.mixActive(m.sfxBuf)
	}

	// sidechain duck — music dips under sounding sfx
	duckTarget := 1.0
	if sfxLive {
		duckTarget = MusicDuckAmount
	}

	for i := range left {
		coef := duckRel
		if duckTarget < m.duckGain {
			coef = duckAtk
		}
		m.duckGain += (duckTarget - m.duckGain) * coef

		if isPaused {
			if m.pauseGain > 0 {
				m.pauseGain -= pauseStep
				if m.pauseGain < 0 {
					m.pauseGain = 0
				}
			}
		} else if m.pauseGain < 1 {
			m.pauseGain += pauseStep
			if m.pauseGain > 1 {
				m.pauseGain = 1
			}
		}

		// The bed joins after the duck, so sfx never dip it; with no bed and no
		// sound placed off the middle both ears are the one mix, to the bit
		l := (m.musicBuf[i]*m.duckGain + m.sfxBuf[0][i]) * m.pauseGain
		r := (m.musicBuf[i]*m.duckGain + m.sfxBuf[1][i]) * m.pauseGain
		if bedOn {
			l += m.bedBuf[0][i] * m.pauseGain
			r += m.bedBuf[1][i] * m.pauseGain
		}
		// The limiter follows the louder ear's peak now and ramps toward its gain
		// by at most a look-ahead's worth a sample, applied to both ears of the
		// frame a look-ahead ago
		if a := max(math.Abs(l), math.Abs(r)); a > m.limitEnv {
			m.limitEnv = a
		} else {
			m.limitEnv *= limRel
		}
		target := 1.0
		if m.limitEnv > LimiterCeiling {
			target = LimiterCeiling / m.limitEnv
		}
		const step = 1.0 / LimiterLookahead
		if target < m.limitGain {
			m.limitGain = max(target, m.limitGain-step)
		} else {
			m.limitGain = min(target, m.limitGain+step)
		}
		was := m.limitBuf[m.limitPos]
		left[i], right[i] = was[0]*m.limitGain, was[1]*m.limitGain
		m.limitBuf[m.limitPos] = [2]float64{l, r}
		m.limitPos = (m.limitPos + 1) % LimiterLookahead
	}

	floatToBytes(left, right, m.outBytes)

	if m.outBroken {
		return // keep rendering state; supervisor swaps output or latches silent
	}
	if _, err := m.output.Write(m.outBytes); err != nil {
		// Mixer doesn't exit on write error — flags broken output,
		// continues; failover restores audio via cmdSwapOutput
		m.outBroken = true
		select {
		case m.errChan <- fmt.Errorf("%w: %v", ErrPipeClosed, err):
		default:
		}
	}
}

// mixActive mixes all active sounds into the two ears, returns remaining sounds
func (m *Mixer) mixActive(ears [2][]float64) []activeSound {
	remaining := m.active[:0]

	for i := range m.active {
		s := &m.active[i]
		for j := 0; j < len(ears[0]) && s.pos < len(s.buffer); j++ {
			v := s.read() * s.volume
			ears[0][j] += v * s.ears[0]
			ears[1][j] += v * s.ears[1]
		}
		if s.pos < len(s.buffer) {
			remaining = append(remaining, *s)
		}
	}

	return remaining
}

// floatToBytes converts the two ears' float64 samples to interleaved stereo
// int16 LE bytes, hard clipping what the limiter let through; a mono mix is
// its own second ear
func floatToBytes(left, right []float64, out []byte) {
	for i := range left {
		for ear, v := range [2]float64{left[i], right[i]} {
			if v > 1.0 {
				v = 1.0
			} else if v < -1.0 {
				v = -1.0
			}
			binary.LittleEndian.PutUint16(out[i*4+ear*2:], uint16(int16(v*32767)))
		}
	}
}

// growTables extends the per-SoundID tables to cover id. Reachable only via
// cmdReloadSound for a sound defined after Start.
func (m *Mixer) growTables(id SoundID) {
	n := int(id) + 1
	if n <= len(m.sfxVar) {
		return
	}
	m.sfxVar = append(m.sfxVar, make([]uint32, n-len(m.sfxVar))...)
	m.lastPlay = append(m.lastPlay, make([]time.Time, n-len(m.lastPlay))...)
	for len(m.rapidVol) < n {
		m.rapidVol = append(m.rapidVol, 1.0)
	}
}

// admit inserts a voice under the per-type and global caps, stealing the
// most-progressed sibling (deepest into its decay tail). Shared by the registry
// and preview paths.
func (m *Mixer) admit(ns activeSound) {
	cnt, victim := 0, -1
	for i := range m.active {
		if m.active[i].st != ns.st {
			continue
		}
		cnt++
		if victim < 0 || m.active[i].pos > m.active[victim].pos {
			victim = i
		}
	}
	switch {
	case cnt >= MaxSfxPerSound:
		m.active[victim] = ns
	case len(m.active) >= MaxActiveSfx:
		g := 0
		for i := range m.active {
			if m.active[i].pos > m.active[g].pos {
				g = i
			}
		}
		m.active[g] = ns
	default:
		m.active = append(m.active, ns)
	}
	m.played.Add(1)
}

// startBuffer plays an unregistered buffer. st is SoundNone, so previews
// contend only with each other for the per-type cap; rapid-fire dampening and
// variant rotation do not apply.
func (m *Mixer) startBuffer(buf floatBuffer, vol float64) {
	if m.paused.Load() || len(buf) == 0 {
		m.dropped.Add(1)
		return
	}
	m.admit(activeSound{st: SoundNone, buffer: buf, volume: vol, placing: Placement{}.placing()})
}
