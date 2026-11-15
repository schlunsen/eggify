"""Narration for an Eggify learning video, in the promo film's voice.

The film's narrator is an OmniVoice designed voice; here it is cloned from two of the film's
lines (ref/ref.wav, transcript in ref/ref.txt). For every line of <episode-dir>/episode.json
this writes learn/public/episodes/<slug>/vo/<lineId>.wav (48 kHz mono, silence trimmed, 80 ms
pad), speaking the line's `say` when present, else its `text`.

usage (always through the lock, from learn/tts):
  ../scripts/with-lock.sh tts uv run python gen.py ../episodes/01-quick-start
      [--only id,id] [--takes 3] [--speed 0.92] [--steps 32] [--force] [--seed 1]
  ../scripts/with-lock.sh tts uv run python gen.py ../episodes/01-quick-start --pick p1=1
      (after listening: use kept take 1 for line p1; nothing is generated)

Each line is generated --takes times and the take is chosen by checks, not by ear:
  1. Whisper (openai/whisper-large-v3-turbo, local) transcribes the take; a take whose words
     drift from the script (word error rate above WER_MAX) is rejected.
  2. Among the rest: no long pause inside the line, a natural rate (3.9–5.0 syllables/s, about 2.3–3.0 words/s) and a
     pitch close to the reference voice win.
If every take is rejected, up to two more rounds are generated; the best one is then kept and
the line is marked "check" in the report. Existing files are skipped unless --force or --only.
The report (per line: duration, words/s, longest pause, median pitch, transcript, all takes)
is printed and saved to out/<slug>/report.json; every take is kept in out/<slug>/takes/.
"""
import argparse
import difflib
import json
import os
import re
import sys
from pathlib import Path

import librosa
import numpy as np
import soundfile as sf
import torch
import torchaudio

HERE = Path(__file__).resolve().parent
LEARN = HERE.parent
REF_WAV = HERE / "ref" / "ref.wav"
REF_TXT = HERE / "ref" / "ref.txt"

MODEL_SR = 24000
OUT_SR = 48000
PAD_S = 0.08          # silence kept before and after the speech
TRIM_DB = 42          # below the peak = silence, for trimming and pause detection
WER_MAX = 0.08        # a take above this is rejected
RATE_MIN, RATE_MAX = 3.9, 5.0   # natural narration rate in syllables per second (≈ 2.3–3.0 words/s
                                # for this vocabulary; syllables do not punish "x y z" or long words)
PAUSE_MAX = 0.75      # longest pause inside a line before it costs points, seconds
F0_TOL = 0.12         # pitch may differ this much from the reference before it costs points
TARGET_RMS_DB = -23.0  # speech level of the written files (the film's lines sit about here)

NUMBER_WORDS = {
    "0": "zero", "1": "one", "2": "two", "3": "three", "4": "four", "5": "five", "6": "six",
    "7": "seven", "8": "eight", "9": "nine", "10": "ten", "90": "ninety",
}


def words(text: str) -> list[str]:
    """Lower-case words for comparing a script line with a transcript."""
    text = text.lower().replace("’", "'")
    text = re.sub(r"(?<=\w)\.(?=\w)", " dot ", text)      # eggify.xyz -> eggify dot xyz
    text = re.sub(r"[^a-z0-9' ]+", " ", text)
    out = []
    for w in text.split():
        w = w.strip("'")
        if w:
            out.append(NUMBER_WORDS.get(w, w))
    return out


def syllables(text: str) -> int:
    """Rough syllable count of what is spoken (vowel groups; spelled letters count one each)."""
    total = 0
    for w in words(text):
        if w.isdigit():
            total += 2
        elif len(w) == 1 and w not in ("a", "i"):
            total += 3 if w == "w" else 1
        elif w in ("api", "ai", "xyz", "ui", "cli", "ios"):
            total += len(w)
        else:
            groups = len(re.findall(r"[aeiouy]+", w))
            if w.endswith("e") and not w.endswith(("le", "ee")) and groups > 1:
                groups -= 1
            total += max(groups, 1)
    return total


def squash(ws: list[str]) -> str:
    return "".join(ws)


def word_error_rate(script: str, heard: str) -> float:
    """Word error rate that forgives spelling: words that sound alike (eggify / egify, codex /
    codecs) count as equal, and so does a different split of the same letters (x y z / xyz)."""
    a, b = words(script), words(heard)
    if not a:
        return 0.0
    if squash(a) == squash(b):
        return 0.0
    same = lambda x, y: x == y or difflib.SequenceMatcher(None, x, y).ratio() >= 0.72
    # Levenshtein distance over words with the forgiving comparison.
    prev = list(range(len(b) + 1))
    for i, x in enumerate(a, 1):
        cur = [i]
        for j, y in enumerate(b, 1):
            cur.append(min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (0 if same(x, y) else 1)))
        prev = cur
    # Letters joined or split differently (open router / openrouter) are not errors either.
    letters = 1 - difflib.SequenceMatcher(None, squash(a), squash(b)).ratio()
    return min(prev[-1] / len(a), letters * 3)


