# Eggify - Just Commands
# Install just: https://github.com/casey/just
# Works on macOS, Linux, and Windows (PowerShell)

set windows-shell := ["powershell.exe", "-NoLogo", "-Command"]

# Binary name changes based on OS
eggify := if os_family() == "windows" { "eggify.exe" } else { "eggify" }
frontend_dir := "internal/server/frontend"

# Default recipe to display help
default:
    @just --list

# Build the application (with frontend)
build:
    @node scripts/build.mjs

# Install the optional 3D horse build splash (requires Git, CMake and C++17)
setup-splash:
    @node scripts/setup-splash.mjs

# Every build/test recipe below runs through a wrapper in scripts/ that hides the
# thousands of lines of tool chatter and prints a short summary instead. The
# complete output always lands in dist/logs/<name>.log — read it with
# `just logs`, or stream it live with the matching *-verbose recipe.

# Build frontend only — summarises Nuxt/Vite warnings, routes, output size
build-frontend:
    @node scripts/build-frontend.mjs

# Build frontend with the complete Nuxt/Vite output streamed live
build-frontend-verbose:
    @node scripts/build-frontend.mjs --verbose

# Build Go binary only (assumes frontend already built)
build-go:
    @node scripts/build-go.mjs

# Build Go with the complete compiler output streamed live
build-go-verbose:
    @node scripts/build-go.mjs --verbose

# Build everything with the complete output
build-verbose: build-frontend-verbose build-go-verbose

# List the build/test logs, or print one: just logs go-test
logs name="":
    @node scripts/logs.mjs {{name}}

# Run the application
run:
    @go run ./cmd/eggify

# Run with analytics flag (web dashboard)
analytics:
    @go run ./cmd/eggify

# Run Nuxt frontend in development mode
frontend-dev:
    @echo "Starting Nuxt development server..."
    @npm --prefix {{frontend_dir}} run dev

# Build Nuxt frontend for production (alias for build-frontend)
frontend-build: build-frontend

# Install Nuxt frontend dependencies
frontend-install:
    @node scripts/build-frontend.mjs --install

# Full development workflow: build frontend then start analytics
dev-full: build-frontend build-go analytics

# Run with agents flag
agents:
    @go run ./cmd/eggify --agents

# Run with chats flag
chats:
    @go run ./cmd/eggify --chats

# Run help
help:
    @go run ./cmd/eggify --help

# Install specific agent
install-agent agent:
    @go run ./cmd/eggify --agent {{agent}}

# Install specific command
install-command command:
    @go run ./cmd/eggify --command {{command}}

# Install specific MCP
install-mcp mcp:
    @go run ./cmd/eggify --mcp {{mcp}}

# Setup TTS: download Pocket TTS model for local KITT voice clone
[unix]
setup-tts:
    @echo "🔊 Setting up local TTS (Sherpa-ONNX Pocket TTS)..."
    @mkdir -p models
    @if [ -d "models/sherpa-onnx-pocket-tts-int8-2026-01-26" ]; then \
        echo "✅ Pocket TTS model already downloaded"; \
    else \
        echo "📥 Downloading Pocket TTS model (~94MB)..."; \
        curl -sL "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/sherpa-onnx-pocket-tts-int8-2026-01-26.tar.bz2" -o models/pocket-tts.tar.bz2; \
        cd models && tar xjf pocket-tts.tar.bz2 && rm pocket-tts.tar.bz2; \
        echo "✅ Pocket TTS model downloaded"; \
    fi
    @echo ""
    @echo "🎙️  KITT voice reference:"
    @if [ -f "$HOME/.claude/assets/kitt-voice-ref.wav" ]; then \
        echo "   ✅ Found at ~/.claude/assets/kitt-voice-ref.wav"; \
    else \
        echo "   ⚠️  Not found. Place kitt-voice-ref.wav in ~/.claude/assets/ for voice cloning"; \
    fi
    @echo ""
    @echo "🎉 TTS ready! The server will auto-detect the model on startup."
    @echo "   API: POST /api/tts/generate {\"text\": \"Hello\", \"backend\": \"sherpa\"}"

