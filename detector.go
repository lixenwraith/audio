package audio

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Synthetic sink backends. Neither is auto-detected — they have no executable
// and no probe — so they are reachable only by explicit force. They exist for
// headless runs: tests, CI, and offline capture.
const (
	BackendNameNull = "null"
	BackendNameHost = "host" // AudioConfig.Sink, which replaces detection
	wavForcePrefix  = "wav:"
)

// backendSpecs returns the priority-ordered candidate table: pacat first, the
// route desktop PipeWire answers through its pulse server. pw-cat 1.4 on reads
// "-" through libsndfile unless --raw, which 1.2 and older reject, so it has a
// row for each and the probe keeps the one that starts. play takes no -d: there
// it names a second input, the capture device.
func backendSpecs() []*BackendConfig {
	return []*BackendConfig{
		{Type: BackendPulse, Name: "pacat", Args: []string{"--raw", "--format=s16le", "--rate=44100", "--channels=2", "--latency-msec=50", "--playback"}},
		{Type: BackendPipeWire, Name: "pw-cat", Args: []string{"--playback", "--raw", "--format=s16", "--rate=44100", "--channels=2", "--latency=50ms", "-"}},
		{Type: BackendPipeWire, Name: "pw-cat", Args: []string{"--playback", "--format=s16", "--rate=44100", "--channels=2", "--latency=50ms", "-"}},
		{Type: BackendALSA, Name: "aplay", Args: []string{"-t", "raw", "-f", "S16_LE", "-r", "44100", "-c", "2", "-q"}},
		{Type: BackendSoX, Name: "sox", Args: []string{"-t", "raw", "-e", "signed", "-b", "16", "-c", "2", "-r", "44100", "-", "-q"}},
		{Type: BackendFFplay, Name: "ffplay", Args: []string{"-nodisp", "-autoexit", "-f", "s16le", "-ch_layout", "stereo", "-sample_rate", "44100", "-probesize", "32", "-analyzeduration", "0", "-i", "pipe:0", "-loglevel", "quiet"}},
		{Type: BackendOSS, Name: "oss", Path: "/dev/dsp"},
	}
}

// lookupBin maps backend name to executable name
func lookupBin(name string) string {
	if name == "sox" {
		return "play"
	}
	return name
}

// DetectBackends returns installed candidates in priority order
// force restricts selection to a single named backend ("" = all)
// Returns ErrNoAudioBackend when nothing is installed; caller drops or logs
// it — silent mode remains the degradation path
func DetectBackends(force string) ([]*BackendConfig, error) {
	if force == BackendNameNull {
		return []*BackendConfig{{Type: BackendNull, Name: force}}, nil
	}
	if p, ok := strings.CutPrefix(force, wavForcePrefix); ok {
		if p == "" {
			return nil, fmt.Errorf("%w: %q needs a path, e.g. wav:out.wav", ErrNoAudioBackend, force)
		}
		return []*BackendConfig{{Type: BackendWAV, Name: force, Path: p}}, nil
	}

	var out []*BackendConfig
	for _, spec := range backendSpecs() {
		if force != "" && spec.Name != force {
			continue
		}
		if spec.Type == BackendOSS {
			if runtime.GOOS == "freebsd" {
				if _, err := os.Stat(spec.Path); err == nil {
					out = append(out, spec)
				}
			}
			continue
		}
		if path, err := exec.LookPath(lookupBin(spec.Name)); err == nil {
			spec.Path = path
			out = append(out, spec)
		}
	}
	if len(out) == 0 {
		if force != "" {
			return nil, fmt.Errorf("%w: forced backend %q not installed", ErrNoAudioBackend, force)
		}
		return nil, ErrNoAudioBackend
	}
	return out, nil
}
