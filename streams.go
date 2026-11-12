package audio

import (
	"math"
	"slices"
)

// LeadRule names which of the melody register's two lines a rip calls the
// melody: the busier by default, the most notes for its sounding time, since the
// ear follows the line that moves over held notes; or the higher, lower, louder,
// softer, longer or shorter in its notes, when the listener knows the piece.
type LeadRule int

const (
	LeadBusy LeadRule = iota // the line with more notes a second of its sounding, the one that moves
	LeadHigh                 // the line whose notes sit higher on average
	LeadLow
	LeadLoud // the line whose notes are stronger on average
	LeadSoft
	LeadLong // the line whose notes last longer on average
	LeadShort
)

var leadWords = [...]string{"busy", "high", "low", "loud", "soft", "long", "short"}

func (l LeadRule) String() string {
	if l < 0 || int(l) >= len(leadWords) {
		return "busy"
	}
	return leadWords[l]
}

// ParseLeadRule reads a rule's word
func ParseLeadRule(s string) (LeadRule, bool) {
	i := slices.Index(leadWords[:], s)
	return LeadRule(max(i, 0)), i >= 0
}

// The costs a stream pays, each about one for a clear difference: an octave
// from its last note, a note four times as long or as short, a change of
// strength of two thirds, a note begun while its last sounds for over half the
// shorter, a change of stream between two notes in a row that do not sound
// together, and a stream's first note.
const (
	streamJump    = 1.0
	streamLength  = 1.0
	streamLevel   = 1.5
	streamOverlap = 10.0
	streamSwitch  = 0.25
	streamStart   = 0.5
	streamMemory  = 48 // how many notes back the other stream's last note is kept
)

// streams parts a register's notes, both decoded lines' together, into two lines
// by what a line keeps, its pitch from note to note, its notes' length and
// strength and no two at once, where the decoder follows the louder: a path over
// the notes in time order, each joining the stream of the note before or the
// other. The lead is the stream the rule names, the rest the second line.
func streams(notes []ripNote, hop float64, lead LeadRule) []ripNote {
	slices.SortFunc(notes, func(a, b ripNote) int { return a.start - b.start })
	n := len(notes)
	if n == 0 {
		return notes
	}
	sec := func(frames int) float64 { return float64(frames) * hop }
	together := func(i, j int) bool {
		a, b := notes[i], notes[j]
		over := min(a.end, b.end) - max(a.start, b.start)
		return 2*over > min(a.end-a.start, b.end-b.start)
	}
	one := true
	for k := 1; k < n && one; k++ {
		for i := k - 1; i >= 0 && notes[i].end > notes[k].start; i-- {
			if together(i, k) {
				one = false
				break
			}
		}
	}
	if one {
		for k := range notes {
			notes[k].second = false
		}
		return notes
	}
	cost := func(i, j int) float64 { // note j joining the stream whose last note is i
		a, b := notes[i], notes[j]
		gap := max(sec(b.start-a.end), 0)
		c := streamJump * math.Min(math.Abs(float64(b.midi-a.midi)), 12) / 12 * math.Exp(-gap)
		c += streamLength * math.Min(math.Abs(math.Log(float64(max(b.end-b.start, 1))/float64(max(a.end-a.start, 1))))/math.Log(4), 1)
		c += streamLevel * math.Abs(b.strength-a.strength)
		if together(i, j) {
			c += streamOverlap
		}
		return c
	}
	// state after note k: the index of the other stream's last note, -1 for
	// none, at states[k][other+1]; note k is the last of its own. Each holds its
	// cheapest cost and from where it came, as the other stream's last note before
	// k and whether k joined the stream of k-1. A tie goes to the earliest other,
	// met first in the walk, so the parting is the notes' whatever the run.
	type arrival struct {
		cost float64
		from int
		same bool
	}
	states := make([][]arrival, n)
	states[0] = []arrival{{cost: streamStart, from: -1, same: true}}
	for k := 1; k < n; k++ {
		states[k] = make([]arrival, k+1)
		for i, prev := range states[k-1] {
			other := i - 1
			// k joins the stream of k-1
			states[k][i] = arrival{cost: prev.cost + cost(k-1, k), from: other, same: true}
			// k joins the other stream, started here or continued from other;
			// the change costs nothing where k sounds together with k-1
			c := prev.cost
			if !together(k-1, k) {
				c += streamSwitch
			}
			if other < 0 || k-1-other > streamMemory { // no note, or one long silent and forgotten
				c += streamStart
			} else {
				c += cost(other, k)
			}
			if i == 0 || c < states[k][k].cost {
				states[k][k] = arrival{cost: c, from: other, same: false}
			}
		}
	}
	// the cheapest end, walked back into stream numbers
	best, at := math.Inf(1), -1
	for i, a := range states[n-1] {
		if a.cost < best {
			best, at = a.cost, i-1
		}
	}
	same := make([]bool, n)
	for k := n - 1; k >= 1; k-- {
		a := states[k][at+1]
		same[k] = a.same
		at = a.from
	}
	stream := make([]int, n)
	for k := 1; k < n; k++ {
		stream[k] = stream[k-1]
		if !same[k] {
			stream[k] = 1 - stream[k-1]
		}
	}
	// which stream is the lead
	var count, strength, pitch, sounding [2]float64
	for k, s := range stream {
		count[s]++
		strength[s] += notes[k].strength
		pitch[s] += float64(notes[k].midi)
		sounding[s] += sec(notes[k].end - notes[k].start)
	}
	if count[1] == 0 {
		return notes
	}
	score := func(s int) float64 {
		switch lead {
		case LeadHigh:
			return pitch[s] / count[s]
		case LeadLow:
			return -pitch[s] / count[s]
		case LeadLoud:
			return strength[s] / count[s]
		case LeadSoft:
			return -strength[s] / count[s]
		case LeadLong:
			return sounding[s] / count[s]
		case LeadShort:
			return -sounding[s] / count[s]
		}
		return count[s] / math.Max(sounding[s], hop)
	}
	leadStream := 0
	if score(1) > score(0) {
		leadStream = 1
	}
	for k := range notes {
		notes[k].second = stream[k] != leadStream
	}
	return notes
}