[windows]
setup-tts:
    @echo "🔊 Setting up local TTS (Sherpa-ONNX Pocket TTS)..."
    @if (-not (Test-Path "models")) { New-Item -ItemType Directory -Path "models" | Out-Null }
    @if (Test-Path "models/sherpa-onnx-pocket-tts-int8-2026-01-26") { echo "✅ Pocket TTS model already downloaded" } else { echo "📥 Downloading Pocket TTS model (~94MB)..."; Invoke-WebRequest -Uri "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/sherpa-onnx-pocket-tts-int8-2026-01-26.tar.bz2" -OutFile "models/pocket-tts.tar.bz2"; tar xjf "models/pocket-tts.tar.bz2" -C models; Remove-Item "models/pocket-tts.tar.bz2"; echo "✅ Pocket TTS model downloaded" }
    @echo "🎉 TTS ready! The server will auto-detect the model on startup."

# Setup F5-TTS: download ONNX models for browser-based TTS with voice cloning
[unix]
setup-f5-tts:
    @echo "🧠 Setting up F5-TTS (browser WebGPU inference)..."
    @mkdir -p models/f5-tts
    @if [ -f "models/f5-tts/F5_Preprocess.onnx" ]; then \
        echo "✅ F5-TTS models already downloaded"; \
    else \
        echo "📥 Downloading F5-TTS ONNX models from HuggingFace (~711MB total)..."; \
        echo "   F5_Preprocess.onnx (17MB)..."; \
        curl -sL "https://huggingface.co/huggingfacess/F5-TTS-ONNX/resolve/main/F5_Preprocess.onnx" -o models/f5-tts/F5_Preprocess.onnx; \
        echo "   F5_Decode.onnx (30MB)..."; \
        curl -sL "https://huggingface.co/huggingfacess/F5-TTS-ONNX/resolve/main/F5_Decode.onnx" -o models/f5-tts/F5_Decode.onnx; \
        echo "   F5_Transformer.onnx (664MB — this takes a while)..."; \
        curl -L "https://huggingface.co/huggingfacess/F5-TTS-ONNX/resolve/main/F5_Transformer.onnx" -o models/f5-tts/F5_Transformer.onnx; \
        echo "✅ F5-TTS models downloaded"; \
    fi
    @echo ""
    @echo "🎉 F5-TTS ready for browser inference via WebGPU!"
    @echo "   Models served at: /api/tts/models/f5-tts/"

# Check TTS status
tts-status:
    @echo "🔊 TTS Backend Status:"
    @echo ""
    @echo "Pocket TTS model:"
    @if [ -d "models/sherpa-onnx-pocket-tts-int8-2026-01-26" ]; then echo "  ✅ Installed"; else echo "  ❌ Not found (run: just setup-tts)"; fi
    @echo ""
    @echo "KITT voice reference:"
    @if [ -f "$HOME/.claude/assets/kitt-voice-ref.wav" ]; then echo "  ✅ Found"; else echo "  ⚠️  Not found"; fi
    @echo ""
    @echo "macOS say:"
    @which say > /dev/null 2>&1 && echo "  ✅ Available" || echo "  ❌ Not available"

# Clean build artifacts
[unix]
clean:
    @echo "Cleaning..."
    @rm -f eggify
    @rm -rf dist/
    @echo "✅ Clean complete"

[windows]
clean:
    @echo "Cleaning..."
    @if (Test-Path eggify.exe) { Remove-Item eggify.exe }
    @if (Test-Path dist) { Remove-Item -Recurse -Force dist }
    @echo "✅ Clean complete"

# Install to GOPATH/bin
install:
    @echo "Installing eggify..."
    @go install ./cmd/eggify
    @echo "✅ Installed to GOPATH/bin"

# Run all tests — one line per package, plus any failing tests and assertions
test *packages:
    @node scripts/go-test.mjs {{packages}}

# Run tests with the complete output streamed live
test-verbose *packages:
    @node scripts/go-test.mjs --verbose {{packages}}

# Run tests with coverage (coverage.out + coverage.html)
test-coverage *packages:
    @node scripts/go-test.mjs --coverage {{packages}}

# Build for all platforms — a size per target, or the reason it can't be built
# (cross-compiling needs cgo, so only the host target works outside CI)
build-all *targets: build-frontend
    @node scripts/build-all.mjs {{targets}}

