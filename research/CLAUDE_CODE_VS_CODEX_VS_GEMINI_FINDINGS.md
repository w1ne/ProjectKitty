# Claude Code vs Codex vs Gemini CLI: Comparative Findings

This note extends the [Claude Code vs Codex findings](./CLAUDE_CODE_VS_CODEX_FINDINGS.md) with Gemini CLI added as a third data point.

Gemini CLI source is fully readable (unminified TypeScript), which means findings here have higher confidence than the Claude Code analysis (minified bundle) or the Codex analysis (Rust binary + partial Node wrapper).

> **Second-pass dark corners added**: After a full source deep-dive, several significant hidden behaviors were documented in `gemini/GEMINI_REVERSE_REPORT.md` Section 23. Key ones with cross-tool implications are summarized in the [Dark Corners section below](#dark-corners-gemini-specific).

## Scope

- Claude Code: [CLAUDE_REVERSE_REPORT.md](./CLAUDE_REVERSE_REPORT.md)
- Codex: [codex/](./codex/)
- Gemini CLI: [gemini/](./gemini/) — especially [GEMINI_REVERSE_REPORT.md](./gemini/GEMINI_REVERSE_REPORT.md)

> **Note**: The Gemini CLI section has been updated through **v0.33.1** (March 2026). Original analysis was v0.27.3. All new findings are in [Section 24 of the Gemini report](./gemini/GEMINI_REVERSE_REPORT.md#24-changes-v0280--v0331).

## Comparison Table

| Area | Claude Code | Codex | Gemini CLI |
|---|---|---|---|
| **Source transparency** | Minified single bundle (~11 MB) — hard to read | Rust binary + partial Node wrapper | Fully unminified TypeScript — all internals visible |
| **Memory model** | `MEMORY.md` per-project + JSONL history | SQLite state DB + two-phase ghost compaction | `GEMINI.md` global + per-project; in-memory compression |
| **Session resume** | JSONL history re-read, compaction may cause drift | SQLite-backed, structured re-hydration | `--resume <id>`: reloads exact JSON conversation array |
| **Agent architecture** | Bark loop + teammate routines + summarizer | Multi-agent lifecycle (`spawn`, `resume`, `close`, `send_message`) | `LocalAgentExecutor` per agent + A2A federated protocol |
| **Sub-agent isolation** | Nested context with key findings handoff | Context forking with parent history preservation | Isolated `ToolRegistry` per agent; recursion blocked |
| **Safety model** | Custom `SandboxedBash` + heuristic misparse checks | `bubblewrap` (Linux) with explicit policy definitions | Four `ApprovalMode` levels + full `PolicyEngine` with regex rules |
| **Sandbox** | macOS seatbelt only | Linux bubblewrap only | macOS seatbelt + Docker/Podman + **gVisor** + LXC (5 options) — strongest cross-platform |
| **Shell security** | SHA256 hash of allowed commands | Approval levels + policy | `PolicyEngine` + redirection downgrade + `pgrep` PID tracking |
| **Feature flags** | 663+ remote Tengu flags (GrowthBook) | Small explicit set | 8 numeric CCPA flags — minimal remote behavior control |
| **Enterprise controls** | GrowthBook A/B with `TENGU_LOCAL_OVERRIDES` | N/A | CCPA admin server polling + IPC propagation to child process |
| **Auth options** | OAuth or `ANTHROPIC_API_KEY` | API key | 4 types: OAuth, ADC, Gemini key, Vertex AI |
| **Model routing** | Tiered fallback (Sonnet → Opus → Haiku) | Single model | AI classifier (Pro/Flash) + Gemini 3 preview tier |
| **Model transparency** | Hidden behind flags | Simple enumeration | Full alias → concrete model resolution in source |
| **Telemetry** | Statsig + Sentry + GrowthBook + beacons | OpenTelemetry-based | OpenTelemetry + ClearcutLogger (Google internal) |
| **Prompt privacy** | Broad telemetry; redaction present | More conservative | `logPrompts=false` by default — prompts not sent |
| **Prompt engineering** | Large static blocks in minified bundle | `AGENTS.md` hierarchy + skill system | Modular `PromptProvider` with named sections, runtime composition |
| **System prompt override** | Not documented externally | `AGENTS.md` / skill files | `GEMINI_SYSTEM_MD` env var + `GEMINI_WRITE_SYSTEM_MD` dump |
| **Hook system** | Post-tool auto-format hooks only | N/A | Full event-driven hook system: SessionStart/End + ToolConfirmation |
| **Skills** | N/A | Explicit skill system (`AGENTS.md`) | First-class `SkillManager` (global + project) + `ActivateSkillTool` |
| **MCP integration** | Proxied through `mcp-proxy.anthropic.com` | Native MCP client (`codex-mcp-client`) | Native MCP client + per-server OAuth + A2A protocol |
| **UI framework** | React + Ink (standard) | Ratatui (Rust TUI) | React 19 + forked Ink (`@jrichman/ink`) + Kitty keyboard protocol |
| **Vim mode** | Not observed | N/A | First-class `VimModeProvider` |
| **IDE integration** | VS Code awareness + clickable links | N/A | Zed editor integration (experimental) + `IdeIntegrationNudge` |
| **Config format** | JSON (`settings.json`) | TOML flags | JSON with 5-layer scope merge (remote admin → workspace → user → system → defaults) |
| **Update mechanism** | `npm view ... version` check on startup | N/A | `latest-version` npm check + `handleAutoUpdate()` |

## Main Findings

### 1. Gemini CLI is the most transparent architecturally

The unminified source means every architectural decision is directly readable. This is either a consequence of being open-source (Apache-2.0), or deliberate transparency. Either way, the Gemini CLI is the easiest to audit and understand of the three.

Claude Code hides everything behind an 11 MB minified bundle. Codex hides the core logic in a pre-compiled Rust binary. Gemini exposes it all.

### 2. Gemini CLI has the strongest cross-platform sandbox story

Claude Code supports macOS seatbelt only. Codex supports Linux bubblewrap only. Gemini now supports **five** sandbox modes: macOS seatbelt, Docker, Podman, gVisor (runsc), and LXC/LXD — covering every major platform with the strongest isolation option in the set (gVisor, which runs containers inside a user-space Go kernel and intercepts all syscalls).

The Docker sandbox image is versioned with the CLI itself, meaning sandbox behavior is reproducible across environments. gVisor and LXC were added in v0.31.0–v0.33.0.

### 3. Gemini CLI has the cleanest privacy posture

`logPrompts = false` by default means Gemini is the only CLI of the three where user prompts are not sent to telemetry unless explicitly opted in.

Claude Code's broad telemetry stack (project hashing, git metadata, environment fingerprinting, session tracking) has the widest collection surface. Codex sits in the middle.

### 4. Gemini CLI has the most sophisticated approval system

Claude Code has a binary `isAuthorized` check (SHA256 of command). Codex has approval levels with explicit policy.

Gemini goes further: a full `PolicyEngine` with regex-based rule matching, wildcard server names, priority ordering, persistent rule files, mode-specific applicability, and `MessageBus`-based decoupled confirmation flow. The four `ApprovalMode` levels (DEFAULT / PLAN / AUTO_EDIT / YOLO) give users real semantic control.

### 5. Claude Code is the most remotely controlled

663+ Tengu flags controlled via GrowthBook mean Claude Code's behavior is the most opaque and variable of the three. A session with `tengu_fast_mode_toggled=true` may behave differently than one without. There's no easy way for users to enumerate or reproduce which flags applied to a given session.

Gemini has 8 CCPA flags. Codex's flag surface is the smallest.

### 6. Gemini's hook system is unique

Neither Claude Code nor Codex has a comparable event-driven hook system. Gemini's `SessionStart` hook can inject context into every conversation, `SessionEnd` guarantees cleanup, and `ToolConfirmation` hooks allow automation of approval decisions. This makes Gemini the most extensible for scripts-as-governance patterns.

### 7. Model routing: Gemini is the only one with a live classifier

Claude Code picks models based on task tier (Sonnet/Opus/Haiku) with flag-controlled fallback. Codex uses a single model. Gemini's `ENABLE_NUMERICAL_ROUTING` flag activates an AI classifier that dynamically selects Pro or Flash per query based on a configurable threshold — the most sophisticated model selection of the three.

## Where Each System Looks Strongest

| Dimension | Strongest |
|---|---|
| Transparency / auditability | **Gemini CLI** |
| Sandbox isolation | **Gemini CLI** |
| Privacy posture | **Gemini CLI** |
| Approval system expressiveness | **Gemini CLI** |
| Hook / automation extensibility | **Gemini CLI** |
| Long-session memory integrity | **Codex** (SQLite + ghost snapshots) |
| Infrastructure / systems foundation | **Codex** (Rust core) |
| Reproducibility | **Codex** |
| Product polish | **Claude Code** |
| Adaptive orchestration | **Claude Code** |
| Remote experimentation depth | **Claude Code** |

## Source Pointers

### Claude Code
- [CLAUDE_REVERSE_REPORT.md](./CLAUDE_REVERSE_REPORT.md)

### Codex
- [codex/architecture.md](./codex/architecture.md)
- [codex/agents.md](./codex/agents.md)
- [codex/memory_history.md](./codex/memory_history.md)
- [codex/sandbox_details.md](./codex/sandbox_details.md)
- [codex/telemetry.md](./codex/telemetry.md)
- [codex/tools.md](./codex/tools.md)
- [codex/skills_system.md](./codex/skills_system.md)

### Gemini CLI
- [gemini/GEMINI_REVERSE_REPORT.md](./gemini/GEMINI_REVERSE_REPORT.md)
- [gemini/architecture.md](./gemini/architecture.md)
- [gemini/agents.md](./gemini/agents.md)
- [gemini/tools.md](./gemini/tools.md)
- [gemini/memory_history.md](./gemini/memory_history.md)
- [gemini/auth_security.md](./gemini/auth_security.md)
- [gemini/sandbox_details.md](./gemini/sandbox_details.md)
- [gemini/telemetry.md](./gemini/telemetry.md)
- [gemini/hooks_policy.md](./gemini/hooks_policy.md)
