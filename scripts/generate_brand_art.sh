#!/usr/bin/env bash
# Regenerates the Eggify brand art (app icon master + README banner) with
# Atlas Cloud text-to-image (google/nano-banana-pro). Needs ATLASCLOUD_API_KEY.
#
#   ATLASCLOUD_API_KEY=... scripts/generate_brand_art.sh [out_dir]
#
# Generations are not deterministic; the committed files are one pick.
# Derived assets (iOS/Tauri/PWA icons, favicons) are produced from
# icon.png with ImageMagick and `npx @tauri-apps/cli icon` (see
# docs/MEDIA_PROVENANCE.md).
set -euo pipefail
: "${ATLASCLOUD_API_KEY:?set ATLASCLOUD_API_KEY}"
OUT=${1:-./brand-art}
mkdir -p "$OUT"
API=https://api.atlascloud.ai/api/v1/model
MODEL=google/nano-banana-pro/text-to-image

STYLE="in the style of 1970s British surreal cut-out collage animation: hand-coloured Victorian copperplate engravings cut out with scissors, flat paper layers, muted sepia and cream paper with bold pink, mustard and teal accents, absurdist comedy"

generate() { # out ratio prompt
  local out=$1 ratio=$2 prompt=$3 id status resp
  id=$(jq -n --arg m "$MODEL" --arg p "$prompt" --arg r "$ratio" \
        '{model:$m,prompt:$p,aspect_ratio:$r,resolution:"2k",output_format:"png"}' |
      curl -fsS -X POST "$API/generateImage" -H "Authorization: Bearer $ATLASCLOUD_API_KEY" \
        -H 'Content-Type: application/json' -d @- | jq -r .data.id)
  for _ in $(seq 1 100); do
    resp=$(curl -fsS "$API/prediction/$id" -H "Authorization: Bearer $ATLASCLOUD_API_KEY")
    status=$(jq -r .data.status <<<"$resp")
    case $status in
      completed|succeeded) curl -fsS -o "$out" "$(jq -r '.data.outputs[0]' <<<"$resp")"; echo "wrote $out"; return ;;
      failed) echo "failed: $(jq -r .data.error <<<"$resp")" >&2; return 1 ;;
    esac
    sleep 3
  done
  echo "timed out: $out" >&2; return 1
}

generate "$OUT/icon.png" 1:1 "A perfectly square full-bleed illustration that fills the entire canvas edge to edge: NO rounded corners, NO border, NO frame, NO white margin, NO text. A dignified Victorian-engraved egg with a pompous waxed handlebar moustache, monocle and a tiny bowler hat, cracked open at the top with a green glowing terminal prompt '>_' visible inside the crack, wearing a cut-out paper collar in mustard and teal with a pink lining. A giant cut-out bare foot descends from engraved cream clouds above it. Pale teal sky. $STYLE. Egg large and centered, bold silhouette readable at small sizes."

generate "$OUT/banner.png" 21:9 "Wide banner for a software project called EGGIFY. The word 'EGGIFY' in large ornate Victorian circus poster lettering, cut out of paper. Around it: a pompous engraved egg with handlebar moustache and bowler hat, a giant bare foot descending from the clouds, cherubs, cogs, a vintage typewriter, and a small cut-out laptop with a '>_' terminal prompt. $STYLE. Generous margins, text perfectly spelled EGGIFY."