# Format code
fmt:
    @echo "Formatting code..."
    @go fmt ./...
    @echo "✅ Format complete"

# Format code (alias for fmt)
format: fmt

# Lint code — golangci-lint when installed, else `go vet`; findings grouped by linter
lint:
    @node scripts/go-lint.mjs

# Lint without failing the recipe when issues are found
lint-report:
    @node scripts/go-lint.mjs --no-fail

# Download and tidy dependencies — reports whether go.mod/go.sum moved
deps:
    @node scripts/go-deps.mjs

# Run the app with verbose logging
verbose:
    @go run ./cmd/eggify --verbose

# Quick test - build and run help
[unix]
quick: build
    @./eggify --help

[windows]
quick: build
    @./eggify.exe --help

# Development mode - build and test
dev: fmt build test
    @echo "✅ Development checks passed"

# ──────────────────────────────────────────────────────
# macOS/Linux only recipes below (release, deploy, etc.)
# ──────────────────────────────────────────────────────

# Development time estimation (unix only)
[unix]
time-used:
    @echo "🕒 Analyzing development time from git history..."
    @./scripts/time-used.sh

# Build and start the web dashboard
[unix]
start: build
    @echo "🚀 Starting eggify..."
    @./eggify -v

[windows]
start: build
    @echo "🚀 Starting eggify..."
    @./eggify.exe -v

# Create a new release (tags, builds, updates Homebrew formula) — macOS/Linux only
[unix]
release version release_name:
    #!/usr/bin/env bash
    set -euo pipefail
    \
    echo "🚀 Creating release v{{version}} - {{release_name}}..."; \
    \
    if [[ "{{version}}" != v* ]]; then \
        VERSION="v{{version}}"; \
        VERSION_NUM="{{version}}"; \
    else \
        VERSION="{{version}}"; \
        VERSION_NUM="${VERSION#v}"; \
    fi; \
    \
    if [[ -n $(git status -s) ]]; then \
        echo "❌ Working directory is not clean. Please commit or stash changes first."; \
        exit 1; \
    fi; \
    \
    TODAY=$(date +%Y-%m-%d); \
    echo "🤖 Starting Claude agent to update version and create git commit..."; \
    claude --auto-edit "Please perform the following release tasks for version $VERSION_NUM with release name '{{release_name}}': 1) Update the version constant in internal/cmd/root.go to $VERSION_NUM. 2) Update CHANGELOG.md by moving content from [Unreleased] section to a new [$VERSION_NUM] - {{release_name}} section with today's date ($TODAY), and ensure the version comparison links at the bottom are updated. The format should be: ## [$VERSION_NUM] - {{release_name}} - $TODAY. 3) Create a git commit with message 'chore: bump version to $VERSION_NUM'. 4) Create a git tag $VERSION with the message '$VERSION - {{release_name}}'. 5) Push both the commit and tag to origin."; \
    \
    echo "✅ Version updated and tagged by Claude agent"; \
    \
    echo "⏳ Waiting 60 seconds for GitHub Actions to build binaries..."; \
    sleep 60; \
    \
    echo "📦 Downloading binaries and calculating checksums..."; \
    mkdir -p /tmp/eggify-release; \
    cd /tmp/eggify-release; \
    \
    curl -sL "https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-darwin-arm64" -o eggify-darwin-arm64; \
    curl -sL "https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-darwin-amd64" -o eggify-darwin-amd64; \
    curl -sL "https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-linux-arm64" -o eggify-linux-arm64; \
    curl -sL "https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-linux-amd64" -o eggify-linux-amd64; \
    \
    SHA_DARWIN_ARM64=$(shasum -a 256 eggify-darwin-arm64 | awk '{print $1}'); \
    SHA_DARWIN_AMD64=$(shasum -a 256 eggify-darwin-amd64 | awk '{print $1}'); \
    SHA_LINUX_ARM64=$(shasum -a 256 eggify-linux-arm64 | awk '{print $1}'); \
    SHA_LINUX_AMD64=$(shasum -a 256 eggify-linux-amd64 | awk '{print $1}'); \
    \
    echo "✅ Checksums calculated"; \
    \
    echo "🍺 Updating Homebrew formula..."; \
    VERSION_NUM="${VERSION#v}"; \
    \
    cd "${EGGIFY_HOMEBREW_REPO:-$HOME/projects/homebrew-eggify}"; \
    \
    printf '%s\n' \
        'class Eggify '"<"' Formula' \
        '  desc "High-performance CLI tool for Claude Code component templates and analytics"' \
        '  homepage "https://github.com/schlunsen/claude-templates-go"' \
        "  version \"$VERSION_NUM\"" \
        '' \
        '  # This is a precompiled binary, no build tools required' \
        '  uses_from_macos "unzip" '"=>"' :build' \
        '' \
        '  on_macos do' \
        '    if Hardware::CPU.arm?' \
        "      url \"https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-darwin-arm64\"" \
        "      sha256 \"$SHA_DARWIN_ARM64\"" \
        '    else' \
        "      url \"https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-darwin-amd64\"" \
        "      sha256 \"$SHA_DARWIN_AMD64\"" \
        '    end' \
        '  end' \
        '' \
        '  on_linux do' \
        '    if Hardware::CPU.arm?' \
        "      url \"https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-linux-arm64\"" \
        "      sha256 \"$SHA_LINUX_ARM64\"" \
        '    else' \
        "      url \"https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-linux-amd64\"" \
        "      sha256 \"$SHA_LINUX_AMD64\"" \
        '    end' \
        '  end' \
        '' \
        '  def install' \
        '    # The downloaded file is a precompiled binary' \
        '    downloaded_file = Dir["eggify-*"].first' \
        '    bin.install downloaded_file '"=>"' "eggify"' \
        '    chmod 0755, bin/"eggify"' \
        '  end' \
        '' \
        '  test do' \
        '    system "#{bin}/eggify", "--help"' \
        '  end' \
        'end' \
        > Formula/eggify.rb; \
    \
    git add Formula/eggify.rb; \
    git commit -m "chore: update eggify formula to $VERSION"; \
    git push origin main; \
    \
    rm -rf /tmp/eggify-release; \
    \
    echo ""; \
    echo "✅ Release $VERSION complete!"; \
    echo ""; \
    echo "📦 GitHub Release: https://github.com/schlunsen/claude-templates-go/releases/tag/$VERSION"; \
    echo ""; \
    echo "🍺 Homebrew users can upgrade with:"; \
    echo "   brew update && brew upgrade eggify"; \
    echo ""; \
    echo "📝 Or force cache refresh:"; \
    echo "   brew untap schlunsen/eggify && brew tap schlunsen/eggify && brew install eggify"; \
    echo "";

