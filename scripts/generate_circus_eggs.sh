#!/usr/bin/env bash
# Generates the subagent egg characters for the Zen Circus stages (the
# subagents view and the session Zen walkers): six Victorian-engraved eggs in
# the style of the main egg gentleman (public/zen/circus/egg.webp), one per
# CircusKind in app/utils/agents/circusCharacters.ts.
#
#   ATLASCLOUD_API_KEY=... scripts/generate_circus_eggs.sh [out_dir] [kind ...]
#
# Then process the PNGs with scripts/process_circus_eggs.py (background
# removal, cream scissor-cut border, shadow, webp). Generations are not
# deterministic; the committed files are one pick (see docs/MEDIA_PROVENANCE.md).
set -euo pipefail
: "${ATLASCLOUD_API_KEY:?set ATLASCLOUD_API_KEY}"
OUT=${1:-./circus-eggs}
shift || true
mkdir -p "$OUT"
API=https://api.atlascloud.ai/api/v1/model
MODEL=${MODEL:-bytedance/seedream-v5.0-pro/text-to-image}

STYLE="A full-length anthropomorphic egg character: a large smooth white egg as the whole head and body, a friendly face with small dark eyes, thin cream-sleeved arms with white gloves, thin legs and small black buttoned shoes, standing, full body visible from head to toe, three-quarter view. Hand-coloured 19th century steel engraving: fine crosshatching and stipple shading on the egg shell, muted hand-tinted colours (dusty rose, ochre, verdigris, cream, sage), Victorian illustration, slightly absurd and dignified. Single isolated character on a plain flat white background, no ground, no cast shadow, no text, no frame, no border."

subject() { # kind
  case $1 in
    gent) echo "The egg is a pompous City gent: a black bowler hat, a dark pinstripe frock coat over a striped waistcoat, a rolled black umbrella under one arm and a folded newspaper in the other hand." ;;
    knight) echo "The egg is a slightly rusty knight: a dented steel helmet with a raised visor and a red plume perched on top of the egg, a chainmail collar and a red tabard with a gold cross, holding a wooden sword upright with great seriousness." ;;
    hanky) echo "The egg wears a white handkerchief knotted at its four corners on top of its head, small round wire spectacles, a toothbrush moustache, a knitted tank top over a white shirt with braces, trousers rolled up to the knee and black wellington boots." ;;
    parrot) echo "The egg is an intrepid explorer: a cream pith helmet, a khaki safari jacket with many pockets, a brass telescope in one hand, and a large blue parrot with beautiful plumage perched proudly on its shoulder." ;;
    egg) echo "The egg is a learned professor: a black mortarboard cap with a tassel, round half-moon spectacles, a black academic gown over a mustard waistcoat, holding a thick open leather-bound book and raising one gloved finger as if making a point." ;;
    lady) echo "The egg is a formidable Victorian lady: an enormous piled-up auburn hairdo topped with a small feathered hat, pearl earrings, a high lace collar, a dusty rose bustle dress down to the ground, and a frilly parasol held over one shoulder." ;;
    *) echo "unknown kind: $1" >&2; return 1 ;;
  esac
}

generate() { # kind
  local kind=$1 out="$OUT/$1.png" id status resp prompt
  prompt="$(subject "$kind") $STYLE"
  id=$(jq -n --arg m "$MODEL" --arg p "$prompt" \
        '{model:$m,prompt:$p,size:"1536*2304",output_format:"png"}' |
      curl -fsS -X POST "$API/generateImage" -H "Authorization: Bearer $ATLASCLOUD_API_KEY" \
        -H 'Content-Type: application/json' -d @- | jq -r .data.id)
  for _ in $(seq 1 120); do
    resp=$(curl -fsS "$API/prediction/$id" -H "Authorization: Bearer $ATLASCLOUD_API_KEY")
    status=$(jq -r .data.status <<<"$resp")
    case $status in
      completed|succeeded) curl -fsS -o "$out" "$(jq -r '.data.outputs[0]' <<<"$resp")"; echo "wrote $out"; return ;;
      failed) echo "$kind failed: $(jq -r .data.error <<<"$resp")" >&2; return 1 ;;
    esac
    sleep 3
  done
  echo "timed out: $kind" >&2; return 1
}

KINDS=("$@")
[ ${#KINDS[@]} -eq 0 ] && KINDS=(gent knight hanky parrot egg lady)
pids=()
for k in "${KINDS[@]}"; do generate "$k" & pids+=($!); done
fail=0
for p in "${pids[@]}"; do wait "$p" || fail=1; done
exit $fail
