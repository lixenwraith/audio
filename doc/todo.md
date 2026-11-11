# TODO

Where the library may grow. Each item states what remains and the smallest thing
that would close it. Delete an item when it lands; this file is not a changelog.
Source files do not carry TODO comments.

Priorities: P1 is wanted next, P2 is convenient follow-up, and P3 is an idea.

Work that changes the library and stapes together is planned in [vif's
todo](https://github.com/lixenwraith/vif/blob/main/doc/todo.md#audio): tuning the
rip and closing its loop.

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
pressure); and the analysis's bass and melody under the natural kit (stapes'
`bench 12 4` clean 0.15 to 0.03 and 0.17 to 0.11, augmented 0.04 to 0.08 and
0.13 to 0.13), a kick ringing at 52 Hz read as bass, which a model trained on
the new corpus is to be benched against.

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

The accounting ([Audio §9](audio.md#9-ripping-a-recording)) judges each note
by its onset alone. What is left: a note's velocity and end from its fitted
activation, against the half-second level the velocity head is moved by and
the heads' release, which a listening sheet decides; on a clean piano half the
partials reported over a chord still pass, as the template of the note under
them is learned from frames the partial shares; a difference tone under
distortion has a fundamental of its own, which no additive fit can rule out;
and the decoder's partial rule is judged again by the fit, so it may go once
a run without it holds the graded sets (`ownPitch` stays: without it the
register's F1 holds within .005 on Für Elise's renders and takes while
E2-D#3 loses .03 to .06 on five of the seven renders). The memory through a
louder note reads a note's start; it does not yet follow a note past its end:
on the clean take two fifths of the kept notes end while the pitch head still
hears the pitch, but the right ones end at or after the score's (the hold is
the pedal's ring), and on the study, where the held lines are, the sound after
an end decays as the piano's does, so nothing yet tells a sustained line cut by
the release head from a ring, and an extension would lengthen piano notes past
the score. A candidate on a pitch still sounding stays the decoder's: judged
with the memory, the study's C3 drone was cut in half by its pulse. The
smallest close: the velocity from the fit on a sheet beside `dynamics`; the
ends on a sheet of the study alone. The scorer's truth map snaps each truth
note to the reported note it matched, so a partial shadow at a real note's
pitch pulls the map by 100 ms and the restrike labels with it; the stage was
calibrated on maps by a local line over the matched notes' offsets instead
(`tmp/explain/py/smoothmap.py`), which the scorer itself should write. A
note struck softly under its octave's ring (the E3 at 8.52 s of the clean
take, Sheet 12's miss) is a candidate whose fit sees a 2 dB rise; its memory
reads its new sound only when a partial another pitch rang on from before
is predicted through rather than left out, which costs the restrike kind
across the four sets (same-pitch extras gone 11 of 24 against 15 on the
clean take), and the candidate score refuses it either way, the level 19 dB
under the reference outweighing it; candidates under a ring with new sound,
a sure pitch head and a sure strike are right in only half of the clean
take's cases and a tenth of the Pixel take's, so no rule admits them yet.

### The misses and extras the ear still hears

- Priority: P1
- Affected files: `account.go`, `rip.go`

Sheet 13, on the memory's build, scored Für Elise clean 4 on the melody, the
low notes, the timing and overall and 5 on note length, the study 3 and Billie
Jean 3 to 4, every ask Better, and by ear better overall with misses and
extras still. What is left, by ear and by the truths: on Für Elise clean some
melody notes are missing, the under voice's soft notes of the bass figures
above all (E3-D#4 right 162 of 246 where the decoder reported 175, the note
under its octave's ring among them), and some low extras remain; at 27 to 31
s the notes linger: every note there is right and none missing, but the C5 at
29.9 s, the E3 at 30.4 s and the B4 and E2 at 31.4 s run 0.3 to 1.0 s past the
score's ends under the pedal, which the ear hears as timing off, the other
side of the ends above; on the study the melody is 40% right at 5.7 to 12 s
with in-between notes on the beat's melody, which wants the notes model more
than the accounting. The smallest close: the ends from the fitted activation
on a Für Elise sheet, and the under voice's soft notes judged as a population
of their own. A sheet's Note length row goes where extra sounds take over.

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
  `StrikeAgain` judges it as before. The 69 repeats inside a sung note on
  vocadito are of this kind where the voice never left the cell.
- A sound is followed within the register its note was struck in: a glide
  across E3 reads the cells past it, but takes in no note the other register
  made there.

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
beside the three, over the labeled sets (the corpus, e-gmd, GuitarSet, Für Elise), the
margin chain held its 4/4 labels only without them, and a linear score over
path score, repetition and a prior per meter could not hold them within 2% of
the chain on any prior set tried. The smallest close: 5/4 and 7/4 as readings
taken only by the full margin, graded on e-gmd's 5/4 and 5/8 performances (7
of each on disk, one performance under seven kits) before the 4/4 sets.

### Hear the phone takes' bars

- Priority: P2
- Affected files: `rip.go`, `ripmodel/`

On the exact-clock renders of Für Elise the bar path puts three quarters of
the lines within a sixteenth of the render's and on the clean take half; on
the three phone takes a fifth, as on main. Their onset head (the model's, 12a)
smears a note over 50-80 ms and fires between notes at a third of a strike's
strength, so the comb cannot tell a sixteenth from the gap beside it and the
tracker lays a pulse or two too many through a ritardando. A sharper onset
head, trained on the phone channel (`rip-train --channel`), is the lever; the
decoders need no change to use it.

## The live rip

### Follow a recording on the phone

- Priority: P2
- Affected files: `ripstream.go`, `ripmodel/ripmodel.go`

`RipStream` ([Audio §9](audio.md#9-ripping-a-recording)) cost a desktop core
0.08 s a second of audio through the analysis and 0.34 s through a model of the
trainer's shapes (`Spec`, and PitchNet with dilations 3, 9 and 27), 0.30 s of it
the model's ear, which ran `Forward` over a window around each half second.
The ear now runs each frame through each model once: `BenchmarkEar`, 30 s
through those shapes on one core of a 4-vCPU Xeon with AVX2, medians of five,
in seconds a second of audio:

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
weights of `Spec`'s shape lie from the whole's, as the rip reads them, by 0.09
rms for the onset (0.16 at worst), 0.04 for the drums (0.16), 0.19 for the
wave (0.69, of rms 1.45), 0.05 for the voice (0.20) and 0.03 for the family
(0.17), and on trained weights are unmeasured: `go test ./ripmodel -run
TestTheEarHearsAsTheWholeRecording -v -args -model <file> -notes <file> -wav
<file>` logs each head's under both waits; the tempo, meter and key held from
the first six bars, so a recording that changes meter or key reads on in the
first until `Final`; and the notes' lag, a median taken as they come, which
moved one note a bar a step in five early bars of the loop at 175 bpm. The
smallest close: measure on the phone, and score a trained model's stream at
each wait against its offline rip (`MatchBars`, `BarDistance`).

### Give each model's frames at its own reach

- Priority: P2
- Affected files: `rip.go` (`RipEar`, `RipFrames`), `ripstream.go`,
  `ripmodel/ripmodel.go` (`ear`)

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
- Affected files: `ripmodel/ripmodel.go` (`ear`)

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

A view reads a stream's frames with their spectrum (`Frames`) and its open bars
(`Open`) ([Audio §9](audio.md#9-ripping-a-recording)). Two reads are left.
What the decoders made of a read, each candidate with what became of it, is
not kept: it comes with the judge ([Stapes' rip design
§7](https://github.com/lixenwraith/stapes/blob/main/doc/rip-design.md#7-plan)),
whose verdicts are what a view shows. And the open bars are those of the last
read that froze a bar, about a bar old: laying them out at every half-second
read was not taken, since `lay`'s `bright` pass alone is 28% of the stream's
time through the analysis (a profile of the 140 bpm loop) and it would run
three to four times as often there. The smallest close: a layout of the bars
after the frozen ones alone, their brightness read over the notes not yet
frozen. Through a model the spectrum comes with its frames, three to three and
a half seconds behind, though its `MelStream` holds each band 46 ms after the
sound: a
view that wants it sooner needs the bands handed on before the frames they
belong to.