# Start server with ngrok tunnel (requires ngrok installed) — macOS/Linux only
[unix]
ngrok: build
    #!/usr/bin/env bash
    set -euo pipefail

    # Load .env file if it exists
    if [ -f .env ]; then
        export $(cat .env | grep -v '^#' | xargs)
    else
        echo "⚠️  .env file not found"
        echo ""
        echo "Please create a .env file from .env.example:"
        echo "  cp .env.example .env"
        echo ""
        echo "Then edit .env and set your NGROK_DOMAIN"
        exit 1
    fi

    # Check if NGROK_DOMAIN is set
    if [ -z "${NGROK_DOMAIN:-}" ]; then
        echo "❌ NGROK_DOMAIN not set in .env file"
        exit 1
    fi

    # Check if ngrok is installed
    if ! command -v ngrok &> /dev/null; then
        echo "❌ ngrok is not installed"
        echo ""
        echo "Install ngrok:"
        echo "  macOS: brew install ngrok"
        echo "  Linux: https://ngrok.com/download"
        echo "  Windows: https://ngrok.com/download"
        exit 1
    fi

    # Check if ngrok authtoken is configured
    if ! ngrok config check > /dev/null 2>&1; then
        echo "⚠️  ngrok authtoken not configured"
        echo ""
        echo "Set up your ngrok authtoken:"
        echo "  1. Get your token from: https://dashboard.ngrok.com/get-started/your-authtoken"
        echo "  2. Run: ngrok config add-authtoken YOUR_TOKEN"
        echo ""
        read -p "Continue anyway? (y/n) " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            exit 1
        fi
    fi

    echo "🚀 Starting Eggify with ngrok tunnel..."
    echo "📡 Using ngrok domain: $NGROK_DOMAIN"
    echo ""

    # Start ngrok and the Go server
    # ngrok will provide HTTPS, so we disable TLS on the Go server
    ngrok http --url="$NGROK_DOMAIN" 3333 &
    NGROK_PID=$!

    # Give ngrok a moment to start
    sleep 2

    # Set up a trap to kill ngrok when this script exits (including on Ctrl+C)
    # Also handle SIGTERM and SIGINT properly
    cleanup() {
        echo ""
        echo "🛑 Shutting down..."
        kill $NGROK_PID 2>/dev/null || true
        wait $NGROK_PID 2>/dev/null || true
        exit 0
    }
    trap cleanup EXIT SIGINT SIGTERM

    # Start the server with TLS disabled (ngrok provides HTTPS)
    EGGIFY_DISABLE_TLS=true ./eggify

