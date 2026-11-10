# Agent Notes

What agents working on this repository learned that no rule in
[AGENTS.md](../AGENTS.md) and nothing in the [README](../README.md) or
[audio.md](audio.md) states: facts that cost a session an hour to rediscover,
and how to check them. Read it before starting. When a task teaches you one, add
it in the same PR, one bullet under its topic; delete a note that proves wrong or
that a document now states, and link the document instead.

## Toolchain

- The `gofmt` on a container's `PATH` can be older than the module's Go; run
  `"$(go env GOROOT)/bin/gofmt" -l`.
- A container whose Go is older than the module's, where none may be fetched,
  builds and tests with `-modfile` (a copy of `go.mod` at its Go, `toml` and
  `transformer` replaced by local copies whose `go.mod` say the same) and
  `-overlay` for the few lines that use newer library calls
  (`reflect.Type.Fields`, `errors.AsType`); CI's toolchain is the authority.

## Tests

- A test that needs voices registers stand-ins: the root package's tests run
  with an empty voice registry (`VoiceDefByName` is nil until an embedder hands
  the engine the bank's). `ResetRegistries` in a cleanup.
- The mixer drops ticks under load, so audio time lags the wall clock and one tick
  can take 200 ms under `-race`: a test waiting on mixer state gives it seconds,
  not periods. Force it with a sleep at the top of `Mixer.renderTick`.
- CI's `go test -race` runs the root package at eight to sixteen times its time
  here, by the runner: 377 s for its 25 s at v0.13.0, and 257 s and 515 s for
  its 32 s at v0.14.0, so the race step runs with `-timeout 20m`. A test that
  rips whole recordings costs CI that factor: keep one to the fewest bars that
  still fail when its rule breaks, and read the package's time in the PR's run.
- A labelled corpus for the rip renders offline with no device: register a bank and
  voices, `freezeSounds`, `newSoundCache().preloadAll(nil)`, `buildKit`,
  `NewSequencer(bpm, kit)` with patterns placed in its slots, then `Generate` over
  whole bars (`rip_test.go`'s `renderLoop`). The music fades in over
  `MusicStartFadeIn`, so the first bar is quiet and a recall test skips it.
- `melody_gen` is no fixed pattern: every sequencer that plays it rewrites the
  registered lead in place from its second bar on, so an offline render's first
  bar replays the last render's last bar and a label read off the pattern after
  the render gives every bar the last one's notes. Stapes' corpus exported
  whole and in `part`s must match byte for byte; that comparison found it, 43
  loops in 9468.

## The snapshot

- `go run ./bank/snapshot [-wav] <dir> [document.toml ...]` writes down what a
  build plays, 13 s for the bank. `patterns.txt` and `sounds.txt` list what the
  engine holds once started: every pattern in the order registered, a line an
  event, and every sound and voice, a line a table, a field left unset left
  out. `index.txt` is a line a render, with its samples, its peak and first half
  second's RMS in dB and the sha256 of its samples: each sound
  (`RenderPreview`), each voice at C2 to C5 at 0.4 and 0.8 (`RenderNote`), each
  pattern alone for four bars in its role's slot, the free layer for one of no
  role, and one song of them all by name, four at a time for four bars, at 140
  in E Phrygian under seed 1. The line it prints is the counts and one sha256
  over the three files.
- So it holds a change that must not move what plays (`diff -r` of two), a
  render's or a voice's levels read off a diff of `index.txt`, and it says what
  a document plays: one music and one sound document are laid over the bank as
  vif lays a player's (`PatternTOML`, `SoundFS` and `SoundFile`), a name the
  bank has replaced in place, so a diff against the bank's own snapshot is what
  the document changes, note by note.
- It does not cover a sound's variants and a drum's velocity layers except as
  the patterns' hits play them; what only the live engine makes (the drawn
  arrangement, fills, reveals and crossfades, the mixer's levels, ducking and
  limiter), though the pools follow from the role, groups, tiers, motif and
  order `patterns.txt` lists; any tempo, meter, key or seed but the one; a
  document's `[[song]]`; MIDI and the rip.
- `melody_gen` is listed as registered, before any render: its own render
  rewrites its lead (Tests, above), and the song starts it on what that left.
- The listings are the same on amd64 and 386; 168 of the bank's 195 renders
  hash differently there, their levels the same to the hundredth of a dB.
  Compare snapshots taken on one platform.

## Voices and strings

- A string or modal voice ignores the ADSR's decay and sustain, yet the shipped
  ones keep `sustain = 1`: stapes' augmented corpus swaps a voice's wave a third of
  the time and keeps its genes, and an unset sustain reads as the gene's floor,
  so the swapped oscillator would fall silent within 5 ms.
- Levels re a note's peak and a mel distance are both blind to how loud a voice
  is: a bass fit went to a 50 Hz cutoff at Q 0.1, its notes 20 dB down, and
  scored best. Hold a fit's first half second RMS to main's (`RenderNote` over
  a few notes and velocities) before shipping it.
- A modal voice's two strings cancel a partial when the beat leaves them half a
  cycle apart where the slower overtakes the faster: with the piano's genes
  that took the fundamental out of B4 to E5 within half a second, and a refit
  moved the band (D5 to A5) rather than closing it. `excite` now holds the
  hand-over to a quarter cycle. Iowa's notes have such dips (C5 -29 dB at 0.5
  s), one note at a time, never a band.
