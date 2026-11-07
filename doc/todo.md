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
E2-D#3 loses .03 to .06 on five of the seven renders). The smallest close: the
velocity from the fit on a sheet beside `dynamics`.

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
