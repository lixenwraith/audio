package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
)

// The judge file: a magic and version, the row's version and size, the two
// hidden layers' sizes and what its row was read through, the digests of the
// heads it reads of the notes and of the frame model (version 1: the notes
// weights' checksum); then for the reported notes and for the dropped strikes the
// row's mean and spread and each layer's weights and biases, an output's inputs
// together, as little-endian float32; and an FNV-1a 64 of everything before it.
const (
	judgeMagic   = "VIFJ"
	judgeVersion = 2
	judgeWidest  = 4096 // the widest hidden layer a file may name
)

// RipWeights is what a judge's row is read through, by which a judge's file
// names the weights it was fitted on: the digests of the heads the row reads
// of the notes and of the frame model, which a file of version 2 names, and the
// checksum the file of the weights that give the notes ends in, which version 1
// named (ripmodels.FrontEnd.Weights)
type RipWeights struct {
	Notes, Frame, NotesSum uint64
}

// The stages a judge rules, a network each
const (
	judgeReported = iota // the notes the decoder reported
	judgeDropped         // the strikes it dropped
	judgeStages
)

// RipJudge is a trained judge of a rip's strikes, in place of the accounting's
// installed scores: for each stage a network over the strike's row (RipRowSize)
// that gives the odds it is a note of the recording. It is fitted on the rows
// one notes model gives, and rules no other's. It is safe for concurrent use.
type RipJudge struct {
	stages [judgeStages]judgeNet
}

// judgeNet is a stage's network: the row standardized by the fit's mean and
// spread, two hidden layers with ReLU and one output through a sigmoid
type judgeNet struct {
	mean, spread []float32
	w, b         [3][]float32
}

// LoadRipJudge reads a judge file for a rip whose row is read through w, and
// refuses one fitted through other weights, naming the model, or on a row this
// package does not build.
func LoadRipJudge(r io.Reader, w RipWeights) (*RipJudge, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	if len(data) < 4+5*4+8+8 || string(data[:4]) != judgeMagic {
		return nil, errors.New("rip: not a judge file")
	}
	h := fnv.New64a()
	h.Write(data[:len(data)-8])
	if le.Uint64(data[len(data)-8:]) != h.Sum64() {
		return nil, errors.New("rip: judge file checksum")
	}
	v := le.Uint32(data[4:])
	if v < 1 || v > judgeVersion {
		return nil, fmt.Errorf("rip: judge file version %d, want 1 to %d", v, judgeVersion)
	}
	row, size, h1, h2 := int(le.Uint32(data[8:])), int(le.Uint32(data[12:])), int(le.Uint32(data[16:])), int(le.Uint32(data[20:]))
	if row != RipRowVersion || size != RipRowSize {
		return nil, fmt.Errorf("rip: the judge reads row version %d of %d values, this rip builds version %d of %d", row, size, RipRowVersion, RipRowSize)
	}
	header := 4 + 5*4 + 8*int(v)
	if len(data) < header+8 {
		return nil, errors.New("rip: judge file cut short")
	}
	if fitted := le.Uint64(data[24:]); v == 1 && fitted != w.NotesSum {
		return nil, fmt.Errorf("rip: the judge was fitted on the notes weights %016x, not on those loaded, %016x", fitted, w.NotesSum)
	}
	for i, model := range []struct {
		name string
		now  uint64
	}{{"notes model", w.Notes}, {"frame model", w.Frame}} {
		if fitted := le.Uint64(data[24+8*i:]); v == 2 && fitted != model.now {
			return nil, fmt.Errorf("rip: the judge was fitted through the %s's heads its row reads at %016x, these read %016x", model.name, fitted, model.now)
		}
	}
	if h1 < 1 || h1 > judgeWidest || h2 < 1 || h2 > judgeWidest {
		return nil, fmt.Errorf("rip: judge layers of %d and %d", h1, h2)
	}
	body := data[header : len(data)-8]
	if len(body) != judgeStages*4*(2*RipRowSize+h1*RipRowSize+h1+h2*h1+h2+h2+1) {
		return nil, errors.New("rip: judge file cut short")
	}
	take := func(n int) []float32 {
		out := make([]float32, n)
		for i := range out {
			out[i] = math.Float32frombits(le.Uint32(body[4*i:]))
		}
		body = body[4*n:]
		return out
	}
	j := &RipJudge{}
	for s := range j.stages {
		n := &j.stages[s]
		n.mean, n.spread = take(RipRowSize), take(RipRowSize)
		for l, sz := range [...][2]int{{RipRowSize, h1}, {h1, h2}, {h2, 1}} {
			n.w[l], n.b[l] = take(sz[0]*sz[1]), take(sz[1])
		}
		for _, v := range n.spread {
			if !(v > 0) {
				return nil, errors.New("rip: a judge's spread is not over zero")
			}
		}
	}
	return j, nil
}

// odds is the odds a stage's network gives a row
func (j *RipJudge) odds(stage int, row []float32) float64 {
	n := &j.stages[stage]
	in := make([]float32, len(row), len(row)+len(n.b[0])+len(n.b[1])+1)
	for k, v := range row {
		in[k] = (v - n.mean[k]) / n.spread[k]
	}
	for l := range n.w {
		out := in[len(in) : len(in)+len(n.b[l])]
		for i := range out {
			sum := n.b[l][i]
			for k, w := range n.w[l][i*len(in):][:len(in)] {
				sum += w * in[k]
			}
			if l < len(n.w)-1 {
				sum = max(sum, 0)
			}
			out[i] = sum
		}
		in = out
	}
	return 1 / (1 + math.Exp(-float64(in[0])))
}
