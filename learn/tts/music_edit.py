"""Turn raw generated music (scripts/gen-music.mjs) into the files the videos load.

  uv run python music_edit.py bed   <in.mp3> <out.mp3> [--min 90] [--max 120]
  uv run python music_edit.py sting <in.mp3> <out.mp3> [--from 6.4] [--len 3.9]

bed: finds the beat, cuts a whole number of 16-beat phrases (the cut whose end sounds most like
its start wins), and cross-fades the last second into the bar before the start, so the end of
the file runs into its beginning without a jump. 10 ms fades at both ends, -16 LUFS, 48 kHz
stereo mp3 (the level of the film's music).
sting: cuts --len seconds from --from (default: the last --len seconds, i.e. the final chord),
snapped to the nearest note onset, 15 ms fade in, 350 ms fade out, -16 LUFS.
"""
import argparse
import subprocess
import tempfile

import librosa
import numpy as np
import soundfile as sf

SR = 48000


def write_mp3(audio: np.ndarray, out: str) -> None:
    """audio is (channels, samples). Loudness-normalised and encoded with ffmpeg."""
    with tempfile.NamedTemporaryFile(suffix=".wav") as tmp:
        sf.write(tmp.name, audio.T, SR, subtype="PCM_24")
        subprocess.run(
            ["ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", tmp.name,
             "-af", "loudnorm=I=-16:TP=-1.5:LRA=11:linear=true", "-ar", str(SR), "-b:a", "192k", out],
            check=True,
        )


def fade(audio: np.ndarray, fade_in: float, fade_out: float) -> np.ndarray:
    a, b = int(fade_in * SR), int(fade_out * SR)
    audio = audio.copy()
    if a:
        audio[:, :a] *= np.linspace(0, 1, a)
    if b:
        audio[:, -b:] *= np.linspace(1, 0, b)
    return audio


def bed(src: str, out: str, lo: float, hi: float) -> None:
    y, _ = librosa.load(src, sr=SR, mono=False)
    mono = librosa.to_mono(y)
    hop = 512
    tempo, beats = librosa.beat.beat_track(y=mono, sr=SR, hop_length=hop, units="samples")
    chroma = librosa.feature.chroma_cqt(y=mono, sr=SR, hop_length=hop)
    frame = lambda sample: int(sample // hop)
    look = int(2.0 * SR / hop)   # compare two seconds after each cut point

    def alike(a: int, b: int) -> float:
        ca, cb = chroma[:, frame(a):frame(a) + look], chroma[:, frame(b):frame(b) + look]
        n = min(ca.shape[1], cb.shape[1])
        if n < look // 2:
            return -1.0
        ca, cb = ca[:, :n].ravel(), cb[:, :n].ravel()
        return float(np.dot(ca, cb) / (np.linalg.norm(ca) * np.linalg.norm(cb) + 1e-9))

    xf = int(1.0 * SR)
    best = None
    for i in range(8, 40):                       # start after the first bars (room for the cross-fade)
        for j in range(i + 16, len(beats), 16):  # whole 16-beat phrases
            length = (beats[j] - beats[i]) / SR
            if not lo <= length <= hi or beats[i] < xf:
                continue
            score = alike(beats[i], beats[j])
            if best is None or score > best[0]:
                best = (score, i, j)
    if best is None:
        raise SystemExit("No loop of the wanted length found")
    score, i, j = best
    start, end = int(beats[i]), int(beats[j])
    body = y[:, start:end].copy()
    ramp = np.sqrt(np.linspace(0, 1, xf))        # equal-power
    body[:, -xf:] = body[:, -xf:] * ramp[::-1] + y[:, start - xf:start] * ramp
    write_mp3(fade(body, 0.01, 0.01), out)
    print(f"{out}: {float(np.atleast_1d(tempo)[0]):.1f} bpm, beats {i}–{j} ({start / SR:.2f}–{end / SR:.2f} s of the source), "
          f"{(end - start) / SR:.2f} s, start/end likeness {score:.3f}")


def sting(src: str, out: str, start: float | None, length: float) -> None:
    y, _ = librosa.load(src, sr=SR, mono=False)
    mono = librosa.to_mono(y)
    total = mono.shape[-1] / SR
    want = total - length if start is None else start
    onsets = librosa.onset.onset_detect(y=mono, sr=SR, units="time")
    begin = min(onsets, key=lambda t: abs(t - want)) if len(onsets) else want
    a, b = int(max(begin - 0.01, 0) * SR), int(min(begin + length, total) * SR)
    write_mp3(fade(y[:, a:b], 0.015, 0.35), out)
    print(f"{out}: {a / SR:.2f}–{b / SR:.2f} s of the source, {(b - a) / SR:.2f} s")


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("kind", choices=["bed", "sting"])
    ap.add_argument("src")
    ap.add_argument("out")
    ap.add_argument("--min", type=float, default=90)
    ap.add_argument("--max", type=float, default=120)
    ap.add_argument("--from", dest="start", type=float, default=None)
    ap.add_argument("--len", dest="length", type=float, default=3.9)
    args = ap.parse_args()
    if args.kind == "bed":
        bed(args.src, args.out, args.min, args.max)
    else:
        sting(args.src, args.out, args.start, args.length)
