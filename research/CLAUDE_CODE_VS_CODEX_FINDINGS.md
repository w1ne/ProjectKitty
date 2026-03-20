# Claude Code vs Codex vs Gemini CLI: Comparative Findings

This note summarizes the main findings from the Claude Code reverse-engineering report, the Codex research notes, and the Gemini CLI research notes in this repository.

The goal is not to crown a winner. The goal is to identify architectural differences, state which claims are directly supported by evidence, and mark which comparisons still require benchmarking.

Gemini CLI source is fully readable (unminified TypeScript), which means findings here have higher confidence than the Claude Code analysis (minified bundle) or the Codex analysis (Rust binary + partial Node wrapper).

> **Second-pass dark corners added**: After a full Gemini source deep-dive, several significant hidden behaviors were documented in `gemini/GEMINI_REVERSE_REPORT.md` Section 23. Key ones with cross-tool implications are summarized in the [Dark Corners section below](#dark-corners-gemini-specific).

## Scope

- Claude Code evidence comes from [CLAUDE_REVERSE_REPORT.md](./CLAUDE_REVERSE_REPORT.md).
- Codex evidence comes from the files under [codex/](./codex/).
- Gemini CLI evidence comes from the files under [gemini/](./gemini/) — especially [GEMINI_REVERSE_REPORT.md](./gemini/GEMINI_REVERSE_REPORT.md).
- `Observed` means the claim is directly supported by the documents in this repository.
- `Hypothesis` means the claim is an engineering inference that still needs testing.
- We should not treat hypotheses as product facts until they are measured.

> **Note**: The Gemini CLI section has been updated through **v0.33.1** (March 2026). Original analysis was v0.27.3. All new findings are in [Section 24 of the Gemini report](./gemini/GEMINI_REVERSE_REPORT.md#24-changes-v0280--v0331).

## Comparison Table

| Area | Claude Code | Codex | Gemini CLI | Status | Current Conclusion |
|---|---|---|---|---|---|
| **Source transparency** | Minified single bundle (~11 MB) — hard to read | Rust binary + partial Node wrapper | Fully unminified TypeScript — all internals visible | Observed | Gemini is the easiest to audit of the three. |
| **Memory model** | `MEMORY.md` per-project + JSONL history + aggressive compaction | Two-phase memory writers, ghost snapshot compaction, SQLite-backed state DB | `GEMINI.md` global + per-project; in-memory compression | Observed | The memory architectures are materially different. Whether one preserves correctness better still needs measurement. |
| **Context recovery** | Full transcript remains on disk; may need reread after compaction | Ghosted history preserves re-hydration potential within structured memory pipeline | `--resume <id>`: reloads exact JSON conversation array | Observed | Claude and Codex use different recovery strategies. Gemini offers exact session replay. Reliability under long sessions is still unmeasured. |
| **Agent architecture** | Bark loop + teammate routines + summarizer sub-agents | Explicit multi-agent lifecycle (`spawn_agent`, `resume_agent`, `close_agent`, `send_message`) | `LocalAgentExecutor` per agent + A2A federated protocol | Observed | Codex exposes more explicit agent lifecycle primitives; Gemini adds a federated agent-to-agent protocol. |
| **Sub-agent isolation** | Nested context with key findings handoff | Context forking with parent history preservation | Isolated `ToolRegistry` per agent; recursion blocked | Hypothesis | Handoff fidelity should be tested with controlled multi-agent tasks before claiming one is better. |
| **Safety model** | Custom `SandboxedBash` + heuristic misparse checks + environment sterilization | `bubblewrap` sandbox + explicit policies + approval modes + host/sandbox boundary controls | Four `ApprovalMode` levels + full `PolicyEngine` with regex rules | Observed | Codex documents a more explicit sandbox model than Claude Code; Gemini adds the most expressive approval policy system. |
| **Sandbox** | macOS seatbelt only | Linux bubblewrap only | macOS seatbelt + Docker/Podman + **gVisor** + LXC (5 options) — strongest cross-platform | Observed | Gemini has the strongest and broadest cross-platform sandbox story. |
| **Shell security** | SHA256 hash of allowed commands | Approval levels + policy | `PolicyEngine` + redirection downgrade + `pgrep` PID tracking | Observed | Gemini's shell security model is the most explicit and auditable. |
| **Feature flags** | 663+ remote Tengu flags (GrowthBook) | Smaller surfaced flag set | 8 numeric CCPA flags — minimal remote behavior control | Observed | Claude Code is the most remotely controlled; Gemini has the smallest remote flag surface. |
| **Enterprise controls** | GrowthBook A/B with `TENGU_LOCAL_OVERRIDES` | N/A | CCPA admin server polling + IPC propagation to child process | Observed | Claude Code's behavior is the most opaque and variable across sessions. |
| **Auth options** | OAuth or `ANTHROPIC_API_KEY` | API key | 4 types: OAuth, ADC, Gemini key, Vertex AI | Observed | Gemini offers the most auth flexibility. |
| **Model routing** | Tiered fallback (Sonnet → Opus → Haiku) with automatic escalation | No equally prominent dynamic fallback system surfaced | AI classifier (Pro/Flash) + Gemini 3 preview tier | Observed | Claude Code documents fallback; Gemini uses a live AI classifier per query — the most sophisticated model selection of the three. |
| **Model transparency** | Hidden behind flags | Simple enumeration | Full alias → concrete model resolution in source | Observed | Gemini's model routing is fully auditable. |
| **Telemetry** | Statsig + Sentry + GrowthBook + beacons + project hashing + git metadata | OpenTelemetry-based observability | OpenTelemetry + ClearcutLogger (Google internal) | Observed | Claude Code has the widest collection surface. |
| **Prompt privacy** | Broad telemetry; redaction present | More conservative | `logPrompts=false` by default — prompts not sent | Observed | Gemini is the only CLI where prompts are not sent to telemetry unless explicitly opted in. |
| **Prompt engineering** | Large static blocks in minified bundle | `AGENTS.md` hierarchy + skill system | Modular `PromptProvider` with named sections, runtime composition | Observed | Gemini's prompt engineering is the most modular and inspectable. |
| **System prompt override** | Not documented externally | `AGENTS.md` / skill files | `GEMINI_SYSTEM_MD` env var + `GEMINI_WRITE_SYSTEM_MD` dump | Observed | Gemini exposes the most direct system prompt control. |
| **Hook system** | Post-tool auto-format hooks only | N/A | Full event-driven hook system: SessionStart/End + ToolConfirmation | Observed | Gemini is the only tool with a first-class hook system usable for scripts-as-governance patterns. |
| **Skills** | N/A | Explicit skill system (`AGENTS.md`) | First-class `SkillManager` (global + project) + `ActivateSkillTool` | Observed | Codex and Gemini both have skill systems; Claude Code does not expose one. |
| **MCP integration** | Proxied through `mcp-proxy.anthropic.com` | Native MCP client (`codex-mcp-client`) | Native MCP client + per-server OAuth + A2A protocol | Observed | Claude Code introduces an extra proxy boundary. Gemini adds the richest MCP extension story. |
| **UI framework** | React + Ink (standard) | Ratatui (Rust TUI) | React 19 + forked Ink + Kitty keyboard protocol | Observed | All three differ materially in UI stack. |
| **Vim mode** | Not observed | N/A | First-class `VimModeProvider` | Observed | Gemini is the only CLI with native vim mode. |
| **IDE integration** | VS Code awareness + clickable links | N/A | Zed editor integration (experimental) + `IdeIntegrationNudge` | Observed | Claude Code targets VS Code; Gemini targets Zed. |
| **Config format** | JSON (`settings.json`) | TOML flags | JSON with 5-layer scope merge (remote admin → workspace → user → system → defaults) | Observed | Gemini has the most sophisticated config hierarchy. |
| **Update mechanism** | `npm view` version check on startup | N/A | `latest-version` npm check + `handleAutoUpdate()` | Observed | Both Claude Code and Gemini implement auto-update; Codex does not surface this. |
| **Runtime foundation** | Node.js bundle with custom PTY and orchestration layers | Rust core + dedicated exec layer + PTY tools + structured IPC | TypeScript/Node.js | Observed | Implementation stacks are materially different. Stability and performance need tests. |
| **Local instruction control** | Internal prompts and product modes dominate the report | Explicit `AGENTS.md` hierarchy + skill system | `GEMINI.md` global/project hierarchy + `SkillManager` | Observed | Codex and Gemini expose more repository-local instruction machinery than Claude Code. |

## Evidence-Based Findings

### 1. Gemini CLI is the most transparent architecturally

Observed:
The unminified source means every architectural decision is directly readable. Claude Code hides everything behind an 11 MB minified bundle. Codex hides the core logic in a pre-compiled Rust binary. Gemini exposes it all (Apache-2.0 license).

### 2. Claude Code is weaker on long-session memory reliability

Observed:
Claude Code relies on aggressive session compaction and `MEMORY.md` updates to keep context within budget. Codex uses phase-based consolidation, ghost snapshots, and a SQLite state database. Gemini uses `--resume <id>` to reload the exact JSON conversation array.

Hypothesis:
Claude Code may be more vulnerable to summary drift in long sessions.

Required measurement:
- Run the same long-horizon task in all three systems.
- Force at least one compaction event.
- Measure retained factual correctness after compaction.
- Measure how often the agent must reread prior transcript to recover lost detail.

### 3. Claude Code is weaker on reproducibility

Observed:
The reverse-engineering report surfaced more than 600 remote feature flags and explicit model fallback logic in Claude Code. Codex surfaces a smaller flag set and more explicit local control primitives. Gemini surfaces only 8 CCPA flags.

Hypothesis:
Claude Code may be less reproducible across runs because more behavior is remotely steerable.

Required measurement:
- Repeat identical tasks across multiple runs and machines.
- Record tool-selection variance, edit variance, and completion variance.
- Compare results with flags fixed where possible.

### 4. Claude Code is weaker on privacy and trust posture

Observed:
The Claude Code report shows a broad telemetry and experimentation stack, including project path hashing, git metadata, environment targeting, and cloud/environment fingerprinting. Gemini defaults to `logPrompts=false` — prompts are not sent to telemetry unless explicitly opted in. Codex sits in the middle.

Current conclusion:
Claude Code has the largest exposed trust surface in the current research set. Gemini has the cleanest privacy posture.

### 5. Gemini CLI has the strongest cross-platform sandbox story

Observed:
Claude Code supports macOS seatbelt only. Codex supports Linux bubblewrap only. Gemini supports five sandbox modes: macOS seatbelt, Docker, Podman, gVisor (runsc), and LXC/LXD — covering every major platform.

gVisor runs containers inside a user-space Go kernel and intercepts all syscalls — the strongest isolation option in the set. The Docker sandbox image is versioned with the CLI itself, meaning sandbox behavior is reproducible across environments.

### 6. Gemini CLI has the most sophisticated approval system

Observed:
Claude Code has a binary `isAuthorized` check (SHA256 of command). Codex has approval levels with explicit policy. Gemini goes further: a full `PolicyEngine` with regex-based rule matching, wildcard server names, priority ordering, persistent rule files, mode-specific applicability, and `MessageBus`-based decoupled confirmation flow.

The four `ApprovalMode` levels (DEFAULT / PLAN / AUTO_EDIT / YOLO) give users real semantic control.

### 7. Gemini's hook system is unique

Observed:
Neither Claude Code nor Codex has a comparable event-driven hook system. Gemini's `SessionStart` hook can inject context into every conversation, `SessionEnd` guarantees cleanup, and `ToolConfirmation` hooks allow automation of approval decisions. This makes Gemini the most extensible for scripts-as-governance patterns.

### 8. Model routing: Gemini is the only one with a live classifier

Observed:
Claude Code picks models based on task tier (Sonnet/Opus/Haiku) with flag-controlled fallback. Codex uses a single model. Gemini's `ENABLE_NUMERICAL_ROUTING` flag activates an AI classifier that dynamically selects Pro or Flash per query based on a configurable threshold.

### 9. Claude Code appears more productized; Codex and Gemini more infrastructural

Observed:
Claude Code shows orchestration modes, teammate routines, auto-format hooks, dynamic reasoning effort, and model fallback tiers.

Observed:
Codex shows split-process design, Rust execution core, persistent state, explicit agent lifecycle, and repository-local instruction systems.

Observed:
Gemini shows modular prompt providers, a full policy engine, versioned sandbox images, and a federated agent protocol.

Hypothesis:
Claude Code may optimize for product smoothness, while Codex and Gemini may optimize for architectural control, auditability, and operational governability.

Required measurement:
- Time-to-first-useful-action
- Task completion rate
- User intervention count
- Auditability of why a given action happened
- Failure isolation when orchestration or tool execution breaks

### 10. All three systems are complex, but in different ways

Observed:
Claude Code's visible complexity centers on orchestration and experimentation. Codex's visible complexity centers on infrastructure and explicit control surfaces. Gemini's visible complexity centers on policy, extensibility, and multi-cloud auth.

Current conclusion:
Claims about which architecture is easier to govern, audit, and operate still require empirical testing.

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

## Bottom Line

If the question is what the current research already supports, the strongest answers are:

- The memory architectures are all different.
- Claude Code exposes more telemetry and feature-flag machinery in the current research.
- Codex exposes a more explicit sandbox and local-instruction model than Claude Code.
- Claude Code exposes more explicit model fallback behavior than Codex; Gemini adds a live AI classifier.
- Gemini is the strongest on sandbox isolation, privacy, approval expressiveness, and auditability.

If the question is what still needs measurement, the key items are:

- Long-session memory correctness after compaction
- Reproducibility across repeated runs
- Shell ergonomics under safety controls
- Task completion quality and intervention rate
- Performance and runtime stability

## Proposed Test Matrix

We should benchmark all three systems on the same tasks and record outputs in a reproducible format.

| Question | Test | Metric |
|---|---|---|
| Does compaction hurt correctness? | Long repo task that exceeds context window | Factual retention score after compaction |
| Is one system more reproducible? | Same prompt repeated across runs | Edit diff variance, tool-call variance, success variance |
| Which shell model is more usable? | Compound shell tasks with pipes, chaining, env vars, PTY interaction | Task completion rate, number of blocked commands, user overrides needed |
| Which safety model is clearer? | Ask each system to explain why a command was blocked or allowed | Explanation quality, policy traceability |
| Which architecture is easier to govern? | Compare how each system exposes state, policies, flags, and execution boundaries during the same task set | Auditability, state traceability, policy traceability, failure isolation |
| Which system is faster in practice? | Standard code-reading, edit, test, and debug tasks | Time to first action, total time, number of retries |
| Which system needs more user steering? | Multi-step implementation tasks | Number of clarifications, interrupts, and manual corrections |

## Next Step

This document should be treated as a research baseline, not a final verdict.

The next stage should be an empirical benchmark document with:

- fixed tasks
- fixed repos
- repeated runs
- logged outputs
- scored evaluation criteria

Current benchmark execution notes and partial results are recorded in [BENCHMARK_RESULTS.md](./BENCHMARK_RESULTS.md).

## Benchmark Status

We have started running the benchmark suite in `research/benchmarks/`.

Current status:

- Claude completed the initial five-task suite successfully in run `20260314-141035`.
- Codex completed only part of the same suite before the run became invalid for comparison.
- Gemini completed the suite in run `20260314-145850` (same run as the corrected Codex rerun).

Observed blocker (Codex):

- The Codex harness passed sandbox configuration incorrectly for `codex exec`.
- As a result, at least one writable Codex benchmark (`shell_workflow`) executed under `read-only` sandbox and could not create its output file.

Current conclusion:

- The Claude results from run `20260314-141035` are usable as raw artifacts.
- The Codex results from that run should be treated as partially invalid for benchmark comparison.
- The Gemini and corrected Codex results from run `20260314-145850` are the current valid comparison baseline.
- We need a corrected Codex rerun for the remaining benchmarks before drawing full benchmark-based conclusions.

Follow-up:

- The Codex benchmark runner was fixed to pass sandbox mode to `codex exec` correctly.
- A validation rerun of `shell_workflow` under run `20260314-144155` confirmed `sandbox: workspace-write` and successful creation of `report.md`.
- The remaining Codex benchmarks still need to be rerun under the corrected harness.

## Source Pointers

### Claude Code

- [CLAUDE_REVERSE_REPORT.md](./CLAUDE_REVERSE_REPORT.md)

Key sections:

- Memory systems and compaction
- Telemetry and feature flags
- Sandboxed bash and security
- Bark orchestration core
- Teammate mode and parallelism
- MCP proxy bridge

### Codex

- [codex/architecture.md](./codex/architecture.md)
- [codex/memory_history.md](./codex/memory_history.md)
- [codex/flags_safety.md](./codex/flags_safety.md)
- [codex/sandbox_details.md](./codex/sandbox_details.md)
- [codex/tools.md](./codex/tools.md)
- [codex/telemetry.md](./codex/telemetry.md)
- [codex/agents.md](./codex/agents.md)
- [codex/context_instructions.md](./codex/context_instructions.md)
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
