package audio

import (
	"cmp"
	"math"
	"slices"
)

// RipStage is a stage of the decoders, in the order a rip runs them
// (Rip.Stages): the notes' then the hits'
type RipStage int

const (
	StageStrikes    RipStage = iota // a note a strike starts, to its end
	StageGlides                     // a sound followed across its cells and parted into the notes it rests on
	StagePath                       // a pitch track's note: the analysis's, or a bass note no strike answers
	StageOwnPitch                   // a low note whose own pitch stands under its next partials
	StageSteadyRise                 // a low note that rises not over its pitch's steady level
	StageSettle                     // a note both registers heard, given to one line
	StageAccounting                 // the notes held to the recording, and the strikes it brings back
	StageMusicStart                 // what starts before the music does
	StagePicks                      // a drum's hits picked off its strength
	StageBars                       // a hit laid on a step of the bars
	ripStages
)

var ripStageNames = [ripStages]string{"strikes", "glides", "path", "own pitch", "steady rise", "settle", "accounting", "music start", "picks", "bars"}

func (s RipStage) String() string {
	if s < 0 || s >= ripStages {
		return "?"
	}
	return ripStageNames[s]
}

// RipChangeKind is what a stage did to a note or a hit
type RipChangeKind int

const (
	RipMade    RipChangeKind = iota // made it
	RipDropped                      // dropped it
	RipMoved                        // moved a note's end, or laid a hit on a step
	RipRefused                      // started no note at a strike surer than a half, the strikes' alone
)

// RipWhy is why the strikes started no note at a strike, or the bars dropped a hit
type RipWhy int

const (
	WhyNone     RipWhy = iota
	WhyUnderBar        // under Strike, StrikeLow under the melody's register
	WhyAgain           // within StrikeGap of its pitch's last strike
	WhyPartial         // a stronger strike's partial, an octave, a twelfth or two octaves down
	WhySilent          // its pitch sounds not within MinNote of it
	WhyShort           // its note ends within MinNote
	WhyInside          // inside its pitch's last note, which it does not end
	WhyRung            // while its pitch rings on after its last note, not sure enough to start it again
	WhyUnheard         // its register sounds under a tenth of the frames, and has no reference
	WhyOutside         // a hit outside the bars
	WhyHeld            // a hit on a step another of its drum holds
	ripWhys
)

var ripWhyNames = [ripWhys]string{"", "under its bar", "within strike_gap", "a stronger strike's partial",
	"its pitch silent within min_note", "its note under min_note", "inside its pitch's note", "while its pitch rings",
	"its register unheard", "outside the bars", "on a step another holds"}

func (w RipWhy) String() string {
	if w < 0 || w >= ripWhys {
		return "?"
	}
	return ripWhyNames[w]
}

// RipChange is what a stage of the decoders did to a note or a hit of a rip
// whose Ripper asked (Stages), in seconds of the recording: a note's End is the
// end the stage left it, a hit's the place the bars laid it at
type RipChange struct {
	Stage      RipStage
	Kind       RipChangeKind
	Why        RipWhy
	Start, End float64
	MIDI       int            // a note's, -1 for a hit
	Drum       InstrumentType // a hit's
}

// ripTrace keeps what each stage does to a rip's notes and hits, where the
// Ripper asks (Stages); a nil trace keeps nothing
type ripTrace struct {
	hop     float64
	changes []RipChange
}

// note keeps what a stage did to a note
func (tr *ripTrace) note(stage RipStage, kind RipChangeKind, why RipWhy, n ripNote) {
	if tr != nil {
		tr.changes = append(tr.changes, RipChange{Stage: stage, Kind: kind, Why: why, Start: float64(n.start) * tr.hop, End: float64(n.end) * tr.hop, MIDI: n.midi})
	}
}

// made keeps the notes a stage made
func (tr *ripTrace) made(stage RipStage, notes ...ripNote) {
	if tr == nil {
		return
	}
	for _, n := range notes {
		tr.note(stage, RipMade, WhyNone, n)
	}
}

// between keeps what a stage did to the notes it was given, before, as it gave
// them, after: a note is its start and pitch, made where after holds one before
// lacks, dropped where before holds one after lacks, moved where its end did
func (tr *ripTrace) between(stage RipStage, before, after []ripNote) {
	if tr == nil {
		return
	}
	type key struct{ start, midi int }
	at, used := map[key][]int{}, make([]bool, len(before))
	for i, n := range before {
		k := key{n.start, n.midi}
		at[k] = append(at[k], i)
	}
	for _, n := range after {
		i := -1 // the same note: one of before's at its start and pitch, its end first
		for _, j := range at[key{n.start, n.midi}] {
			if !used[j] && (i < 0 || before[j].end == n.end && before[i].end != n.end) {
				i = j
			}
		}
		switch {
		case i < 0:
			tr.note(stage, RipMade, WhyNone, n)
		case before[i].end != n.end:
			used[i] = true
			tr.note(stage, RipMoved, WhyNone, n)
		default:
			used[i] = true
		}
	}
	for i, n := range before {
		if !used[i] {
			tr.note(stage, RipDropped, WhyNone, n)
		}
	}
}

