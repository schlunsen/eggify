# Meta Quest 3 support — investigation

Status: investigation + MVP (`/vr` Agent Space, see bottom) on branch `feature/quest3-vr-support`.

## TL;DR

Eggify already runs in the Meta Quest Browser (Chromium 146, WebGPU experimental) —
nothing in the stack is incompatible. The work is (1) reaching the server from the
headset, (2) making the 2D UI usable with pinch/ray input and a virtual keyboard, and
optionally (3) a WebXR "immersive" mode built on the existing three.js Zen renderer.
There is zero Quest/XR-specific code today.

## Phase 0 — reach the server from the headset

| Option | How | Notes |
|---|---|---|
| **USB/Wi-Fi adb (best for dev)** | `adb reverse tcp:3333 tcp:3333`, open `https://localhost:3333` on the Quest | Existing cert SANs (`localhost`) match; accept self-signed warning once. |
| LAN | `EGGIFY_SERVER_HOST=0.0.0.0` (`internal/server/config.go:381`) | Certs only cover localhost/127.0.0.1/::1 (`internal/server/tls.go:176-177`, `:247-252`) → name-mismatch warning. |
| Tunnel | `eggify --analytics --tunnel` (ngrok, `internal/server/tunnel.go`) | Real cert, works anywhere; needs ngrok token. |

Fixes worth doing:
- `eggify cert --san <ip/host>` (or auto-add LAN IPs) in `tls.go` / `internal/cmd/cert.go`.
- `EGGIFY_PORT` (`internal/cmd/root.go:257`) vs `EGGIFY_SERVER_PORT` (`config.go:376`) inconsistency.
- Headset login: password + TOTP on a virtual keyboard is painful. Add a **QR / device-link
  login** (desktop shows QR → headset scans with passthrough or opens short URL → one-time
  token → session cookie). The only QR today is MFA (`internal/server/mfa.go:51`).

## Phase 1 — make the 2D UI "headset-grade" (most value, lowest risk)

Detection: add `useInputProfile()` composable → `data-input="coarse"` on `<html>` when
`matchMedia('(pointer: coarse)')`, `(hover: none)` or UA contains `OculusBrowser`. Today
only `innerWidth <= 768` is used (`layouts/default.vue:350`, `Sidebar.vue:205`), and the
Quest viewport is desktop-width, so none of the touch adaptations in
`assets/css/main.css:336-367` apply.

1. **Hover-only controls** → always visible under `[data-input=coarse]` / `@media (hover: none)`:
   `SessionItem.vue:959-973` (session actions), `MessageHeader.vue:346-353` (TTS),
   `ChatInput.css:99`, `SubagentTrackerSection.vue:393`, `projects/[id]/index.vue:1281`,
   `memories.vue:746`, `ZenMode.vue:1595` tooltip.
2. **Right-click-only menus** → add a "⋯" button / long-press:
   `SessionItem.vue:13`, `FilePreview.vue:149-160`, `JustJobIndicator.vue:13`.
3. **Scale**: `main.css:24-27` already uses `--font-size-scale` but nothing sets it — expose it
   in Settings and default to ~1.25 in coarse mode. Bump 44px hit targets globally in coarse mode.
4. **Keyboard**: default shortcut modifier Shift+Alt+Meta (`useKeyboardShortcuts.ts:31`) is
   unreachable on the virtual keyboard — surface a command palette button. ChatInput
   `handleEnter` (`ChatInput.vue:624-642`) should honour `event.isComposing`, set
   `enterkeyhint="send"`.
5. **Terminal** (`SessionTerminal.vue`): add an on-screen key bar (Esc, Tab, Ctrl, arrows),
   persist font size.
6. **Voice-first** is the natural headset input: dictation already exists. Parakeet hard-codes
   WebGPU and a ~2.5GB model (`useParakeetTranscription.ts:193`) — on Quest prefer server-side
   `/api/transcribe` or Whisper wasm. Replace deprecated `createScriptProcessor`
   (`utils/audio-capture.ts:42`) with AudioWorklet.
