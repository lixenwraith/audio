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

## Meters

### Change the meter or the tempo inside a song

- Priority: P2
- Affected files: `song.go`, `sequencer.go`, `engine.go`, `midi.go`

A song holds one meter and one tempo ([Audio §7](audio.md#7-music-sequencer)),
so `MIDIScore.Song` plays a file at its first tempo and its bars of another
meter out of time, and says so. The smallest close: a song bar that names a
meter or a tempo, the sequencer taking it at that bar line as `SetMeter` already
waits for one, `RenderLines` likewise, and the MIDI writers an event there.

### Hear a bar by its repetition

- Priority: P2
- Affected files: `rip.go`

The meter's readings ([Audio §9](audio.md#9-ripping-a-recording)) are told
apart by their bar lines' cues, a kick, no snare, the bass's root and its
accent, which a loop whose kick and bass strike every quarter leaves even:
stapes' `bench 24 16 meters` reads the bar of 7 of 10 rips of its 3/4 and 6/8
loops, the two at 96 bpm as 4/4. The smallest close: a cue of how alike a
reading's bars are, onsets and chroma at its pulses, since a loop repeats every
bar of its own meter and of no other, graded on that bench and kept off 4/4's.

### Read more meters

- Priority: P3
- Affected files: `rip.go`

The rip ([Audio §9](audio.md#9-ripping-a-recording)) reads one meter for the
whole recording, 4/4, 3/4, 6/8 or 12/8: 2/4 reads as 4/4, and 5/4, 7/8 or 9/8 as
whichever of those fits. A 12/8 reads as 6/8 unless its four-beat lines carry
twice the margin; a waltz the beat tracker takes at half its tempo has no whole
bar in its beats and reads 4/4; and a tracker that slips by a sixteenth hides the
thirds a dotted beat holds. The smallest close: a reading per further meter in
`readings`, each graded on renders as `TestRipHearsTheMeter` grades 3/4 and 6/8.
