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

## Meters

### Change the meter or the tempo inside a song

- Priority: P2
- Affected files: `song.go`, `sequencer.go`, `engine.go`, `midi.go`

A song holds one meter and one tempo ([Audio §7](audio.md#7-music-sequencer)),
so `MIDIScore.Song` plays a file at its first tempo and its bars of another
meter out of time, and says so. The smallest close: a song bar that names a
meter or a tempo, the sequencer taking it at that bar line as `SetMeter` already
waits for one, `RenderLines` likewise, and the MIDI writers an event there.

### Hear a played take's bar by its repetition

- Priority: P2
- Affected files: `rip.go`

`repeats` ([Audio §9](audio.md#9-ripping-a-recording)) picks 3/4 over 4/4 for
renders and loops whose bar lines' cues tie, but not for a played take: four
takes of Für Elise, at 147 to 150 bpm over a hundred bars, still read 4/4 and
need `Ripper.Meter` set to 3/4, which reads them right. Rubato may smear the
frames' chroma that `repeats` compares at fixed lags; that is not confirmed. A
bar's distance to its nearest other bar, over the rip's quantized notes and
hits, does separate the readings: 3/4's against 4/4's is 0.527 against 0.570 on
a clean take, and 0.671 against 0.706 and 0.611 against 0.667 on two phone
recordings, while 4/4 recordings go the other way, by 0.006 to 0.09. The
distance ties on a loop, whose bars in another meter repeat too, three bars on,
where `repeats` decides. The smallest close: that distance as a cue beside
`repeats`, graded on the played takes and on renders and loops of each meter.

### Follow the bar line through a recording

- Priority: P3
- Affected files: `rip.go`

The rip decodes one bar line for the whole recording ([Audio
§9](audio.md#9-ripping-a-recording)), the phase whose lines carry the most cues,
so a performance that drifts against its bars keeps a phase that fits only part
of it: a played take of Für Elise spreads its bass notes evenly over the three
beats of a bar, as rubato drifting from one phase would. The smallest close: the
phase decoded over windows of a few bars, a change taken only where the next
windows hold it.

### Read more meters

- Priority: P3
- Affected files: `rip.go`

The rip ([Audio §9](audio.md#9-ripping-a-recording)) reads one meter for the
whole recording, 4/4, 3/4, 6/8 or 12/8: 2/4 reads as 4/4, and 5/4, 7/8 or 9/8 as
whichever of those fits. A 12/8 reads as 6/8 unless its four-beat lines carry
twice the margin; a waltz the beat tracker takes at half its tempo has no whole
bar in its beats and reads 4/4; and a tracker that slips by a sixteenth hides the
thirds a dotted beat holds. A fast 4/4 opens the dotted readings where the
tracker locks at 4/5 or 2/3 of its tempo or noise smears its sixteenths onto the
beat's thirds: 4 of 56 4/4 loops of `corpus <dir> 48 16 gen meters`, clean and
augmented, at 140 to 176 bpm read 6/8 or 12/8. The smallest close: a reading per
further meter in `readings`, each graded on renders as `TestRipHearsTheMeter`
grades 3/4 and 6/8.

### Tell 3/4 from 6/8

- Priority: P3
- Affected files: `rip.go`

3/4 and 6/8 share a bar of 12 steps ([Audio
§9](audio.md#9-ripping-a-recording)), and the rip offers 6/8 only when it hears
the tracked beats as dotted quarters (`thirds`), so a 6/8 whose beats are
tracked as quarters can read 3/4, its bar right and its label wrong. `repeats`
cannot split them, since their lags are the same. On stapes'
`bench 96 16 meters`, 36 of the 48 rips in 3/4 or 6/8, clean and augmented, read
the right bar and 25 the right label: 10 of the 11 with the bar right and the
label wrong are 6/8 read as 3/4. A wrong label keeps the bar and its notes, and
changes only the meter the song and its MIDI file name. The smallest close: a
6/8 reading beside 3/4 where the beats read as quarters, at their tempo, its
lines' cues weighing a snare and bass on the bar's step 6 against its steps 4
and 8.