7. **PWA**: re-enable `@vite-pwa/nuxt` (commented out, `nuxt.config.ts:9`) — Quest can install
   PWAs as windowed apps (and they can be packaged for the Horizon Store via Bubblewrap).
   The OAuth service-worker issue that caused the disable needs a `navigateFallbackDenylist`
   for `/api/**` and `/oauth-callback`.

## Phase 2 — immersive mode (the "wow" part)

`useZenAnimation.ts` is the only three.js renderer (`WebGLRenderer` at `:656`, rAF loop at
`:730`, UnrealBloom post-processing).

- Switch to `renderer.setAnimationLoop()`, enable `renderer.xr`, add an "Enter VR/AR" button
  (`immersive-ar` for passthrough on Quest 3).
- Bloom/EffectComposer does not work in XR as-is — use a cheaper in-shader glow when
  `renderer.xr.isPresenting`.
- Concept: each running agent session is an orb/panel around you; subagents orbit it;
  tool calls pulse; pinch an orb to hear its last reply (Kokoro) and hold to dictate
  a prompt. Text panels via `troika-three-text` or a DOM-overlay-lite approach.
- Headers: add `xr-spatial-tracking=(self)` to `Permissions-Policy`
  (`internal/server/setup_components.go:96-109`).

## Phase 3 (optional) — native

Tauri shell is desktop-only (spawns the Go sidecar, `src-tauri/src/lib.rs:65`). A Quest APK
would be a thin remote-URL webview (Tauri 2 Android or a Bubblewrap TWA of the PWA). The iOS
app's WS protocol (`ios/eggify/eggify/Services/WebSocketService.swift`) is small enough to port to
Kotlin, but a PWA gives 90% of the value for far less.

## Testing without / with a headset

1. **2D UI, fastest loop** — Chrome DevTools → Devices → add a custom "Quest 3" device with the
   Quest Browser UA (copy it from the headset via `navigator.userAgent`), a desktop-width
   viewport, and type "Mobile" (touch/coarse). Enough for Phase 1.
2. **WebXR** — [Immersive Web Emulator](https://chromewebstore.google.com/detail/immersive-web-emulator/cgffilbpcibhmcfbgggfhfolhkfbhmik)
   Chrome extension (emulates Quest 3 headset + controllers, WASD/mouse).
3. **Automated** — `iwer` npm package (v2.5) injected in dev builds / Playwright tests
   (`@playwright/test` already a devDep) for deterministic XR input replay.
4. **Horizon OS 2D panel app (APK path)** — Meta Spatial Simulator in the Meta Horizon
   Android Studio plugin.
5. **Real device** — enable developer mode, then
   `~/Library/Android/sdk/platform-tools/adb reverse tcp:3333 tcp:3333` and browse to
   `https://localhost:3333`. Debug via `chrome://inspect` on the Mac.

## MVP: Agent Space (`/vr`) — implemented on this branch

`app/pages/vr.vue` + `app/composables/vr/useAgentSpace.ts`: every agent session is an orb in
an arc around you (amber = needs you, teal pulsing = working, grey = recent), satellites are
subagents (bright = running). Tap / trigger / pinch an orb → activity panel with the last log
entries. Data comes from the same polling as the Agent Overview (`useAgentOverview`, 3 s).
**Enter XR** starts `immersive-ar` (passthrough) on Quest 3, or `immersive-vr` elsewhere.

Run it in the emulator (desktop Chrome):

```bash
eggify --analytics                      # or your running server
open "https://localhost:3333/vr?emulate=1"
```

`?emulate=1` lazy-loads Meta's IWER runtime + DevUI (`iwer`, `@iwer/devui` devDependencies),
installs an emulated Quest 3 over `navigator.xr`, and exposes `window.__eggifyXR = { device, space }`
for scripted tests (move `device.controllers.right`, `updateButtonValue('trigger', 1)`).

On a real Quest 3: `adb reverse tcp:3333 tcp:3333`, open `https://localhost:3333/vr` in the
Quest Browser, press **Enter XR**.
