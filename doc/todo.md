# TODO

Where the library may grow. Each item states what remains and the smallest thing
that would close it. Delete an item when it lands; this file is not a changelog.
Source files do not carry TODO comments.

Priorities: P1 is wanted next, P2 is convenient follow-up, and P3 is an idea.

Work that changes the library and stapes together is planned in [stapes'
todo](https://github.com/lixenwraith/stapes/blob/main/doc/todo.md): tuning the
rip and closing its loop. What vif makes of the library is in [vif's
todo](https://github.com/lixenwraith/vif/blob/main/doc/todo.md#audio).

## The bank's instruments

### Bring the tonal voices nearer their recordings

- Priority: P2
- Affected files: `voice.go`, `bank/voices.toml`

The fit ([Audio §7](audio.md#7-music-sequencer)) took the piano and the bass to
Iowa's notes at pp, mf and ff by their level at 0.25 to 3 s, mel distance and
brightness. The piano's level by velocity stays the old law by the owner's
choice (pp 4 dB under mf, ff 2 dB over, where the recordings' spread is 14 and
12 dB): brightness carries the dynamic. What is left: the piano's lowest
octave still falls 4 to 6 dB faster than recorded by 3 s; its fundamental gains
the slow second string only from about C3 up (`bright`'s body rounds the first
paired partial), so f0's T60 steps from 5 s to 23 s across that semitone; the
bass's level error is 7 to 8 dB, against Iowa runs whose notes differ by as much;
and the pad and the lead are measured against main only, by their energy above
4·f0 and how alike two notes are, never against an instrument.

### Bring the drum kit nearer its recordings

- Priority: P2
- Affected files: `bank/drums.toml`, `kit.go`, `rip.go`

The kit ([Audio §7](audio.md#7-music-sequencer)) was measured against CC0 MP3
previews and Iowa's hihat, at three velocities each. What is left: an open hihat,
which the MIDI drum map's 46 reads as the closed one; a kick recorded several
times at one pressure, to set its strokes' spread (the Sonor set has one take a
pressure); and the analysis reading the natural kit's kick, ringing at 52 Hz, as
bass, which a model trained on the new corpus is to be benched against.

### Read a matched kit's weak bands

- Priority: P3
- Affected files: `kit.go`

A drum's tone reads a band 15 dB or more under its strongest through the frame
window's leakage from the strong ones and where its hits fall in the frames, so
a matched drum's eq moves such a band by chance (the stand-ins of
`TestToneReadsBackAnEQ` read 4 to 9 dB off there), and the clap is never
matched, as a rip hears none. The smallest close: leave a band the drum does not
fill at the eq's neighbouring value, once a recording shows it audible.

## The rip's notes

### Hold more of the rip to the recording

- Priority: P2
- Affected files: `account.go`, `rip.go`

The accounting ([Audio §9](audio.md#9-ripping-a-recording)) judges each note by
its onset alone. What is left: a note's velocity and end from its fitted
activation, against the half-second level the velocity head is moved by and the
heads' release, which a listening sheet decides; on a clean piano partials
reported over a chord can still pass, as the template of the note under them is
learned from frames the partial shares; a difference tone under distortion has a
fundamental of its own, which no additive fit can rule out; and the decoder's
partial rule is judged again by the fit, so it may go once a run without it
holds the graded sets (`ownPitch` stays: notes under E3 need it). The memory
through a louder note reads a note's start; it does not yet follow a note past
its end: a kept note can end while the pitch head still hears the pitch, but
under the pedal the right ones end at or after the score's (the hold is the
pedal's ring), and where lines are held the sound after an end decays as the
piano's does, so nothing yet tells a sustained line cut by the release head from
a ring, and an extension would lengthen piano notes past the score. A candidate
on a pitch still sounding stays the decoder's: judged with the memory, a held
drone is cut by its pulse. The smallest close: the velocity from the fit on a
sheet beside `dynamics`; the ends on a sheet of held lines alone. The scorer's
truth map snaps each truth note to the reported note it matched, so a partial
shadow at a real note's pitch pulls the map and the restrike labels with it; the
stage was calibrated on maps by a local line over the matched notes' offsets
instead, which the scorer itself should write. A note struck softly under its
octave's ring is a candidate whose fit sees a small rise; its memory reads its
new sound only when a partial another pitch rang on from before is predicted
through rather than left out, which costs the restrike kind, and the candidate
score refuses it either way, its level far under the reference outweighing it;
candidates under a ring with new sound, a sure pitch head and a sure strike are
too often wrong for a rule to admit them yet.

### Close what a judge leaves open

- Priority: P1
- Affected files: `account.go`, `judge.go`, `ripstream.go`

A judge can rule the accounting's verdicts ([Audio
§9](audio.md#9-ripping-a-recording)); none is accepted or shipped yet, and the
installed scores stay until one is. What the stage leaves open:

- **A followed sound is not cut.** A strike that comes back in the cell a
  followed note was struck in starts a note beside it; ending the followed
  note there would part its cells, which is the glides' decoding.
- **Candidates never see each other.** Each is fitted against the kept notes
  alone, as the installed candidates are, so two partials of one missed note
  can both come back. Judged together, a bass note's partials took its sound.
- **The rows a fit takes are not the rows its judge reads.** With no judge a
  dropped strike is measured against the notes the installed scores kept,
  under a judge against those it kept. The smallest close: rows written again
  under the first judge, if a fit on them measures better.
- **The file names the notes weights alone.** The row's flux is the frame
  model's onset head, so a judge run beside another frame model reads another
  flux. The smallest close: the file names both checksums.
- **In a stream a row ends at the newest frame.** A strike within eight frames
  of it reads zeros after it until the next read; frozen strikes are read two
  bars behind and are whole. The flux's level is binned as the registers'
  levels are, and counted as the ear gave it, which is not what the decoders
  read through a front end whose flux a stream scales again (the analysis,
  which hears no strikes).
- **The read with no judge fits every strike a second time**, apart from the
  installed candidates, whose fit moves the floor: 3% more of the loop's rip
  and a tenth more of its stream's. A judge's network is scalar float32, 0.16 M
  multiply-adds a strike: 5% more of the loop's rip, 8% of its stream's.

### Walk the notes that start together in an order of their own

- Priority: P2
- Affected files: `streams.go`

`streams` walks the melody register's notes by start, and those that start on
one frame as its unstable sort leaves the order the decoders hand them in; a
note joins the line of the note walked before it with no change of line, so the
parting follows that order where no cost ties. On stapes' `corpus <dir> 48 8
aug gen meters` through the analysis, offline and streamed, a walk by pitch at a
shared start parts many rips otherwise, and so does a stable sort. The smallest
close: an order the notes alone set, chosen by the lines' agreement with the
corpus labels (`MatchNotes`).

### Pair a tapped rhythm as the notes with pitches are

- Priority: P3
- Affected files: `match.go`

`onsets` gives each note given with no pitch, in time order, the nearest note
within its tolerance, so a fast rhythm loses a tap it could pair (taps 60 ms
apart against notes at -50 and +20 ms pair one of two); and where a transcript
mixes a tapped line with lines given with pitches, every note as one line pairs
the pitched first, so one can take across lines the note a tap took within its
line and `All` reads under the lines. The smallest close: the taps paired by
`nearPlace`'s alignment with pitch aside (a run is then every note within the
tolerance, long in a dense rip), the two kinds together where they meet.

### Write the motion a glide's note leaves out

- Priority: P2
- Affected files: `rip.go`, `voice.go`, `pattern.go`

The decoder follows a glide across pitch cells from a notes model's cents head
and writes one bent note ([Audio §9](audio.md#9-ripping-a-recording)). What a
step's bend cannot hold is heard and dropped:

- A scoop that settles before its note ends, and a fall after a held pitch
  that stops short of the note's end: a bend is one motion ending with the
  note. A step that glides to a share of its length, or a second segment.
- A vibrato: the note takes the pitch it sways about. A rate and a depth on
  the note, as a voice has them, read from the cents along it.
- A note struck again in its own cell: the cents say nothing there, so
  `StrikeAgain` judges it as before. Repeats inside a sung note, where the voice
  never leaves the cell, are of this kind.
- A sound is followed within the register its note was struck in: a glide
  across E3 reads the cells past it, but takes in no note the other register
  made there.
- `bright` measures a note's partials at its written pitch along its whole
  length, as stapes' `notes` reads its support: while a bent note moves, and
  through a scoop's rise, the sound is not there. The cells a followed sound
  passed are in `ripNote.cells`, where the accounting already reads them.

### Account for a note written beside the cell it sounds in

- Priority: P3
- Affected files: `account.go`

`glided` writes a sound that rests in one cell at the pitch beside it where the
cents say it sits nearer that pitch, and the note keeps that cell
(`ripNote.cells`), which `tell` reads its voice from. The accounting does not:
it measures a note of one cell at its written pitch (`accNotes`), and its
ringing check reads a glide's cells alone (`accInput`). So by the code a strike
in the cell such a note sounds in, the one that struck it included, is a
candidate apart from it, not the note ringing, and may come back as a second
note a semitone off. It was so before the note kept its cell, and is kept so
because reading the cell could move notes; not seen on data, as no notes model
of random weights wrote such a note. The smallest close: measure a one-cell
note in its cell and let the ringing check read it, then count the notes that
move on recordings through a notes model whose cents head writes such notes.

### Carry a note's voice further

- Priority: P2
- Affected files: `rip.go`, `ripstream.go`, `ripmodels/ripmodels.go`

A notes model with the heads says what plays each pitch, and a note carries its
voice ([Audio §9](audio.md#9-ripping-a-recording)). What is left:

- The lines are still filed by register, so a line two instruments share takes
  the mean of their genes, a voice neither plays. Lines by voice is the next
  step ([Stapes' rip design
  §7](https://github.com/lixenwraith/stapes/blob/main/doc/rip-design.md#7-plan)).
- A live rip's notes carry no voice and its melody and under lines share their
  register's mean: `freeze` lays its notes out without it. The smallest close:
  the frozen notes keep `ripNote.said` and `Final` parts the register as
  `Samples` does.
- `Hear` holds the notes model's whole output while it reads it: 4,531 values
  a frame with these heads against 571 without, 281 MB over three minutes
  against 35 MB, beside the stack's 538 MB, though the frames keep 552 bytes of
  it. The smallest close: `Hear` in stretches of frames with the notes model's reach
  either side, as the ear already hears; or a cell net that gives a head only at
  the cells asked for, in `transformer`.
- The measure is one number for every family, written in the weight file as a
  frame head of no weights, and a measure a family fits the same head at eight
  values. A file states constants by name since `transformer`'s version 6, as
  it states its frames' layout (`ripmodels.Layout`), and the measure belongs
  there: it moves once the trainer's export writes the constant, `New` reading
  either.
- A note keeps the name and not the share: `RipVoice` has no field for how far
  past the measure its family was, or for the second family. A caller that
  ranks notes by sureness needs one.
- A frame's thirteenth most salient pitch and on are told nothing; a note is
  read on its other frames. How many notes of a dense recording are told on
  under half their frames is not counted yet.

### Read a score and the analysis over the piano's pitches

- Priority: P3
- Affected files: `midi.go` (`MIDIScore.Rip`), `rip.go` (`analysisEar`)

The models tell apart the piano's pitches, while a score's rip still drops a
note outside E1 to C7 and the analysis hears no other
([Audio §9](audio.md#9-ripping-a-recording)).

- `MIDIScore.Rip` keeps its bound because stapes' corpus draws its MIDI loops
  through it: a wider one changes every such corpus's bytes, so it goes with
  the next corpus rendered, as a range the caller gives.
- The analysis's bass is the strongest pitch of its register by its first
  partials, so more pitches under E1 move its rips; nothing is gained there
  until its long window reads them apart.

## The bank's music

### Settle what a macro over a base does with a default that invokes

- Priority: P3
- Affected files: `lixenwraith/toml/macro`, `bank/music.toml`

A macro written over a base ([toml's
Macros](https://github.com/lixenwraith/toml#macros)) passes each parameter it
leaves open on to the base as an argument, its default when the invocation
gives none. A default that itself invokes a macro so expands in the macro over
the base and arrives as one value, where the base invoked alone expands it in
place, and in an array splices it: `["${p}", 9]` reads `[[1, 2], 9]` through
the one and `[1, 2, 9]` through the other. No macro the bank or vif ships has a
default that invokes, so nothing reads differently today. The root is older
than the form: in toml v0.7.0 already, an invocation given as an argument is
one element where the same invocation as a default splices. Two ways settle it
when a document first needs such a default: refuse a macro over a base that
leaves the parameter open, or pass an open parameter on only when the
invocation gives it, so the base applies its own default. The second gives up
the form's definition as a body that could be written by hand. Either is the
TOML module's to change.

### Keep a comment inside an entry through a save

- Priority: P3
- Affected files: `sound_load.go`, `lixenwraith/toml`

A save ([README](../README.md#documents)) writes every entry anew, so a comment
inside one is lost: above a key or a sub-table's header, as the eight above
`[[sound.layer]]` in `bank/drums.toml`, at a line's end, or under its last key,
which by toml's rule takes in a comment below the last entry that stands nearer
it than what follows: commented-out entries a blank line under it and two above
a closing line. The comment above an entry's own header stays, and `desc` is
data. The smallest close: toml reads a table's inner comments by the key each
stands above, and the save writes each above that key where the entry still
holds it.

## Meters

### Read more meters

- Priority: P3
- Affected files: `rip.go`

The rip reads 4/4, 3/4 and 6/8, and names them ([Audio
§9](audio.md#9-ripping-a-recording)): a 2-beat bar is 4/4 at double tempo, a
6- or 8-beat bar 3/4 or 4/4 at half, a triple bar of tracked triplets 3/4, so
a slow waltz, a slow 4/4 and a jazz waltz tracked on its swing read right. 5/4,
7/8, 9/8 and 12/8 still read as whichever of those fits. Offered as readings
beside the three, they cost the margin chain 4/4 labels, and a linear score over
path score, repetition and a prior per meter held fewer than the chain. The
smallest close: 5/4 and 7/4 as readings taken only by the full margin, graded on
e-gmd's 5/4 and 5/8 performances (7 of each on disk, one performance under seven
kits) before the 4/4 sets.

### Hear the beat and the bar line on trained weights

- Priority: P1
- Affected files: `rip.go`, `ripstream.go`; stapes' `tool/rip-train`, `tune.go`

The decoders read a beat and a downbeat head where the frames carry them
([Audio §9](audio.md#9-ripping-a-recording)), and are held on frames built by
hand alone: no model has the heads yet. The trainer's side, the targets, the
heads, the loss and the run that is this change's evidence, is [stapes'
todo](https://github.com/lixenwraith/stapes/blob/main/doc/todo.md#teach-the-frame-model-the-beat-and-the-bar-line).
What is left here, each to settle on trained weights:

- **Read against truths.** Recordings against a truth that has bar lines
  (GuitarSet's `beat_position`, a score's bars), with the heads and with the
  same weights' heads left out (a `FrontEnd` whose frames have `Beat` set nil).
- **The place of the line.** A downbeat head can mark the half bar as it marks
  the line, and the drums' cues tie there; the cues' path sums its bars, so one
  that starts with the recording and fits a bar more wins, which every rendered
  loop rewards and a recording cut anywhere does not, where a place's mean leans
  on nothing. To settle on trained heads and on truth that starts off the bar
  line, not by a lean on the start.
- **The repetition under six bars** is not asked of a heard reading: on four-bar
  loops it took 3/4 on loops in 4/4 and set no more bars right.
- **The weights the heads are read by.** `bar_head` (1) puts the downbeat
  head's lines standing out over its mean beat on the cues' scale, the beat
  head's comb (`beat_comb_2` to `4`) is a plain tracker's, and `meter_head`
  (1) is the mean beat's own level: all set by reason, for stapes' `tune ...
  truth` to fit on trained heads against truth of every meter. Under the default prior a beat head
  marking quarters at 0.98 and eighths at 0.3 reads a loop at 70 as 140; the
  plain tracker's prior (110 over 0.9 octaves) holds 70 there and turns at
  0.35. `tempo_prior` and `tempo_width` are tune's genes.
- **A fast beat.** A head marking every beat alike over 170 a minute is
  tracked at half by the prior, as a plain tracker's is, and its bars then hold
  two of the music's. What else tells the two apart (the head halfway between
  the tracked beats against at them) is to be measured on trained heads first.
- **A bar that slips.** A bar holds its beats exactly, so a beat the tracker
  adds or loses in a ritardando moves every line after it; the cues' path
  absorbs one at `bar_move`. A cost in the head's own units is to be fitted on
  played recordings, not set.
- **The stream** holds the phase of its first bars, as before: the downbeat
  head is not read again once they freeze. Reading it at every bar is the
  sections thread's.
- **Half and double bars** are not offered (2 and 8 heard beats), nor 5/4 and
  7/8: none is measured on a head. A 6/8 tracked on its dotted quarters, the
  onsets on both their thirds, wants a bar of two heard beats: it reads 4/4 or
  6/4 over them.

## The live rip

### Follow a recording on the phone

- Priority: P2
- Affected files: `ripstream.go`, `ripmodels/ripmodels.go`

`RipStream` ([Audio §9](audio.md#9-ripping-a-recording)) cost a desktop core
0.08 s a second of audio through the analysis and 0.34 s through a model of the
trainer's shapes (`FrameSpec`, and PitchNet with dilations 3, 9 and 27), 0.30 s
of it the models' ear, which ran `Forward` over a window around each half
second. The ear now runs each frame through each model once: `BenchmarkEar`,
30 s through those shapes on one core of a 4-vCPU Xeon with AVX2, medians of
five, in seconds a second of audio:

```
                                      heard    the ear waiting
                                      whole    the reach   half a second
transformer v0.5.0, windows           0.105        -          0.295
v0.6.0's streams, the GELU as it was  0.105      0.097        0.095
v0.6.0                                0.068      0.068        0.065
```

On four cores the ear takes 0.060 s where it took 0.186 and the whole 0.027
where it took 0.034: a stream's writes spread less over the cores than
`Forward` ([transformer's todo](https://github.com/lixenwraith/transformer/blob/main/doc/todo.md)).

What is left: the cost on the phone, where the NEON GELU is untimed; the frame
model's heads under the shorter wait (`FrontEnd.Ahead`), which on random
weights of `FrameSpec`'s shape lie from the whole's, as the rip reads them, by
0.09 rms for the onset (0.16 at worst), 0.04 for the drums (0.16), 0.19 for the
wave (0.69, of rms 1.45), 0.05 for the voice (0.20) and 0.03 for the family
(0.17), and on trained weights are unmeasured: `go test ./ripmodels -run
TestTheEarHearsAsTheWholeRecording -v -args -model <file> -notes <file> -wav
<file>` logs each head's under both waits; the tempo, meter and key held from
the first six bars, so a recording that changes meter or key reads on in the
first until `Final`; and the notes' lag, a median taken as they come, which
moved one note a bar a step in five early bars of the loop at 175 bpm. The
smallest close: measure on the phone, and score a trained model's stream at each
wait against its offline rip (`MatchBars`, `BarDistance`).

### Give each model's frames at its own reach

- Priority: P2
- Affected files: `rip.go` (`RipEar`, `RipFrames`), `ripstream.go`,
  `ripmodels/ripmodels.go` (`ear`)

The ear gives both models' frames in one step, so under the default wait the
notes model's heads (pitch, strike, release, velocity), complete 46 frames,
0.53 s, behind the sound, wait in the ear for the frame model's 256, 2.97 s:
they could come 210 frames, 2.44 s, sooner. What it takes:

- `RipEar.Hear` gives frames whose fields end at two frames: `RipFrames` holds
  one length for every field (`frames`, `extend`, `cut`), so either a count for
  the notes model's fields (`Salience`, `Chroma`, `BassChroma`, `Strike`,
  `Release`, `Velocity`) beside the others', or the two models' frames apart;
- `RipStream` reads up to one frame (`heard`, `next`, `decode`'s `upto`):
  `events` would read the pitched notes up to the notes model's front and the
  hits up to the frame model's, and `count` each register's level and each
  drum's loudest at its own;
- a bar still freezes at the frame model's front, since its line is tracked on
  the onset head and cued by the hits and the bass (`beats`, `lineCues`): what
  comes sooner is a view's, the notes' fields in `Frames`, and the open bars'
  notes if `Open` lays them on lines carried on at the beat held. Frozen bars
  come sooner only if those lines are then held.

### Hear again the frames a louder sample reaches

- Priority: P3
- Affected files: `ripmodels/ripmodels.go` (`ear`)

A frame is scaled by the loudest sample heard when it came and run once, so a
model's heads are the whole's from its reach past the recording's loudest
sample; the frames before were scaled by the loudest of their time, and those
within a reach of a new loudest sample read frames scaled by two levels.
Hearing again, at each new loudest sample, the frames not yet given and the
reach before them would make every frame given from then on the whole's: at
the default wait each time runs the frame model over 555 frames again, 6.4 s
of them, and the notes model over 345. Not taken: each frame runs once.

### Read more of a stream from outside

- Priority: P2
- Affected files: `ripstream.go`, `rip.go`

A view reads a stream's frames with their spectrum (`Frames`), its open bars
(`Open`) and the strikes judged with what became of each (`Strikes`) ([Audio
§9](audio.md#9-ripping-a-recording)). The open bars and their strikes are
those of the last read that froze a bar, about a bar old, and none come before
the first bars freeze: laying them out at every half-second
read was not taken, since `lay`'s `bright` pass alone is 28% of the stream's
time through the analysis (a profile of the 140 bpm loop) and it would run
three to four times as often there. The smallest close: a layout of the bars
after the frozen ones alone, their brightness read over the notes not yet
frozen. Through a model the spectrum comes with its frames, three to three and
a half seconds behind, though its `MelStream` holds each band 46 ms after the
sound: a
view that wants it sooner needs the bands handed on before the frames they
belong to.

## The mixer

### Give the mix a binaural bed

- Priority: P3
- Affected files: `mixer.go`, `engine.go`, `command.go`, `doc/audio.md` §2 and §6
- Guide: [Audio §2](audio.md#2-stream-contract),
  [§6](audio.md#6-mixer-buses-mute-and-pause)

vif is to offer a binaural beat under its music, off by default ([its
todo](https://github.com/lixenwraith/vif/blob/main/doc/todo.md#add-binaural-beats-to-the-music)):
a tone of `f − Δ/2` in the left ear and `f + Δ/2` in the right, heard as a beat
at `Δ`. No shipped pattern, test song or rip carries one. The stream is stereo
end to end: every backend opens two channels (`pacat`, `pw-cat`, `aplay`,
`sox`, `ffplay`, the WAV sink), and vif's hosts take two apart, Android's
`AudioTrack` as `CHANNEL_OUT_STEREO` and the browser's worklet writing `left`
and `right`. The mix is not: the music and sfx buses are one `float64` channel,
and `floatToBytes` writes each sample to both (§2). So nothing of the hosts
changes; the mixer's last stage does.

Where the beat is made, each a choice with what it costs:

1. **A stereo bed in the mixer** (recommended). A generator the mixer owns, two
   phase-continuous sines summed into L and R after the music bus, before the
   pause ramp and the limiter, which becomes stereo-linked (its envelope the
   larger of the two channels). `floatToBytes` takes two inputs; nothing before
   the mix changes: voices, tracks, the sequencer, its crossfades, reveals,
   fills and draws stay mono and unaware. Cost: two oscillators a sample, a new
   command (`SetBinaural{Carrier, Beat, Level}` on `AudioEngine`, slewed inside
   the mixer so a change never clicks), the music tap written in stereo so what
   vif's `-mw` records is what plays. Smallest change, one owner, every host at
   once; the bed is a tone under the music rather than part of it.
2. **One more track.** A pattern track whose voice holds a note per ear. It reads
   as one track in the bank, but a track renders into the mono music bus, so it
   still needs option 1's stereo output and a stereo voice besides; and in a slot
   it is redrawn on phrase downbeats, faded by crossfades, rebuilt by reveals one
   track a bar, replaced by slot 2's fill and moved with the group every four
   phrases, each a gap in the beat, while `Δ` changes on the step grid, not
   continuously. Kept out of the draw it becomes a fifth slot, which is option 1
   inside the sequencer. Its one gain: a group could author its own carrier in
   `music.toml`.
3. **A stereo music bus.** The sequencer renders L and R through every slot,
   player and voice, and the beat is an ear offset of a constant `Δ` Hz on a
   tonal voice (the pad), so every partial pair beats at `Δ` inside the music.
   Most musical, and it brings panning per track with it, which the engine lacks
   (`AudioEngine.PlayBuffer` takes no pan); but it touches the whole pipeline,
   doubles the voices' render cost, the offline renders (`RenderMusic`,
   `RenderTimeline`) and stapes' with it, and the beat sounds only while the pad
   plays.

The carrier `f`: binaural beats are heard best on carriers of a few hundred
hertz and fade above about 1 kHz, and `Δ` blurs into roughness above about
30 Hz. Tuning `f` to the harmony's root (default E2, 82 Hz, so E3 or E4 at 165
or 330 Hz) keeps the bed consonant with the key; it then glides on a key change.
A fixed `f` is simpler and may clash.

Decided with the implementation: whether the bed follows the music bus's mute
or a mute of its own, and whether it ducks under sfx (amplitude only, `Δ` holds
either way). Tests, one per rule: off renders L equal to R; on, each channel's
frequency (Goertzel) is `f ∓ Δ/2` and a change of `Δ` keeps the phase
continuous; the linked limiter holds both channels under its ceiling. It takes
the next minor version, which vif then requires; stapes auditions the bed with
a verb ([its
todo](https://github.com/lixenwraith/stapes/blob/main/doc/todo.md#audition-the-binaural-bed)).

## The repository

### Rewrite the history without the third-party names

- Priority: P1
- Affected files: every commit

v0.27.2 took a game's name, a drum machine's and a song's out of the bank and
the docs, and gave the coin a cue of its own. Older trees, commit messages and
squash titles still carry them. A `git filter-repo` pass with
`--replace-message`, from the list of names kept in the project's files outside
the repository since it spells them, takes them out of every message. Whether
the same pass takes them out of the old trees too, with `--replace-text` from
the same list, is the choice to make first, since 44 of main's commits are
tagged versions:

- Messages only: every tree stays as it is, so each tag set again on its
  rewritten commit gives a module with the checksum `go` already holds. The old
  trees keep the names.
- Messages and trees: the names leave every branch and tag, but each published
  version's tree changes, and its re-set tag then fails `go`'s checksum check
  for anyone who fetches it directly.

Either way:

- Every hash changes: each clone re-clones, open branches are rebased by hand,
  every commit on main, each a squash merge GitHub signed, loses its signature,
  and every tag is set again on its rewritten commit.
- proxy.golang.org and sum.golang.org keep each published version as it was
  tagged, so the proxy keeps serving the old trees, names and all.
- GitHub keeps every PR's `refs/pull/<n>/head`, which no push deletes, so the
  old commits stay reachable there and by hash in cached views. GitHub Support
  removes only sensitive data that rotating a credential cannot make safe, so it
  will not purge them. PR titles and bodies are edited on GitHub by hand.
