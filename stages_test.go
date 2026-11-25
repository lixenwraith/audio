package audio

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

// TestARipsStagesAreItsOwn: what a rip's stages made, dropped and moved,
// replayed, is its notes to their ends and its hits where its bars lay them,
// through the strikes and the accounting and through the analysis's path, and
// a rip that keeps them decodes what one that does not decodes
func TestARipsStagesAreItsOwn(t *testing.T) {
	ae, slots := startLoopEngine(t)
	buf, err := ae.RenderMusic(slots, 176, Meter{}, 4, DefaultRootNote, ScalePhrygian, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		fe  RipFrontEnd
		ran []string
	}{"strikes": {struck{}, []string{"accounting", "bars", "picks", "strikes"}}, "the analysis": {nil, []string{"bars", "path", "picks"}}} {
		rp := Ripper{Params: DefaultRipParams(), FrontEnd: c.fe}
		plain, err := rp.Samples(buf, AudioSampleRate)
		if err != nil {
			t.Fatal(err)
		}
		rp.Stages = true
		r, err := rp.Samples(buf, AudioSampleRate)
		if err != nil {
			t.Fatal(err)
		}
		kept := *r
		if kept.Stages = nil; !reflect.DeepEqual(plain, &kept) {
			t.Errorf("%s: a rip that keeps its stages decodes another rip", name)
		}
		var notes, laid, ran []string
		hits := map[[2]float64]float64{}
		for _, ch := range r.Stages {
			ran = append(ran, ch.Stage.String())
			note := fmt.Sprintf("%.4f %d", ch.Start, ch.MIDI)
			switch k := [2]float64{ch.Start, float64(ch.Drum)}; {
			case ch.MIDI >= 0 && ch.Kind == RipMade:
				notes = append(notes, fmt.Sprintf("%s %.4f", note, ch.End))
			case ch.MIDI >= 0 && (ch.Kind == RipDropped || ch.Kind == RipMoved):
				if i := slices.IndexFunc(notes, func(n string) bool { return n[:len(note)] == note }); i >= 0 {
					notes = slices.Delete(notes, i, i+1)
				}
				if ch.Kind == RipMoved {
					notes = append(notes, fmt.Sprintf("%s %.4f", note, ch.End))
				}
			case ch.Stage == StagePicks:
				hits[k] = -1
			case ch.Kind == RipDropped:
				delete(hits, k)
			case ch.Kind == RipMoved:
				hits[k] = ch.End
			}
		}
		for k, at := range hits {
			laid = append(laid, fmt.Sprintf("%.4f %d", at, int(k[1])))
		}
		var want, wantHits []string
		for _, n := range r.Notes {
			want = append(want, fmt.Sprintf("%.4f %d %.4f", n.Start, n.MIDI, n.End))
		}
		for _, h := range r.Hits() {
			wantHits = append(wantHits, fmt.Sprintf("%.4f %d", h.Start, h.Drum))
		}
		for _, l := range [][]string{notes, want, laid, wantHits} {
			slices.Sort(l)
		}
		if len(want) == 0 || !slices.Equal(notes, want) || !slices.Equal(laid, wantHits) {
			t.Errorf("%s: the stages leave %d notes and %d hits, the rip has %d and %d", name, len(notes), len(laid), len(want), len(wantHits))
		}
		for _, s := range c.ran {
			if !slices.Contains(ran, s) {
				t.Errorf("%s: no %s among the stages kept", name, s)
			}
		}
	}
}