def trim(audio: np.ndarray, sr: int) -> np.ndarray:
    """Cut leading and trailing silence, keep PAD_S of true silence with a short fade."""
    _, (start, end) = librosa.effects.trim(audio, top_db=TRIM_DB, frame_length=1024, hop_length=256)
    speech = audio[start:end].copy()
    fade = min(int(0.008 * sr), len(speech) // 2)
    if fade:
        ramp = np.linspace(0.0, 1.0, fade, dtype=speech.dtype)
        speech[:fade] *= ramp
        speech[-fade:] *= ramp[::-1]
    pad = np.zeros(int(PAD_S * sr), dtype=speech.dtype)
    return np.concatenate([pad, speech, pad])


def longest_pause(audio: np.ndarray, sr: int) -> float:
    """Longest silent stretch between the first and last sound, in seconds."""
    parts = librosa.effects.split(audio, top_db=TRIM_DB, frame_length=1024, hop_length=256)
    gaps = [(b[0] - a[1]) / sr for a, b in zip(parts[:-1], parts[1:])]
    return max(gaps, default=0.0)


def median_f0(audio: np.ndarray, sr: int) -> float:
    """Median pitch of the voiced frames, in Hz (0 when nothing is voiced)."""
    y = librosa.resample(audio, orig_sr=sr, target_sr=16000) if sr != 16000 else audio
    f0, voiced, _ = librosa.pyin(y, fmin=55, fmax=320, sr=16000, frame_length=1024)
    f0 = f0[voiced & ~np.isnan(f0)]
    return float(np.median(f0)) if len(f0) else 0.0


def level(audio: np.ndarray) -> np.ndarray:
    """Bring the speech to TARGET_RMS_DB (measured over the sounding parts), peaks under -1 dBFS."""
    parts = librosa.effects.split(audio, top_db=TRIM_DB, frame_length=1024, hop_length=256)
    voiced = np.concatenate([audio[a:b] for a, b in parts]) if len(parts) else audio
    rms = float(np.sqrt(np.mean(voiced**2))) or 1e-9
    gain = 10 ** (TARGET_RMS_DB / 20) / rms
    peak = float(np.max(np.abs(audio))) or 1e-9
    gain = min(gain, 10 ** (-1 / 20) / peak)
    return audio * gain


def measure(audio: np.ndarray, sr: int, script_text: str, heard: str, ref_f0: float) -> dict:
    """Everything the choice is based on, for one trimmed take."""
    speech_s = len(audio) / sr - 2 * PAD_S
    rate = len(script_text.split()) / max(speech_s, 0.1)
    syl = syllables(script_text) / max(speech_s, 0.1)
    pause = longest_pause(audio, sr)
    f0 = median_f0(audio, sr)
    wer = word_error_rate(script_text, heard)
    f0_off = abs(f0 / ref_f0 - 1) if ref_f0 and f0 else 0.0
    # Lower is better. A rejected transcript outweighs everything else.
    score = (
        (10 + wer * 10 if wer > WER_MAX else wer * 10)
        + max(0.0, pause - PAUSE_MAX) * 4
        + max(0.0, RATE_MIN - syl, syl - RATE_MAX) * 2
        + max(0.0, f0_off - F0_TOL) * 10
        # Tie-breakers between takes that pass everything: shorter pauses, a rate near the middle.
        + pause * 0.1 + abs(syl - (RATE_MIN + RATE_MAX) / 2) * 0.05
    )
    return {
        "dur": round(len(audio) / sr, 3), "words_per_s": round(rate, 2), "syl_per_s": round(syl, 2),
        "longest_pause": round(pause, 2),
        "f0_hz": round(f0, 1), "wer": round(wer, 3), "ok": wer <= WER_MAX, "score": round(score, 3),
        "heard": heard,
    }


def write_line(path: Path, audio: np.ndarray) -> None:
    """A trimmed 24 kHz take -> the levelled 48 kHz mono file the video loads."""
    out = torchaudio.functional.resample(torch.from_numpy(level(audio)), MODEL_SR, OUT_SR).numpy()
    sf.write(path, out, OUT_SR, subtype="PCM_16")


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("episode_dir")
    ap.add_argument("--only", default="", help="comma-separated line ids (implies --force for them)")
    ap.add_argument("--takes", type=int, default=3)
    ap.add_argument("--speed", type=float, default=0.92)
    ap.add_argument("--steps", type=int, default=32)
    ap.add_argument("--seed", type=int, default=1)
    ap.add_argument("--force", action="store_true")
    ap.add_argument("--pick", default="", help="use a kept take instead of the chosen one, e.g. p1=2,w1=0 (no generation)")
    args = ap.parse_args()

    episode = json.load(open(Path(args.episode_dir) / "episode.json"))
    slug = episode["slug"]
    lines = [line for scene in episode["scenes"] for line in scene.get("lines", [])]
    only = {s for s in args.only.split(",") if s}
    unknown = only - {line["id"] for line in lines}
    if unknown:
        sys.exit(f"No such line id: {', '.join(sorted(unknown))}")

    vo_dir = LEARN / "public" / "episodes" / slug / "vo"
    work = HERE / "out" / slug
    takes_dir = work / "takes"
    vo_dir.mkdir(parents=True, exist_ok=True)
    takes_dir.mkdir(parents=True, exist_ok=True)
    report_path = work / "report.json"
    report = json.load(open(report_path)) if report_path.exists() else {}

    if args.pick:
        for item in args.pick.split(","):
            line_id, n = item.split("=")
            entry = report[line_id]
            chosen = next(t for t in entry["takes"] if t["take"] == int(n))
            audio, _ = sf.read(takes_dir / f"{line_id}-{n}.wav", dtype="float32")
            write_line(vo_dir / f"{line_id}.wav", audio)
            report[line_id] = {**entry, **chosen, "picked_by_hand": True}
        json.dump(report, open(report_path, "w"), indent=2, ensure_ascii=False)
        print_report(lines, report)
        return

    todo = [
        line for line in lines
        if (line["id"] in only) or (not only and (args.force or not (vo_dir / f"{line['id']}.wav").exists()))
    ]
    if not todo:
        print("Nothing to do: every line has a file (use --force or --only).")
        print_report(lines, report)
        return

    from omnivoice import OmniVoice

    device = "mps" if torch.backends.mps.is_available() else "cpu"
    model = OmniVoice.from_pretrained(
        "k2-fsa/OmniVoice", device_map=device, dtype=torch.float16 if device == "mps" else torch.float32
    )
    model.load_asr_model()  # Whisper, used only to check the takes
    prompt = model.create_voice_clone_prompt(ref_audio=str(REF_WAV), ref_text=REF_TXT.read_text().strip())
    ref_audio, _ = librosa.load(REF_WAV, sr=MODEL_SR, mono=True)
    ref_f0 = median_f0(ref_audio, MODEL_SR)
    print(f"reference voice: median pitch {ref_f0:.1f} Hz", flush=True)

    for line in todo:
        spoken = line.get("say") or line["text"]
        takes = []
        for round_ in range(3):
            for k in range(args.takes):
                n = len(takes)
                seed = args.seed * 1000 + n
                torch.manual_seed(seed)
                raw = model.generate(
                    text=spoken, voice_clone_prompt=prompt, language="en", speed=args.speed, num_step=args.steps
                )[0]
                audio = trim(np.asarray(raw, dtype=np.float32).squeeze(), MODEL_SR)
                heard = model.transcribe((audio, MODEL_SR))
                m = measure(audio, MODEL_SR, spoken, heard, ref_f0)
                m["take"], m["seed"] = n, seed
                sf.write(takes_dir / f"{line['id']}-{n}.wav", audio, MODEL_SR)
                takes.append((m, audio))
                print(f"  {line['id']} take {n}: {m['dur']}s {m['words_per_s']} w/s {m['syl_per_s']} syl/s pause {m['longest_pause']}s "
                      f"f0 {m['f0_hz']} wer {m['wer']} score {m['score']} | {heard}", flush=True)
            if any(m["ok"] for m, _ in takes):
                break
        best, audio = min(takes, key=lambda t: t[0]["score"])
        write_line(vo_dir / f"{line['id']}.wav", audio)
        report[line["id"]] = {
            **best, "text": line["text"], "say": line.get("say"), "speed": args.speed, "steps": args.steps,
            "ref_f0_hz": round(ref_f0, 1), "takes": [m for m, _ in takes],
        }
        json.dump(report, open(report_path, "w"), indent=2, ensure_ascii=False)
        print(f"{line['id']}: take {best['take']} of {len(takes)} {'ok' if best['ok'] else 'CHECK'}", flush=True)

    print_report(lines, report)


def print_report(lines: list[dict], report: dict) -> None:
    print("\nline  dur    w/s   syl/s pause  f0     wer    check  heard")
    for line in lines:
        r = report.get(line["id"])
        if not r:
            print(f"{line['id']:<5} (no report)")
            continue
        print(f"{line['id']:<5} {r['dur']:<6} {r['words_per_s']:<5} {r.get('syl_per_s', ''):<5} {r['longest_pause']:<6} {r['f0_hz']:<6} "
              f"{r['wer']:<6} {'ok' if r['ok'] else 'CHECK':<6} {r['heard']}")
    total = sum(report[line["id"]]["dur"] for line in lines if line["id"] in report)
    print(f"total narration: {total:.1f} s")


if __name__ == "__main__":
    os.environ.setdefault("TOKENIZERS_PARALLELISM", "false")
    main()
