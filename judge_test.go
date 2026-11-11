package audio

import (
	"bytes"
	"encoding/binary"
	"hash/fnv"
	"math"
	"testing"
)

// judgeSaying is a judge of one unit a layer whose stages give the odds said
// whatever the row
func judgeSaying(odds [judgeStages]float64) *RipJudge {
	j := &RipJudge{}
	for s := range j.stages {
		n := &j.stages[s]
		n.mean, n.spread = make([]float32, RipRowSize), make([]float32, RipRowSize)
		for k := range n.spread {
			n.spread[k] = 1
		}
		n.w = [3][]float32{make([]float32, RipRowSize), {0}, {0}}
		n.b = [3][]float32{{0}, {0}, {float32(math.Log(odds[s] / (1 - odds[s])))}}
	}
	return j
}

// judgeFile is j as the trainer writes it, naming the row and the notes weights
func judgeFile(j *RipJudge, row, size int, notes uint64) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	b.WriteString(judgeMagic)
	binary.Write(&b, le, []uint32{judgeVersion, uint32(row), uint32(size), uint32(len(j.stages[0].b[0])), uint32(len(j.stages[0].b[1]))})
	binary.Write(&b, le, notes)
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

// TestAJudgeIsRefusedWithOtherNotesWeightsOrARowItDoesNotKnow: a judge's file
// loads for the notes weights it names and gives its networks' odds, a row
// standardized, through two ReLU layers and a sigmoid; with other weights, a
// row of another version or size, or a file changed, it is refused.
func TestAJudgeIsRefusedWithOtherNotesWeightsOrARowItDoesNotKnow(t *testing.T) {
	const notes = 0x1122334455667788
	j := judgeSaying([judgeStages]float64{0.5, 0.5})
	for s := range j.stages { // two units in the first layer
		j.stages[s].w[0], j.stages[s].b[0], j.stages[s].w[1] = make([]float32, 2*RipRowSize), []float32{0, 0}, []float32{0, 0}
	}
	n := &j.stages[judgeDropped]
	n.mean[3], n.spread[3] = 1, 2
	n.b[0][0], n.w[0][3], n.w[0][RipRowSize+3] = 0.5, 1, -1 // the fourth value, standardized, and its negative
	n.w[1], n.b[1] = []float32{2, 3}, []float32{-1}
	n.w[2], n.b[2] = []float32{0.5}, []float32{0.25}
	loaded, err := LoadRipJudge(bytes.NewReader(judgeFile(j, RipRowVersion, RipRowSize, notes)), notes)
	if err != nil {
		t.Fatal(err)
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
	cut := judgeFile(j, RipRowVersion, RipRowSize, notes)
	cut[len(cut)/2] ^= 1
	for name, file := range map[string][]byte{
		"fitted on other notes weights": judgeFile(j, RipRowVersion, RipRowSize, notes+1),
		"of another row version":        judgeFile(j, RipRowVersion+1, RipRowSize, notes),
		"of another row size":           judgeFile(j, RipRowVersion, RipRowSize+1, notes),
		"changed":                       cut,
	} {
		if _, err := LoadRipJudge(bytes.NewReader(file), notes); err == nil {
			t.Errorf("a judge %s loaded", name)
		}
	}
}