// refuse keeps a strike of state s at frame t the strikes started no note at,
// why, where it peaks surer than a half
func (tr *ripTrace) refuse(r register, t, s int, why RipWhy, p RipParams) {
	f, midi := r.f, r.lo+s
	if tr == nil || !f.peaks(f.Strike, t, midi, accStrike) {
		return
	}
	tr.note(StageStrikes, RipRefused, why, ripNote{start: t, end: t, midi: midi})
}

// passed keeps the strikes surer than a half of pitch midi the strikes passed
// over in frames a to b, why
func (tr *ripTrace) passed(f *RipFrames, a, b, midi int, why RipWhy) {
	for t := a; tr != nil && t < b; t++ {
		if f.peaks(f.Strike, t, midi, accStrike) {
			tr.note(StageStrikes, RipRefused, why, ripNote{start: t, end: t, midi: midi})
		}
	}
}

// hit keeps what a stage did to drum d's hit at frame, at the place where the
// bars laid it
func (tr *ripTrace) hit(stage RipStage, kind RipChangeKind, why RipWhy, d InstrumentType, frame int, at float64) {
	if tr != nil {
		tr.changes = append(tr.changes, RipChange{Stage: stage, Kind: kind, Why: why, Start: float64(frame) * tr.hop, End: at, MIDI: -1, Drum: d})
	}
}

// list is what the trace kept, empty where its stages did nothing and nil
// where there is no trace
func (tr *ripTrace) list() []RipChange {
	if tr == nil {
		return nil
	}
	return append(make([]RipChange, 0, len(tr.changes)), tr.changes...)
}

// StageCount counts by the stage that owns each, by name: a stage's
// (RipStage.String), a refused strike's reason (RipWhy.String), or with a
// word after it
type StageCount map[string]int

// StageReading is a rip against a truth by the stage of the decoders that owns
// each loss and addition (Rip.Stages): the notes given it lacks, why the
// strikes started none near those lost there, the notes it adds, the ends of
// those matched it puts wrong, and the hits given it lacks and those it adds;
// a twin is a loss to another given note or hit the rip's answered
type StageReading struct {
	Lost, Refused, Added, Ends, HitsLost, HitsAdded StageCount
}

// stageTwin names a loss no stage owns: a note or a hit of the rip's stood at
// its place to the end and was matched to another given
const stageTwin = "a twin"