- Read a decay after a high-pass: a delay line's burst holds a mean that rings
  at the loop's loss alone, so the old string's RMS read a 1.45 s T60 at C7
  where its fundamental fell 60 dB in 0.24 s.
- `PatternPlayer.TriggerNoteMIDI` stamps a voice's serial after `Trigger`, so a
  note seeded by its serial in `Trigger` reads the slot's previous note's:
  still a replay's same, and different for two notes of one pitch.
- Audacity's Pluck is Nyquist's `snd-pluck` (`tran/pluck.alg`, after M4C's
  Pluck.t). GitHub outside the session's repositories is refused; the
  SourceForge Nyquist tree and `sources.debian.org` serve it.
- A string's glide retuned every 16 samples buzzed (14 dB more energy above
  10 kHz than one retuned every sample), and its all-pass's state carried over
  a whole-sample step of the line rang near Nyquist (a bend down an octave read
  15 dB brighter than the unbent note). The line retunes every sample and
  `rejoin` rebuilds its filters' state at each step, which reads the same as
  rebuilding it every sample. Read a glide's artifacts as each 5 ms frame's third
  difference over its energy, beside the unbent note's.
- **A voice-like trait that is not a gene rides beside the genes, not in them.**
  Vibrato and a vowel's formants went on `VoiceDef` as plain fields, kept by
  the augmentation after `VoiceFromGenes`, rather than as new genes: a gene
  changes the model's voice head and every shipped weight file's shape, and the
  fit would search a space the recording rarely needs. Add a gene when a corpus
  with the trait has trained and the fit wants it, not before.

## Recordings to measure against

- Real notes to measure a voice against reach a cloud container: the University
  of Iowa's MIS samples (`theremin.music.uiowa.edu`, free for any use) hold one
  piano note a file at pp, mf and ff, and runs of guitar and pizzicato double
  bass; `ffmpeg -ac 1 -c:a pcm_f32le` makes the WAV `ReadWAV` takes. Their mf
  piano falls 15 to 25 dB in a note's first second from C3 up.
- Iowa's pizzicato double bass comes as runs, one string a file, edited to
  digital silence between notes: cut a note at an onset whose peak is within
  22 dB of the run's (fainter ones are the string's buzz) and end it at its
  first 50 ms under an RMS of 1e-5, or its level reads -240 dB at 3 s.
- Recorded drum one-shots free for any use reach a cloud container: Freesound's
  sound pages serve an HQ MP3 preview without a login (`cdn.freesound.org/previews/
  .../<id>_<user>-hq.mp3`), each page naming its licence by its "Go to the full
  license text" link (filter searches with `f=license:"Creative Commons 0"`); the
  CC0 Sonor kick set holds one head at six pressures, and Iowa's MIS has a hihat
  at pp, mf and ff by tip and shoulder. Previews stop near 16 kHz: read every
  figure under it, the kit's too.
- GuitarSet's JAMS files (beats, downbeats and notes of solo guitar) reach a
  cloud container as mirdata's test resource, one file cut short:
  `cdn.jsdelivr.net/gh/mir-dataset-loaders/mirdata@master/tests/resources/mir_datasets/guitarset/annotation/03_BN3-119-G_solo.jams`.
  Its `pitch_contour` holds its data as arrays by field, `beat_position` as a
  list of observations, so a reader decodes `data` by namespace.
