package audio

import (
	"bytes"
	"encoding/binary"
	"hash/fnv"
	"math"
	"strings"
	"testing"
)

// judgeSaying is a judge of one unit a layer whose stages give the odds said
// whatever the row
func judgeSaying(odds [judgeStages]float64) *RipJudge { return judgeSized(1, 1, odds) }

// judgeSized is a judge of layers h1 and h2 wide whose stages give the odds
// said whatever the row
func judgeSized(h1, h2 int, odds [judgeStages]float64) *RipJudge {
	j := &RipJudge{}
	for s := range j.stages {
		n := &j.stages[s]
		n.mean, n.spread = make([]float32, RipRowSize), make([]float32, RipRowSize)
		for k := range n.spread {
			n.spread[k] = 1
		}
		n.w = [3][]float32{make([]float32, h1*RipRowSize), make([]float32, h2*h1), make([]float32, h2)}
		n.b = [3][]float32{make([]float32, h1), make([]float32, h2), {float32(math.Log(odds[s] / (1 - odds[s])))}}
	}
	return j
}

// judgeFile is j as the trainer writes it, naming the row and the weights it is
// read through as version names them
func judgeFile(j *RipJudge, version, row, size int, w RipWeights) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	b.WriteString(judgeMagic)
	binary.Write(&b, le, []uint32{uint32(version), uint32(row), uint32(size), uint32(len(j.stages[0].b[0])), uint32(len(j.stages[0].b[1]))})
	if version == 1 {
		binary.Write(&b, le, w.NotesSum)
	} else {
		binary.Write(&b, le, []uint64{w.Notes, w.Frame})
	}
	for _, n := range j.stages {
		for _, v := range [][]float32{n.mean, n.spread, n.w[0], n.b[0], n.w[1], n.b[1], n.w[2], n.b[2]} {
			binary.Write(&b, le, v)
		}
	}
	h := fnv.New64a()
	h.Write(b.Bytes())
	binary.Write(&b, le, h.Sum64())
	return b.Bytes()
}

// TestAJudgeIsRefusedThroughOtherWeightsOrARowItDoesNotKnow: a judge's file
// loads for the weights it names and gives its networks' odds, a row
// standardized, through two ReLU layers and a sigmoid; read through other heads
// of the notes or the frame model, refused by the model's name, as a version 1
// file is with other notes weights, and with a row of another version or size or
// a file changed.
func TestAJudgeIsRefusedThroughOtherWeightsOrARowItDoesNotKnow(t *testing.T) {
	w := RipWeights{Notes: 0x1122334455667788, Frame: 0x99aabbccddeeff00, NotesSum: 0x0123456789abcdef}
	j := judgeSaying([judgeStages]float64{0.5, 0.5})
	for s := range j.stages { // two units in the first layer
		j.stages[s].w[0], j.stages[s].b[0], j.stages[s].w[1] = make([]float32, 2*RipRowSize), []float32{0, 0}, []float32{0, 0}
	}
	n := &j.stages[judgeDropped]
	n.mean[3], n.spread[3] = 1, 2
	n.b[0][0], n.w[0][3], n.w[0][RipRowSize+3] = 0.5, 1, -1 // the fourth value, standardized, and its negative
	n.w[1], n.b[1] = []float32{2, 3}, []float32{-1}
	n.w[2], n.b[2] = []float32{0.5}, []float32{0.25}
	loaded, err := LoadRipJudge(bytes.NewReader(judgeFile(j, judgeVersion, RipRowVersion, RipRowSize, w)), w)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRipJudge(bytes.NewReader(judgeFile(j, 1, RipRowVersion, RipRowSize, w)), w); err != nil {
		t.Errorf("a version 1 judge for the notes weights loaded: %v", err)
	}
	row := make([]float32, RipRowSize)
	for _, c := range []struct{ v, want float64 }{{5, 0.5*(2*(2+0.5)-1) + 0.25}, {-5, 0.5*(3*3-1) + 0.25}, {1, 0.25}} {
		row[3] = float32(c.v)
		if got, want := loaded.odds(judgeDropped, row), 1/(1+math.Exp(-c.want)); math.Abs(got-want) > 1e-6 {
			t.Errorf("a row of %v gives odds %.6f, want %.6f", c.v, got, want)
		}
	}
	if got := loaded.odds(judgeReported, row); got != 0.5 {
		t.Errorf("the reported stage gives %.6f, want its own network's 0.5", got)
	}
	cut := judgeFile(j, judgeVersion, RipRowVersion, RipRowSize, w)
	cut[len(cut)/2] ^= 1
	notes, frame, sum := w, w, w
	notes.Notes, frame.Frame, sum.NotesSum = w.Notes+1, w.Frame+1, w.NotesSum+1
	for name, c := range map[string]struct {
		file []byte
		says string
	}{
		"through the notes model's heads of other weights": {judgeFile(j, judgeVersion, RipRowVersion, RipRowSize, notes), "notes model"},
		"through the frame model's heads of other weights": {judgeFile(j, judgeVersion, RipRowVersion, RipRowSize, frame), "frame model"},
		"of version 1 on other notes weights":              {judgeFile(j, 1, RipRowVersion, RipRowSize, sum), "notes weights"},
		"of another row version":                           {judgeFile(j, judgeVersion, RipRowVersion+1, RipRowSize, w), "row version"},
		"of another row size":                              {judgeFile(j, judgeVersion, RipRowVersion, RipRowSize+1, w), "row version"},
		"changed":                                          {cut, "checksum"},
	} {
		if _, err := LoadRipJudge(bytes.NewReader(c.file), w); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("a judge %s: %v", name, err)
		}
	}
}