# Update Homebrew formula only (use after manual release) — macOS/Linux only
[unix]
update-homebrew version:
    #!/usr/bin/env bash
    set -euo pipefail
    \
    echo "🍺 Updating Homebrew formula for v{{version}}..."; \
    \
    if [[ "{{version}}" != v* ]]; then \
        VERSION="v{{version}}"; \
    else \
        VERSION="{{version}}"; \
    fi; \
    \
    echo "📦 Downloading binaries and calculating checksums..."; \
    mkdir -p /tmp/eggify-release; \
    cd /tmp/eggify-release; \
    \
    curl -sL "https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-darwin-arm64" -o eggify-darwin-arm64; \
    curl -sL "https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-darwin-amd64" -o eggify-darwin-amd64; \
    curl -sL "https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-linux-arm64" -o eggify-linux-arm64; \
    curl -sL "https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-linux-amd64" -o eggify-linux-amd64; \
    \
    SHA_DARWIN_ARM64=$(shasum -a 256 eggify-darwin-arm64 | awk '{print $1}'); \
    SHA_DARWIN_AMD64=$(shasum -a 256 eggify-darwin-amd64 | awk '{print $1}'); \
    SHA_LINUX_ARM64=$(shasum -a 256 eggify-linux-arm64 | awk '{print $1}'); \
    SHA_LINUX_AMD64=$(shasum -a 256 eggify-linux-amd64 | awk '{print $1}'); \
    \
    echo "✅ Checksums calculated"; \
    \
    VERSION_NUM="${VERSION#v}"; \
    \
    cd "${EGGIFY_HOMEBREW_REPO:-$HOME/projects/homebrew-eggify}"; \
    \
    printf '%s\n' \
        'class Eggify '"<"' Formula' \
        '  desc "High-performance CLI tool for Claude Code component templates and analytics"' \
        '  homepage "https://github.com/schlunsen/claude-templates-go"' \
        "  version \"$VERSION_NUM\"" \
        '' \
        '  # This is a precompiled binary, no build tools required' \
        '  uses_from_macos "unzip" '"=>"' :build' \
        '' \
        '  on_macos do' \
        '    if Hardware::CPU.arm?' \
        "      url \"https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-darwin-arm64\"" \
        "      sha256 \"$SHA_DARWIN_ARM64\"" \
        '    else' \
        "      url \"https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-darwin-amd64\"" \
        "      sha256 \"$SHA_DARWIN_AMD64\"" \
        '    end' \
        '  end' \
        '' \
        '  on_linux do' \
        '    if Hardware::CPU.arm?' \
        "      url \"https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-linux-arm64\"" \
        "      sha256 \"$SHA_LINUX_ARM64\"" \
        '    else' \
        "      url \"https://github.com/schlunsen/claude-templates-go/releases/download/$VERSION/eggify-linux-amd64\"" \
        "      sha256 \"$SHA_LINUX_AMD64\"" \
        '    end' \
        '  end' \
        '' \
        '  def install' \
        '    # The downloaded file is a precompiled binary' \
        '    downloaded_file = Dir["eggify-*"].first' \
        '    bin.install downloaded_file '"=>"' "eggify"' \
        '    chmod 0755, bin/"eggify"' \
        '  end' \
        '' \
        '  test do' \
        '    system "#{bin}/eggify", "--help"' \
        '  end' \
        'end' \
        > Formula/eggify.rb; \
    \
    git add Formula/eggify.rb; \
    git commit -m "chore: update eggify formula to $VERSION"; \
    git push origin main; \
    \
    rm -rf /tmp/eggify-release; \
    \
    echo ""; \
    echo "✅ Homebrew formula updated to $VERSION!"; \
    echo ""; \
    echo "🍺 Users can upgrade with:"; \
    echo "   brew update && brew upgrade eggify"; \
    echo ""; \
    echo "📝 Or force cache refresh:"; \
    echo "   brew untap schlunsen/eggify && brew tap schlunsen/eggify && brew install eggify"; \
    echo "";