- A spectrum of a one-shot under a Hann window reads its attack at nothing: the
  window is near zero where the hit starts, so a click or a stick vanished from
  the band shares. Start the segment a millisecond before the onset and taper
  only its end. Weigh a stroke-to-stroke spread by each band's energy, or a short
  noise's quiet bands differ by 10 dB between two takes alike to the ear.

## MIDI

- A MIDI note-off names no note: `ReadMIDI` ends the earliest sounding note of
  its channel and pitch, so a note nested inside a longer one of its pitch reads
  back with the two ends exchanged. A rip's line never holds such a pair; a file
  from a sequencer can. `pip install --user mido` reads a written file for a
  check from outside the package (`mido.MidiFile(path).tracks`).
- A score's notes quantize on its ticks, not its seconds: the grid's `place`
  sizes a note by the mean step over every bar, so under a tempo change a
  quarter in seconds read as eleven steps where the file holds eight.
  `MIDIScore.Rip` converts seconds back to ticks (`ticks`) for the grid and the
  beats to seconds after it.
- The MIDI writers cut a note where its key sounds again in the same track
  (`WriteSongMIDI`'s `held`), since the reader pairs a note-off with the earliest
  note: a long pattern note under a short one of its pitch would read back with
  the lengths exchanged. Changing the reader to pair the latest instead would
  move every transcript read so far (stapes' `truth` and `corpus <dir> from`).
- `readMIDITrack` keeps a channel's bend per track, so the reader cannot hear two
  tracks that share a channel's bend, as a player does: a test of the writers'
  channels reads them from `MIDIScore.parts`.
- `ReadMIDI`'s hits are the kick, snare and hihat only (`midiDrum(key,
  InstrClap)`), the drum channel's other keys `Struck`; `MIDIScore.Song` reads
  the clap too (`InstrClap+1`). A clap in a test file is a hit for a song, not
  for a transcript. `firstBar` reads a signature that holds exactly one bar
  before the next as a pickup, so a 3/4 bar ahead of 6/8 gives `Meter` 6/8.

## The analysis

- The rip's spectrogram frames are centred, so every onset reads about two frames
  (23 ms) early in every band alike; the beat grid and the events share the bias,
  and the sixteenth quantization cancels it. Do not correct one without the other.
- The default drum rules were set from the band shares a scratch test printed for
  the rendered loop's hits (a kick's rise under 100 Hz was 0.09 to 0.19 of its
  rise, a hat's above 5 kHz 0.45, a snare's noise above 5 kHz 0.15 to 0.21):
  print them again before moving a threshold, and expect a bass note's attack to
  read as a kick.
- A long analysis window reads an onset early by about a quarter of the
  window: the bass's notes from the 4×2048 window started a step before the
  grid said, and lagging its rows made them start late instead. Multiply the
  long window's salience by the short window's (their geometric mean) so the
  short one keeps the timing and the long one the pitch; and give a pinned
  grid the two-frame lead the beat tracker already has, or a note on the beat
  lands on the step before.
- A bass that wobbles between two semitones leaks its octave into the melody
  register on the frames the cancellation picks the other bin: cancel the
  partials of the strongest bass pitch and of a neighbour within two semitones
  at seventy per cent of it, and the ghosts go while a clean render's melody
  keeps its score.
- A sustained pad changes the key the chroma votes for, and a note the key
  lacks snaps to its neighbour: a melody score that falls when a pad is added
  may be the key, not the pitch paths. Read the rip's root before blaming the
  tracker.
- A second voice in a register is found by masking the first path's notes with
  their partials (the octave, the twelfth, the double octave and the seventeenth
  and nineteenth, a semitone either side) before the path runs again; masking
  the fundamental alone finds the octave ghost. The gate is relative to the first
  path's mean note strength, so a clean render's second pass yields nothing.
- A recording need not sit at A440: the sample is forty cents flat, which put
  every pitch between two salience rows and read as semitone wobble (A#2 and
  A2 taking turns), chopped sustains and a wrong scale. Measure the tuning (a
  histogram of spectral peaks' offsets from the nearest semitone, what
  `tuning` does) before blaming the tracker; the front end reads it first now,
  and since a render is in tune, a bench of renders cannot see this failure.
- The first full bar is not where the music starts: the grid dropped the
  sample's opening phrase, two and a half seconds before its first bar line.
  A pickup bar is extrapolated when a note or a hit leads in by more than half
  a step; a render's silence before bar 0 opens none, so the corpus reads the
  same. `Downbeat` is negative then, `progression` reads no frame before zero,
  and `CompareRips` skips the pickup bar against a truth without one.
- The pitch paths start late against the onsets the beats sit on, a sixth of a
  step on the sample: the median phase of the notes is taken out before
  quantizing, which lifted the test loop's melody from 0.95 to 0.98 and a cut
  recording's from 0.93 to 0.98. Under that sharper grid the second pass's
  fragments cost the pad test's melody two hundredths against the solo pass,
  so that guard allows five.
- The first and second pitch paths swap roles on a held dyad: the louder note
  changes hands from frame to frame, so each line hears a trill where the
  music holds a chord, and two fitted voices play it as two instruments
  alternating. Assign concurrent notes to lines by pitch order (`uncross`).
- The sequencer's range is 80 to 200 bpm and the tempo decoder folds into it,
  so the origin recording, near 74 bpm, reads at 148 while the intro, sped up
  to 86, reads at 86: the same piece on two metrical levels, a sixteenth 101
  ms in one rip and 174 in the other. A `Grid` with a negative downbeat pins the
  tempo alone, so the two rips can be put on one level.
- `RipNote.Bright` on the test loop's lead through its 2.5 kHz low-pass reads
  0.97 on a sine, 0.73 on a square and 0.49 on a saw (an unfiltered saw's six
  partials give 0.41). A melody note at the bass's octave, twelfth or double
  octave is `RipUnmeasured`: the loop's root and fifth over the bass's leave a
  quarter of its melody-register notes so. A concurrent note counts on a frame
  while it sounds anywhere under the window (eight frames either side); counted
  only on its own frames, the edge frames read the fifth's fundamental through
  the bass's partial and gave it a value up to 20% low.
- A `Ripper` of zero parameters hangs: zero costs let the pitch path jump every
  frame and the tempo prior degenerate. `Samples` and `Hear` take the defaults
  for the zero value now; a scratch tool that forgot `DefaultRipParams` ran
  for ten minutes on a twelve-second clip.
- The beat tracker can lock onto six sixteenths: it reads three of thirty 4/4
  renders of the bank's patterns at two thirds of their tempo (165, 176 and 190
  as 110, 117 and 126). Their eighths fall on the tracked beat's thirds, so the
  meter's `thirds` check reads those beats as dotted quarters and the 4/4
  reading at three halves restores the tempo; rerun those renders after any
  change to how the thirds are weighed.
- A truth mapped into a recording through matched notes (`Agreement.At`) is
  off by 50-500 ms in a ritardando where matches are sparse: the bar lines of
  the clean Für Elise take graded 0.4 within a sixteenth where the rip's were
  right. Grade a bar decoder on the exact-clock renders (round19/timbre) or on
  a render whose bars it knows (`RenderBars` with a tempo per bar); the takes
  grade the meter and the label, not the lines.
- A tracker whose tempo is a state (an interval scored against the one before)
  does not follow a ritardando in sixteenth runs: every sixteenth is as good a
  beat as every other, so bridging costs no onset, and on dense guitar it
  wanders toward the note rate. The fixed-period tracker stays; a bar of one
  or two pulses more or fewer in the bar path absorbs what it inserts.
- A chroma-change cue read over a candidate's own bar favours shorter bars,
  since adjacent short spans always differ more: 3/4 scored over 4/4 on every
  strummed 4/4 comp until the span was four tracked beats for every reading.
  A bass-accent cue (a bass note on the line against the bar's other
  sixteenths) peaks at wrong phases on guitar solos and comps by 0.3-0.5, yet
  without it Für Elise's lines go (0.51 to 0.11 within half a sixteenth); it
  rides at half weight.
- The bar line on a pulse grid finer than the beat may sit on an off-beat
  pulse; the beat-class cue (how much a pulse's class, a tracked beat apart,
  stands out in onset over the pulses around it) keeps it on the beat without
  forbidding a slip.
- A bass accent counted over two bars as well as one hears 3/4 in a half-note
  bass in 4/4: its notes fall on every line six beats apart as on every line
  eight apart, with fewer pulses between to dilute them. `accent` counts one bar.
- The repetition cue (`repeats`) compares each sixteenth's chroma and onset at a
  reading's lags, not the transcript's events: event matches favour the shorter
  bar's nearer lags on music that never repeats (three through-composed 4/4
  renders went 3/4 at a 0.02 margin), and a bar's distance to its nearest other
  bar ties on a loop, whose bars in another meter repeat too, three bars on.
- `CompareRips` grades notes within the bar, so a rip read in the right meter
  but with its line a beat off loses its melody or bass, while a wrong meter
  whose line falls on the first beat keeps them. stapes' generated 3/4 beats
  (kick on one, snares on two and three) are heard as a kick on every beat and a
  snare on one, so a gain in their bars can come with a loss in their notes.
- A meter or grid change is held to 4/4 by hashing what it must not move, before
  and after: renders, the drawn music `Generate` plays with tiers and tempo
  moving, rips of the renders with their documents and MIDI bytes, and the
  project's recordings' rips. `%+v` of a struct holding pointers hashes
  addresses: hash `MarshalPatternFile`'s document instead. `Rip.Notes` is the
  transcript before the grid, so it hashes the same when only the bars move.

## The matched kit

- A drum's tone picks the quarter of its hits with the least power before
  them. A zero-phase `eq` rings ahead of a hit into a render's silence, so the
  same hits rendered plain and through an eq can pick different hits, whose
  variants differ by 10 dB in the bands the drum barely fills: compare the
  bands within 15 dB of the drum's strongest, as `TestToneReadsBackAnEQ` does.
- The analysis front end places a drum hit about three frames early, and a tone
  read there takes the hit's attack as the sound before it (a bank beat ripped
  back read +8..+13 dB curves); `hitStarts` moves each hit to where the sound
  rises, on the recording and on the kit's render alike, the balance's renders
  too, since a zero-phase eq moves the rise.
- `aWeight` is the analog curve through the bilinear transform: at 44.1 kHz it
  reads 10 kHz at -4.0 dB where the standard has -2.5 (-22 at 22.05 kHz), and
  prewarping its poles is unstable under a 24.4 kHz rate. A matched kit's
  balance reads both sides through it, so the bias cancels; elsewhere read
  levels near Nyquist with that in mind.

## Front ends and fidelity

- A corpus and a bench render one pattern a part (`RenderMusic`), so they never
  change a slot's pattern at a bar line; the cut a changed bar line once made to
  ringing notes (forty silent gaps in a ripped Für Elise) showed in songs alone.
  Count a song render's silent runs (5 ms frames under -80 dBFS, four or more,
  none touching an end) before blaming the model for a gap.

- The rip's front end is a seam: `analysis` and `ripmodel` both fill `RipFrames`,
  and the decoders read nothing else, so a front end is checked by the frames it
  makes and the rip they give, as `ripmodel`'s test does with an untrained model.
- The harmonic stack (`stacked`) interpolates in float64 where the trainer's
  `PitchNet.stack` does in float32, so a third of the floats differ in the last
  bit (up to 4.8e-7); through PitchNet at the trainer's shape the heads differ
  from PyTorch's by up to 4.8e-6 on outputs up to 11, rounding. Dump
  `model.stack(x)` beside the outputs to tell the stack from the model.
- The trainer's `ripmodel.features` and `features` agree bit for bit on the
  PCEN plane, but on a piano recording 63 of the dB plane's 534,520 floats
  differed in the last bits (up to 1.4e-14, all near zero): numpy's `mean` sums
  pairwise, Go in order. Compare the planes with that in mind, not the whole
  frame for equality.
- A head trained with a positive weight w (`bce(..., pos_weight)` in
  `train.py`) scores a posterior p as wp/(wp + 1 - p): the strike head's
  (w 20) half-sure is 0.95 and the pitch head's (w 12) 0.92. Sweep a
  decoder's threshold on that scale against the truths; 0.5 floods.
- Under a pedal the model's pitch output sits at its ceiling for every ringing
  note, and it rises a few frames before a strike: a "was it sounding" test on
  the frames before a strike reads nearly every note as ringing. Ask the
  decoder's own state (a note still open) instead, and print a window's strikes
  and salience per pitch from a scratch test before moving a strike rule.
- The rip-back cannot judge the rip's own errors: a note misheard in the
  recording is misheard the same way in the render, so a search on it polishes
  the wrong transcript. `Fidelity` compares the render's frames with the
  recording's through one front end; a render a semitone sharp scores 0.56 in
  pitch against 1.0 for itself, with its onsets still at 1.0.
- Silence past a recording's ends is the right guess for the model: on 110
  GuitarSet test cuts, half 150 ms into a held strum and half 5 ms before one,
  mirroring a second of the recording past each end found one note more at a
  mid-sound start, four fewer at an attack, and added sixteen extras at a
  mid-sound end. A gate on the high band's level misses a soft attack (a bossa
  thumb), so it cannot choose the edges to mirror either.
- A render at A440 beside a recording forty cents flat is heard as the same
  notes sharpened, "a stretched string", before any note is judged: the ear
  reads a tuning offset as timbre. Render, play and fit at the recording's
  tuning (`SongDef.Tuning`, `SetTuning`, `RenderNoteAt`); the rip reads it.

## The accounting

- `lines` reports a bass note twice when the strike's short note and the bass
  path's long note of one pitch share a start, as the path note overlaps the
  struck one by under a quarter: 23 of the clean Für Elise take's 41 low
  "same pitch again" extras were such pairs, which `accDedup` merges.
- A truth mapped onto a take through `Agreement.At` sits about 100 ms off in
  places (the repeated A2 bass of the B section at 105 s on the clean take): a
  note counted missing there is often reported 100-110 ms away, so the bass
  F1s of the takes undercount, and a change there is read on the renders.
- Of the 240 notes the clean take misses, 120 have a strike over 0.7 at the
  pitch within 100 ms: the decoder's rules after the bar drop them, a ringing
  pitch struck under `StrikeAgain` above all (the B section's repeated A2s at
  0.7 to 0.9), then `sounds`, the partial rule and `ownPitch`. Candidates
  judged together with the reported notes let the strike head's partials of a
  bass note (an octave, a twelfth and two octaves up, at the same start) split
  its sound and the real note dropped; judged one by one against the notes
  kept, they are precise. A window twice the rip's for notes under E3 changed
  no number.
- `RipFrames.register` sorts the whole recording's salience peaks for one
  reference: called per note it took two thirds of the stage's time.
- The scores were calibrated on a dump of each note's `accInput` beside its
  odds, written by a scratch test in the package fed the notes model's saved
  heads (`tmp/explain/audio/account_dump_test.go`, 318 floats a frame as
  `tmp/round30/fa.py` reads them) over the clean and Pixel takes and the
  acoustic-keyboard and distorted-guitar renders of Für Elise, then
  `tmp/explain/py/cal.py`, a logistic fit by Newton's method with a unit L2
  penalty, reported notes and candidates apart; the weights are `accReported`
  and `accCandidate`.
- The scorer's truth map snaps each truth note to the reported note it
  matched, so where the decoder reports a partial shadow at a real note's pitch
  (the E3 at an E2's own frame in the bass figure at 8.3 s of the clean take)
  the map puts the real note at the shadow's time and squeezes its neighbours
  by 100 ms; the restrike labels under it were wrong. A map by a local line
  over the matched notes' offsets (`tmp/explain/py/smoothmap.py`, ±0.7 s)
  places the bass figures within 40 ms of the recording; the Pixel take is the
  clean take's own timing and the renders are linear in the MIDI's.
- A soft restrike on a loud ring moves the fundamental by the phase it adds
  in: +3 to +8 dB in phase, -6 to -10 dB against it, a few dB in quadrature. A
  ring misread as a strike moves it under 2 dB, with a wobble of .2 to .5 of
  its amplitude from the unison strings' beating over the window's gap. So the
  memory reads the step between the lines fitted either side of the attack in
  the sound before, at the free partials, not a coherence or a residual's
  share (AUC .5 to .6); the fundamental alone separates soft restrikes at .65.
- Sheet 12's miss at 8.48 s of the clean take is a soft E3 under its octave's
  ringing second partial: the fit saw a 2 dB rise and an unexplained
  fundamental of .19, the memory a step of .83 in the prototype. In the stage
  a partial another pitch's partial sits on is left out of the memory, so the
  note reads as all new; predicting such a partial through when its note rang
  from before reads the E3's step (.72) but the candidate score still refuses
  it at .16, and the memory's wobble under other pitches' rings at the higher
  partials costs the restrike kind, so the exclusion stays.

## Decoders

- **The wide table never sees a bar line.** Its F1 matches notes through a
  fitted time map, so a bar path that moves every line by a beat scores the
  same; read the rip's bar lines against a truth that has them (GuitarSet's
  `beat_position` annotations, a score's bars) before calling a bar change
  neutral. A cue that needs context before the line, such as the chroma
  change over four beats, has none at a recording that starts at once; the
  time before the recording is silence and counts as such, not as missing.
- **Fix the cut, not the labels.** A stream assignment over the notes two
  passes had already cut, by pitch, timbre and level, made the two recordings
  of one piece agree less, not more: the first pass had cut the louder pitch
  of each frame into notes that belonged to neither line, and no relabelling
  mends a note cut across two lines. Decoding the two lines together, where
  the cut is made, did. Try the change at the stage the error is made.
- **A joint decoder is cheap when its transition cost is separable.** Pairs
  of forty-five states are two thousand; a step over them is two sweeps of a
  one-line distance transform, since the jump cost is linear in semitones, not
  a product over pairs, and backpointers kept a block at a time from
  checkpoints of the scores hold a twenty-minute recording in a few megabytes.
- **Weigh a cue on a scale you chose.** The first parting of the two lines
  followed loudness alone, whatever the other weights said, because a note's
  strength was the raw salience, a hundred times the other terms; against the
  register's reference it took its intended share. Check the magnitude of each
  term on real data before trusting a weighted sum.
- **The louder line is not the lead.** On the origin recording the lead the
  user hears is the softer line, short notes at a steady cadence, under a
  counter line of held notes that the decoder followed as its first line and
  that the harmonic profile could not tell apart from the lead. Which line is
  the melody is a listener's call with a default, not a rule the signal
  settles; make it a word the listener can give.
- **A register boundary cuts a line that crosses it.** Transposed three
  semitones, the origin's bass D#3 became the intro's F#3, above the bass
  register's top, and surfaced as a low note of the melody with no bass note
  under it, which the stream parting then had to place. Let a decoder reach
  past its register's rows and settle the overlap by a rule.
- **Measure the lines, not the register.** The two-witness script matched any
  note of the same pitch within a window, whatever line held it, so it could
  not see the lines change hands; the agreement per line was an illusion.
  `MatchNotes` matches within a line and reports the register as one line
  beside it, so the two numbers show the parting's own error.
- **A rule that fits a synthetic case can be refuted by one recording.** A
  note cut where its salience rose again by a third after falling off its
  peak heard thirty-two of thirty-two repeated piano strikes in a test and,
  on the recording, cut eleven of the lead's twenty-five plucked notes in two
  and the bass's held notes too, since those strings beat. The rhythm the
  user tapped out showed it in one run; the bench and the suite had not.
  Measure a decoder change against the recording's own truth before it
  ships, and keep the test only when the rule stays.
- **An offline render changes tempo and meter quantized.** It sets them
  so (`SetBPM(bpm, true)`, `SetMeter(m, true)`): set at once at the
  bar line, the new step length is held against the sequencer's count of the
  old bar's last step, and a slower bar starts late by the difference, a
  delay a faster bar may never absorb. `TestARenderedBarStartsOnItsHead`.
- **Replay a fitted rule on its dump once implemented.** A decision fitted
  on a candidate dump (`tmp/meter/fit.py`) is replayed on that dump before
  any set is regraded: the first
  implementation let 6/8 challenge 4/4 where the fit had only consulted it as
  a name, and lost 22 of 40 GuitarSet labels the fit had kept.

## A live rip

- **A statistic over the whole recording is a window's in a live rip unless it
  is carried.** The register reference (the ninetieth percentile of a
  register's loudest row a frame), each drum's loudest onset and the onset
  bands' loudest are read over every frame offline; over the stream's window
  the reference moved enough to flip a marginal note in bar 8 of the 140 bpm
  loop. `RipStream.count` carries each over every frame heard: check a new
  decoder statistic the same way, the stream against `RipSamples` bar by bar.
- **What an ear reads over "what has arrived" makes the rip depend on the
  blocks.** The analysis's tuning, read over the samples in by the first block
  past eight seconds, moved with the block size; it reads exactly the first
  eight seconds now, and the model's ear hears on a fixed frame grid against the
  loudest sample up to it. `TestRipStreamBarsAreTheSameWhateverTheBlocks` and
  `ripmodel`'s ear test compare the rips and the frames in 50 ms and in random
  blocks; a new ear goes through both.
- **A median taken as the notes come rounds at an edge the whole's does not.**
  The loop at 175 bpm reads at 88, where the notes' lag sat at 3.50 frames: in
  five early bars the stream placed one note a step later than `RipSamples`.
  Before calling such a bar a stream bug, compare the shift the stream froze it
  with (`RipStream.lag` × `stepFrames`) with the offline grid's.