// ReadStages reads a rip's stages (Rip.Stages) against its agreement with a
// truth's notes, by pitch and onset, and with its hits matched within tol, nil
// for a truth with none: a lost note's stage is the first after which no note
// of its pitch starts within the matcher's window of it, among those from the
// first that made a note; an added note's the last that made it; a wrong end's
// the first after which it was wrong; a lost hit's what became of the picked
// hit nearest it within tol. A rip whose Ripper asked for none reads none.
func ReadStages(r *Rip, ag Agreement, hg *HitsAgreement, tol float64) StageReading {
	var out StageReading
	if r.Stages == nil {
		return out
	}
	out = StageReading{StageCount{}, StageCount{}, StageCount{}, StageCount{}, StageCount{}, StageCount{}}
	type key struct{ frame, midi int }
	frame := func(sec float64) int { return int(math.Round(sec / RipFrameSec)) }
	pitch, ident := map[int][]RipChange{}, map[key][]RipChange{}
	first, struck := ripStages, false
	for _, c := range r.Stages {
		if c.MIDI < 0 {
			continue
		}
		pitch[c.MIDI], ident[key{frame(c.Start), c.MIDI}] = append(pitch[c.MIDI], c), append(ident[key{frame(c.Start), c.MIDI}], c)
		if c.Kind == RipMade {
			first = min(first, c.Stage)
		}
		struck = struck || c.Stage == StageStrikes
	}
	if first == ripStages { // nothing made: lost where the front end's notes start
		first = StagePath
		if struck {
			first = StageStrikes
		}
	}
	for _, cs := range pitch {
		slices.SortStableFunc(cs, func(a, b RipChange) int { return cmp.Compare(a.Stage, b.Stage) })
	}
	for _, n := range ag.All.OnlyB {
		if n.MIDI < 0 {
			continue
		}
		midi, at := n.MIDI+ag.Shift, ag.At(n.Start)
		cs, stage := pitch[midi], stageTwin
		var starts []float64
		for s, k := first, 0; s < StagePicks && stage == stageTwin; s++ {
			for ; k < len(cs) && cs[k].Stage <= s; k++ {
				switch cs[k].Kind {
				case RipMade:
					starts = append(starts, cs[k].Start)
				case RipDropped:
					if i := slices.Index(starts, cs[k].Start); i >= 0 {
						starts = slices.Delete(starts, i, i+1)
					}
				}
			}
			if !slices.ContainsFunc(starts, func(t float64) bool { return math.Abs(t-at) <= matchWindow+1e-9 }) {
				stage = s.String()
			}
		}
		out.Lost[stage]++
		if stage == StageStrikes.String() {
			why, near := "no strike near", math.Inf(1)
			for _, c := range cs {
				if d := math.Abs(c.Start - at); c.Kind == RipRefused && d <= matchWindow+1e-9 && d < near {
					why, near = c.Why.String(), d
				}
			}
			out.Refused[why]++
		}
	}
	for _, n := range ag.All.OnlyA {
		stage := "?"
		for _, c := range ident[key{frame(n.Start), n.MIDI}] {
			if c.Kind == RipMade {
				stage = c.Stage.String()
			}
		}
		out.Added[stage]++
	}
	for _, p := range ag.All.Pairs {
		start, end, _ := ag.given(p[1])
		if p[1].MIDI < 0 || end <= start {
			continue
		}
		bound, off := max(EndShare*(end-start), EndFloor), p[0].End-end
		if endRight(off, start, end) {
			continue
		}
		e, stage := math.NaN(), "?" // its end as each stage left it
		cs := slices.Clone(ident[key{frame(p[0].Start), p[0].MIDI}])
		slices.SortStableFunc(cs, func(a, b RipChange) int { return cmp.Compare(a.Stage, b.Stage) })
		for i, c := range cs {
			if c.Kind == RipMade || c.Kind == RipMoved {
				e = c.End
			}
			if (i+1 == len(cs) || cs[i+1].Stage != c.Stage) && math.Abs(e-end) > bound {
				stage = c.Stage.String()
				break
			}
		}
		if off < 0 {
			out.Ends[stage+" early"]++
		} else {
			out.Ends[stage+" late"]++
		}
	}
	if hg != nil {
		readHits(r, *hg, tol, out)
	}
	return out
}

// readHits reads a rip's hits against a truth's into out: a lost hit at the
// stage that dropped the picked hit nearest it within tol, each picked hit
// nearest one lost, or where the bars laid it more than tol off; one with none
// picked near at the picks
func readHits(r *Rip, hg HitsAgreement, tol float64, out StageReading) {
	type picked struct {
		start, at float64
		fate      string
	}
	var picks [InstrClap][]picked
	index := map[[2]float64]int{}
	for _, c := range r.Stages {
		if c.MIDI >= 0 || c.Drum < 0 || c.Drum >= InstrClap {
			continue
		}
		k := [2]float64{c.Start, float64(c.Drum)}
		switch {
		case c.Stage == StagePicks:
			index[k] = len(picks[c.Drum])
			picks[c.Drum] = append(picks[c.Drum], picked{start: c.Start, at: math.NaN()})
		case c.Kind == RipDropped:
			if i, ok := index[k]; ok {
				picks[c.Drum][i].fate = c.Stage.String()
				if c.Why != WhyNone {
					picks[c.Drum][i].fate += ", " + c.Why.String()
				}
			}
		case c.Kind == RipMoved:
			if i, ok := index[k]; ok {
				picks[c.Drum][i].at = c.End
			}
		}
	}
	for d, l := range hg.Drums {
		used := make([]bool, len(picks[d]))
		for _, h := range l.OnlyB {
			best := -1
			for i, p := range picks[d] {
				if !used[i] && math.Abs(p.start-h.Start) <= tol && (best < 0 || math.Abs(p.start-h.Start) < math.Abs(picks[d][best].start-h.Start)) {
					best = i
				}
			}
			switch {
			case best < 0:
				out.HitsLost[StagePicks.String()]++
				continue
			case picks[d][best].fate != "":
				out.HitsLost[picks[d][best].fate]++
			case math.IsNaN(picks[d][best].at):
				out.HitsLost["?"]++
			case math.Abs(picks[d][best].at-h.Start) > tol:
				out.HitsLost[StageBars.String()+", a step off"]++
			default:
				out.HitsLost[stageTwin]++
			}
			used[best] = true
		}
		if len(l.OnlyA) > 0 {
			out.HitsAdded[StagePicks.String()] += len(l.OnlyA)
		}
	}
}
