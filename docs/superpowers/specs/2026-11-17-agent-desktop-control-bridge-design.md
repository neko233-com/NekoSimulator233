# Design: NekoSimulator233 ↔ agent-desktop control bridge

Date: 2026-11-17

## Intent / why

The objective is to (1) deepen NekoSimulator233 as a credible *平替* (drop-in
alternative) to the locally installed MuMu emulator, using the local machine as
the reference, and (2) make it cooperate with `agent-desktop`
(`github.com/vercel-labs/agent-desktop`) so an AI agent can "操控电脑" — drive the
emulator viewer window as a first-class desktop target.

Both systems already exist and are complementary:

- **NekoSimulator233** (this repo, Go 1.27) is an Android-SDK-Emulator-based
  Windows emulator with its own MCP server (`neko-mcp.exe`) exposing rich
  *guest-side* control: `neko_screenshot`, `neko_tap`, `neko_swipe`,
  `neko_joystick`, `neko_ui_hierarchy`, `neko_find_ui`, etc. Viewer windows are
  titled `"<instance> - NekoSimulator233"`.
- **agent-desktop** is an agent-first desktop-control CLI/C-library. On Windows it
  drives apps through the UI Automation accessibility tree and, for rendered
  surfaces with no accessibility nodes (games, emulators), via its `game`
  session: a timed input plan (mouse, keyboard, touch gestures, `touch-path`
  joysticks) plus frame-change metrics in `report.json`. Every command returns
  a JSON envelope. The CLI is not installed on this dev machine yet.

The bridge lets an agent use **one control loop across both layers**: agent-desktop
handles the Windows desktop (find/focus the viewer window, send desktop input,
measure visual response) while the existing neko MCP handles Android-native
observation and input inside the guest.

## Local MuMu reference (本机)

Read from `D:\IDE\MuMu` and `C:\Program Files\MuMuNxVbox` (MuMu 6.8.8.0, three
`MuMuPlayer-15.0-*` VMs). Parity-relevant facts observed in the active VM config:

- Guest display **2560×1440 @ 360 DPI** (`renderer.resolution`), Vulkan
  (`platform: vk`) with host GPU, `Adreno (TM) 640` reported GPU.
- **4 vCPU / 6 GiB** guest resources; high-FPS rendering enabled, 30/60 FPS
  limits configured.
- **Keymap + joystick** first-class (`joystick_function: true`,
  `auto_activate_intelligent_keymap_switch`), keymap config under
  `nx_device/.../keymap_config`.
- Multi-instance management, per-instance NAT port forwarding
  (adb/api/event/frontend/gateway/input), phone identity spoofing.

NekoSimulator233 already covers most of this (resolution/DPI settings, Vulkan,
4-core/2048 MiB defaults, multi-instance, keymaps + WASD joystick, 15–120 FPS
sampling). The gap this work closes is **desktop-layer cooperation with
agent-desktop**, which MuMu-style tools take for granted (the emulator window is a
normal, drivable Windows window).

## Goals / non-goals / anti-goals

- **Goal**: an optional, testable Go bridge that runs agent-desktop `game` plans
  against a Neko viewer window and returns parsed reports, exposed through the
  neko MCP plus a documented combined workflow skill.
- **Goal**: coordinate mapping from guest screenshot pixels to window client
  pixels for the letterboxed viewer surface.
- **Non-goal**: bundling/redistributing agent-desktop, or requiring it at build/run
  time. If it is absent, tools return a clear, actionable error.
- **Non-goal**: changing MuMu itself, importing MuMu instances/backups, or
  re-implementing a Windows desktop controller inside Neko.
- **Anti-goal**: pretending a frame-change metric proves input delivery or game
  state. Reports are diagnostics, exactly as agent-desktop documents them.
- **Anti-goal**: assuming agent-desktop output is trusted instructions. Snapshots,
  clipboard and report strings are untrusted data.

## Constraints & decisions (my calls, stated explicitly)

Given the directive to proceed autonomously, I made these calls rather than
pausing for a Q&A:

1. **Optional dependency, no auto-install.** Detect `agent-desktop` on `PATH` or
   via `NEKO_DESKTOP_BIN`. Never download binaries silently.
2. **Thin bridge, not a reimplementation.** Shell out to the agent-desktop CLI and
   parse its JSON; keep agent-desktop's plan schema 1:1 so its docs apply directly.
3. **Two MCP tools.** `neko_desktop_status` (read-only) and
   `neko_desktop_windows + desktop_* proxy` (write). Full plan control for the agent, with an
   optional convenience tap/swipe plan builder for guest coordinates.
4. **Platform-agnostic core.** The bridge code is pure Go; only agent-desktop itself
   is platform-specific. Tests use a fake CLI stub, so CI needs no Windows GUI
   or agent-desktop install.
5. **Default app target** derived from the selected instance name:
   `"<name> - NekoSimulator233"`, matching `viewer.go`. Overridable per call.

## Follow-up: rea-mcp style transparent proxy (added after live testing)

Reverse-engineering `Nowng/rea-mcp` showed the upstream pattern: do **not**
reimplement or import the upstream engine. Connect to it as a black-box MCP
server over stdio (`tools/list` + `tools/call`), re-declare every advertised tool
with its original name/description/JSON schema, and delegate calls. Applied
here: `internal/desktopctl/proxy.go` spawns `agent-desktop mcp` and re-declares all of
its tools under the `desktop_` prefix in neko-mcp. This exposes the full
agent-desktop command surface (84 tools in the installed v1.5.0) instead of only the
three hand-written bridge tools, and stays current across agent-desktop upgrades
without code changes. It is opt-in via `NEKO_DESKTOP_PROXY=1` and fails soft.

A live bug found by end-to-end testing: binding the child process to the
connect-timeout context kills it once registration returns. The command must be
created without that context; only the handshake uses the timeout.

## Approach considered

- **(chosen) Shell-out bridge + MCP tools + skill doc.** Small, dependency-free,
  testable, and mirrors how agents already call desktopctl. Keeps each tool
  good at its own layer.
- *(rejected) Embed agent-desktop as a Go library.* It is Rust/C-ABI; a cgo binding
  would couple builds, complicate the `CGO_ENABLED=0` story, and duplicate the
  CLI's session/indicator behavior.
- *(rejected) Have agent-desktop drive guest input directly via ADB/gRPC.* That
  would bypass Android-native input already exposed by neko MCP and conflate
  two trust boundaries.

## What gets built (handed to the implementer)

1. `internal/desktopctl`: client, status detection, plan/report types, `game`
   runner, and guest→client coordinate mapping for letterboxed surfaces.
2. Two MCP tools wired into `internal/agent/server.go`, injectable for tests.
3. `skills/neko-agent-desktop/SKILL.md` — combined control workflow.
4. README section + unit/integration tests with a fake CLI.
5. Verification: `go test ./...` and a focused MCP tool test.