# ──────────────────────────────────────────────────────
# Tauri Desktop App
# ──────────────────────────────────────────────────────

# Build the Tauri desktop app (.app + .dmg on macOS)
[unix]
tauri-build: build
    #!/usr/bin/env bash
    set -euo pipefail
    source "$HOME/.cargo/env" 2>/dev/null || true
    echo "🖥️  Building Tauri desktop app..."
    echo ""
    echo "📦 Preparing sidecar binary..."
    node src-tauri/scripts/build-sidecar.mjs
    echo ""
    echo "🦀 Building Tauri app (cargo tauri build)..."
    npm run tauri:build
    echo ""
    echo "📦 Copying build artifacts to dist/tauri/..."
    mkdir -p dist/tauri
    cp -R src-tauri/target/release/bundle/macos/*.app dist/tauri/
    cp src-tauri/target/release/bundle/dmg/*.dmg dist/tauri/
    echo ""
    echo "✅ Tauri build complete!"
    ls -lh dist/tauri/

# Run Tauri in development mode
[unix]
tauri-dev:
    #!/usr/bin/env bash
    source "$HOME/.cargo/env" 2>/dev/null || true
    npm run tauri:dev

# Install Tauri prerequisites
[unix]
tauri-setup:
    @bash src-tauri/scripts/setup.sh

# ──────────────────────────────────────────────────────
# Wails v3 Desktop App (see docs/desktop-wails.md)
# ──────────────────────────────────────────────────────

# Build the Wails desktop app into dist/wails/Eggify.app (ad-hoc signed;
# SIGN_IDENTITY="Developer ID Application: ..." signs with the hardened runtime)
[unix]
desktop-wails-build:
    @bash scripts/build-desktop-wails.sh

# Run the Wails desktop app from source (frontend must be built: just build-frontend)
[unix]
desktop-wails-dev:
    cd cmd/eggify-desktop && EGGIFY_VERBOSE=1 go run .

# Test the Wails desktop module (separate Go module, not covered by go test ./...)
[unix]
desktop-wails-test:
    cd cmd/eggify-desktop && go vet . && go test .

# ── Desktop releases (signed + notarized + self-updating, docs/desktop-wails.md) ──
# Env (or ~/.config/eggify/release.env): APPLE_TEAM_ID, SIGN_IDENTITY (auto), NOTARY_PROFILE,
#      DEPLOY_HOST, DEPLOY_DIR, PUBLIC_BASE_URL (no defaults; see scripts/release-env.sh)

# Build dist/wails/Eggify.app signed with the Developer ID (ALLOW_ADHOC=1: local ad-hoc build)
[unix]
desktop-app:
    @bash scripts/desktop-release.sh app

# desktop-app + styled DMG, sign, notarize, staple; update ZIP, .sha256 files, latest.json
[unix]
desktop-dmg:
    @bash scripts/desktop-release.sh dmg

# Pre-publish gate (Gatekeeper, stapled tickets, team/bundle id), then publish to PUBLIC_BASE_URL
[unix]
desktop-deploy:
    @bash scripts/desktop-release.sh deploy

# Full desktop release: desktop-dmg + desktop-deploy, then rebuild the landing page
# so the site shows the new version (it is static; the version is baked in at build time)
[unix]
desktop-release: desktop-dmg desktop-deploy landing-deploy

# Build and publish the landing page (DEPLOY_HOST, LANDING_DEPLOY_DIR)
[unix]
landing-deploy:
    @bash landing/deploy.sh

# Check the published desktop release over HTTPS
[unix]
desktop-verify:
    @bash scripts/desktop-release.sh verify

# Point latest.json and Eggify.dmg back at an earlier published version
[unix]
desktop-rollback VERSION:
    @bash scripts/desktop-release.sh rollback {{VERSION}}

# Deploy to VPS (build locally, deploy source, build on VPS) — macOS/Linux only
[unix]
vps-deploy:
    #!/usr/bin/env bash
    set -euo pipefail

    # Deploy target is configured via the environment so no host lives in the repo.
    # Example: EGGIFY_VPS_HOST=deploy@example.com just vps-deploy
    if [ -z "${EGGIFY_VPS_HOST:-}" ]; then
        echo "❌ EGGIFY_VPS_HOST is not set (e.g. deploy@example.com)" >&2
        echo "   Optional: EGGIFY_VPS_PATH (default /srv/eggify)" >&2
        exit 1
    fi

    VPS_HOST="$EGGIFY_VPS_HOST"
    VPS_PATH="${EGGIFY_VPS_PATH:-/srv/eggify}"
    LOCAL_PATH="$(git rev-parse --show-toplevel)"
    BINARY_NAME="eggify"

    # Get current branch name
    cd "$LOCAL_PATH"
    CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)

    echo "🚀 Deploying to VPS..."
    echo "   From: $LOCAL_PATH"
    echo "   Branch: $CURRENT_BRANCH"
    echo "   To:   $VPS_HOST:$VPS_PATH"
    echo ""

    # Build frontend locally first
    cd "$LOCAL_PATH"
    echo "📦 Building frontend locally..."
    cd internal/server/frontend && npm run generate
    cd "$LOCAL_PATH"

    echo ""
    echo "📤 Syncing source code to VPS..."

    # Create directory on VPS if it doesn't exist
    ssh "$VPS_HOST" "mkdir -p $VPS_PATH"

    # Sync only git-tracked files (excluding .git, node_modules, build artifacts)
    git ls-files -z | rsync --files-from=- --from0 -avz --delete \
        "$LOCAL_PATH/" "$VPS_HOST:$VPS_PATH/"

    echo ""
    echo "📤 Syncing Nuxt build output (.output) to VPS..."

    # Sync the Nuxt build output directory
    if [ -d "$LOCAL_PATH/internal/server/frontend/.output" ]; then
        rsync -avz --delete \
            "$LOCAL_PATH/internal/server/frontend/.output/" \
            "$VPS_HOST:$VPS_PATH/internal/server/frontend/.output/"
        echo "✅ Nuxt build output synced"
    else
        echo "⚠️  .output directory not found (may not have been built)"
    fi

    echo ""
    echo "🔨 Building on VPS (with CGO enabled for SQLite)..."

    # Build on VPS with CGO_ENABLED=1 for sqlite3 support
    # Use -buildvcs=false since .git wasn't synced to VPS
    ssh "$VPS_HOST" "cd $VPS_PATH && CGO_ENABLED=1 go build -buildvcs=false -o $BINARY_NAME ./cmd/eggify"

    echo ""
    echo "✅ Deployment complete!"
    echo "   Binary: $VPS_PATH/$BINARY_NAME"
    echo "   Run: ssh $VPS_HOST '$VPS_PATH/$BINARY_NAME'"

# Build the iOS app and upload it to TestFlight (needs ASC_KEY_ID and ASC_ISSUER_ID; see scripts/ios-testflight.sh) — macOS only
# Check toolchains, signing, notary profile, App Store Connect key and server access
[unix]
release-doctor:
    @bash scripts/release-doctor.sh

# iOS UI tests against a throwaway local server with demo data (scripts/ios-e2e-server.sh)
[macos]
ios-e2e:
    ./scripts/ios-e2e-server.sh --test

# Archive + export a signed iOS build locally without uploading (proves signing works)
[unix]
ios-testflight-dry:
    @DRY_RUN=1 bash scripts/ios-testflight.sh

[macos]
ios-testflight:
    ./scripts/ios-testflight.sh