// TestAStageReadingPutsEachLossWhereItWasMade: a given note is lost at the
// first stage after which no note of its pitch starts near it, with why the
// strikes refused the strike nearest it; one a note stood at to the end is a
// twin's; an added note is its maker's, a wrong end the first stage that left it
// wrong; a lost hit is its picked hit's, dropped, laid a step off or none
func TestAStageReadingPutsEachLossWhereItWasMade(t *testing.T) {
	note := func(stage RipStage, kind RipChangeKind, start, end float64, midi int) RipChange {
		return RipChange{Stage: stage, Kind: kind, Start: start, End: end, MIDI: midi}
	}
	hit := func(stage RipStage, kind RipChangeKind, why RipWhy, start, at float64, d InstrumentType) RipChange {
		return RipChange{Stage: stage, Kind: kind, Why: why, Start: start, End: at, MIDI: -1, Drum: d}
	}
	sec := func(frame int) float64 { return float64(frame) * RipFrameSec }
	r := &Rip{
		Notes: []RipNote{{Start: sec(100), End: sec(130), MIDI: 60}, {Start: sec(200), End: sec(225), MIDI: 64}, {Start: sec(500), End: sec(520), MIDI: 72}},
		Stages: []RipChange{
			note(StageStrikes, RipMade, sec(100), sec(130), 60),
			note(StageStrikes, RipMade, sec(150), sec(170), 62),
			note(StageStrikes, RipMade, sec(200), sec(220), 64),
			{Stage: StageStrikes, Kind: RipRefused, Why: WhyAgain, Start: sec(301), End: sec(301), MIDI: 65},
			note(StagePath, RipMade, sec(500), sec(520), 72),
			note(StageGlides, RipMoved, sec(200), sec(225), 64),
			note(StageAccounting, RipDropped, sec(150), sec(170), 62),
			hit(StagePicks, RipMade, WhyNone, sec(100), 0, InstrKick),
			hit(StagePicks, RipMade, WhyNone, sec(200), 0, InstrSnare),
			hit(StagePicks, RipMade, WhyNone, sec(400), 0, InstrKick),
			hit(StagePicks, RipMade, WhyNone, sec(500), 0, InstrHihat),
			hit(StageBars, RipDropped, WhyOutside, sec(100), 0, InstrKick),
			hit(StageBars, RipMoved, WhyNone, sec(200), sec(220), InstrSnare),
			hit(StageBars, RipMoved, WhyNone, sec(400), sec(400), InstrKick),
			hit(StageBars, RipMoved, WhyNone, sec(500), sec(500), InstrHihat),
		},
	}
	given := []RipNote{{Start: sec(100), End: sec(130), MIDI: 60}, {Start: sec(102), End: sec(130), MIDI: 60}, {Start: sec(150), End: sec(170), MIDI: 62},
		{Start: sec(200), End: sec(260), MIDI: 64}, {Start: sec(300), End: sec(320), MIDI: 65}, {Start: sec(400), End: sec(420), MIDI: 67}}
	ag := MatchNotes(r.Notes, given, true)
	tol := 4 * RipFrameSec
	laid := []RipHit{{Start: sec(220), Drum: InstrSnare}, {Start: sec(400), Drum: InstrKick}, {Start: sec(500), Drum: InstrHihat}}
	hg := MatchHits(laid, []RipHit{{Start: sec(100), Drum: InstrKick}, {Start: sec(200), Drum: InstrSnare}, {Start: sec(300), Drum: InstrHihat}, {Start: sec(400), Drum: InstrKick}}, tol, true)
	got := ReadStages(r, ag, &hg, tol)
	want := StageReading{
		Lost:      StageCount{"a twin": 1, "accounting": 1, "strikes": 2},
		Refused:   StageCount{"within strike_gap": 1, "no strike near": 1},
		Added:     StageCount{"path": 1},
		Ends:      StageCount{"strikes early": 1},
		HitsLost:  StageCount{"bars, outside the bars": 1, "bars, a step off": 1, "picks": 1},
		HitsAdded: StageCount{"picks": 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the reading is\n%v\nwant\n%v", got, want)
	}
	if none := ReadStages(&Rip{Notes: r.Notes}, ag, &hg, tol); none.Lost != nil {
		t.Errorf("a rip that kept no stages reads %v", none)
	}
}
