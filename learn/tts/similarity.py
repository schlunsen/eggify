"""Speaker similarity between the film's narrator and generated narration (ECAPA embeddings).

  uv run --with speechbrain python similarity.py "film=../../promo/public/audio/vo-*.wav" \
      "learn=../public/episodes/quick-start/vo/*.wav"

Prints cosine similarity within and between the groups and each file's similarity to the film's
average voice. For scale: the film's own lines score about 0.80 with each other (0.66–0.90), a
different speaker about 0.06. The first run downloads speechbrain/spkrec-ecapa-voxceleb (~85 MB)
into out/ecapa.
"""
import sys, glob, itertools, numpy as np, torch, torchaudio, soundfile as sf
from speechbrain.inference.speaker import EncoderClassifier
enc = EncoderClassifier.from_hparams(source="speechbrain/spkrec-ecapa-voxceleb", savedir="out/ecapa", run_opts={"device":"cpu"})
def emb(f):
    y, sr = sf.read(f, dtype='float32'); 
    if y.ndim>1: y=y.mean(1)
    y = torchaudio.functional.resample(torch.from_numpy(y), sr, 16000)
    e = enc.encode_batch(y[None]).squeeze().numpy(); return e/np.linalg.norm(e)
groups = {a.split('=')[0]: sorted(glob.glob(a.split('=')[1])) for a in sys.argv[1:]}
E = {g:{f:emb(f) for f in fs} for g,fs in groups.items()}
def stats(v): return f"mean {np.mean(v):.3f} min {np.min(v):.3f} max {np.max(v):.3f} n {len(v)}"
names=list(E)
for g in names:
    v=[float(a@b) for a,b in itertools.combinations(E[g].values(),2)]
    if v: print(f"within {g}: {stats(v)}")
for g,h in itertools.combinations(names,2):
    v=[float(a@b) for a in E[g].values() for b in E[h].values()]
    print(f"{g} vs {h}: {stats(v)}")
if 'film' in E:
    c=np.mean(list(E['film'].values()),0); c/=np.linalg.norm(c)
    for g in names:
        for f,e in E[g].items(): print(f"  {g} {f.split('/')[-1]}: cos to film centroid {float(e@c):.3f}")
