# Claude Code vs Codex vs Gemini CLI: Architectural Findings

This document focuses on **how each tool works**, the architectural decisions behind those choices, and what they reveal about each team's design priorities.

Evidence labels used throughout:
- **Source** — read directly from source code or reverse-engineered bundle (Gemini: unminified TypeScript; Claude Code: ~11 MB minified bundle; Codex: Rust binary + partial Node wrapper)
- **Benchmark** — observed behavior from `research/benchmarks/runs/20260314-145850/`
- **Inferred** — engineering inference, not confirmed by test

> The Gemini CLI section has the highest confidence because its source ships unminified (Apache-2.0). Claude Code and Codex findings come from bundle analysis and behavioral observation. All three comparison documents should be read with that asymmetry in mind.

---

## 1. The Main Execution Loop

How each tool drives the agent through a task is the most important architectural choice. Everything else follows from it.

### Claude Code — Bark Loop

**Source.** Claude Code uses an internal orchestration engine called **Bark**. The loop:
- Maintains a single linear context window
- Dispatches tool calls, collects results, re-enters the model
- Uses a compaction step (`MEMORY.md` flush) when context fills up
- Has special routines: `teammate` mode for parallelism, `summarizer` sub-agents for long context

The loop is opaque — all of this lives inside a minified bundle. No public turn-state enum, no typed event system.

### Codex — Explicit Lifecycle

**Source.** Codex exposes an explicit multi-agent lifecycle at the API level:
```
spawn_agent → resume_agent → send_message → close_agent
```
Each agent is a first-class object with an ID. The loop is not implicit — callers construct it. Context forking allows a sub-agent to inherit parent history.

This is a different philosophy: the orchestration is the interface, not an implementation detail.

### Gemini — Typed Turn State Machine

**Source.** Gemini's `GeminiChat` runs a typed event stream per turn. Every state transition is an explicit enum:

| Event | Meaning |
|---|---|
| `Thought` | Model is thinking (part.thought = true) |
| `Content` | Text response |
| `ToolCallRequest` | Function call dispatched |
| `Finished` | Turn complete |
| `ContextWindowWillOverflow` | Pre-emption before limit hit |
| `LoopDetected` | Infinite loop guard triggered |
| `AgentExecutionBlocked` | Policy/safety blocked the agent |
| `MaxSessionTurns` | Hard turn limit enforced |

The loop emits events; the UI subscribes to them. This means the execution engine and the display layer are cleanly separated, and every state the agent can be in has a name.

**Key difference:** Claude Code's loop is a black box. Codex's loop is an explicit API contract. Gemini's loop is a typed state machine with observable transitions.

---

## 2. Tool Execution Safety

How each tool decides whether to run a shell command or write a file reveals the safety model.

### Claude Code — Agent-Layer Interception

**Benchmark.** In the `safety_boundary` test, Claude Code running in `don't ask` mode blocked all three tool calls (Read, Bash, Write) before any OS call was made. Even the Write tool was denied — no output file was produced.

This is **agent-layer interception**: the tool call never reaches the OS. The mechanism is a SHA256 hash of permitted commands plus a permission mode flag.

Consequence: the safety system can block things the OS would have allowed. Also: it can be bypassed by changing the permission mode.

### Codex — OS-Layer Enforcement

**Benchmark.** In the same test, Codex attempted `cat /etc/shadow`, received `Permission denied` from the OS, and then successfully wrote `safety_report.md` documenting what happened.

This is **OS-layer enforcement**: the agent reaches the filesystem, the OS says no, the agent reports it. Write was not blocked.

The `bubblewrap` sandbox (Linux only) and approval policy sit between these two extremes — they're explicit, inspectable, and documented. But in this run, the OS was what actually stopped the action.

**Source.** Codex policy system has named approval levels and explicit host/sandbox socket boundary controls.

### Gemini — Full PolicyEngine + Typed Confirmation Flow

**Source.** Gemini has the most layered safety architecture of the three:

```
tool invocation
  → shouldConfirmExecute()
  → PolicyEngine.getDecision()    [via MessageBus]
  → ALLOW | DENY | ASK_USER
  → if ASK_USER: getConfirmationDetails() → typed UI
  → user: confirm / deny / always allow
  → if ProceedAlwaysAndSave → persist rule to ~/.gemini/policies/
```

`PolicyEngine` evaluates rules in priority order with:
- `toolName`: exact match or `serverName__*` wildcard
- `argsPattern`: regex against stable JSON-stringified args
- `modes[]`: rule applies only in specified `ApprovalMode`s
- `priority`: explicit ordering

Shell commands with I/O redirection (`>`, `>>`) are automatically downgraded to require confirmation even in `AUTO_EDIT` mode. Only `YOLO` bypasses this.

The four `ApprovalMode`s are semantic, not binary:
```
DEFAULT    → ask for most operations
PLAN       → read-only tools only
AUTO_EDIT  → auto-approve file edits, ask for shell
YOLO       → approve everything
```

`PLAN` mode does something unusual: it physically rewrites the system prompt to inject a 5-phase sequential workflow. The scheduler has a hardcoded `PLAN_MODE_DENIAL_MESSAGE` for any write attempt. It's behavioral gating via prompt engineering, not just a policy check.

---

## 3. File Edit Implementation Quality

How each tool applies edits to files shows engineering care (or lack of it).

### Claude Code

**Inferred.** The edit mechanism is hidden inside the minified bundle. No public information on whether there is fuzzy matching, fallback strategies, or self-correction.

### Codex

**Inferred.** Uses diff-based editing with explicit approval flow. No detail on fallback strategy available from current research.

### Gemini — 3-Tier Replacement + LLM Correction

**Source.** `EditTool` attempts replacement with three strategies in sequence:

**Tier 1 — Exact Match**
- CRLF-normalized, counts occurrences verbatim
- `safeLiteralReplace()` protects against `$` substitution sequences
- If ≥1 match: apply and stop

**Tier 2 — Indentation-Aware Match**
- All lines `.trim()`-stripped, matched window-by-window
- On match: takes the indentation of the first matched line, re-applies to the entire replacement block
- Handles cases where the model guesses wrong indentation

**Tier 3 — Regex / Token-Flexible Match**
- Tokenizes `old_string` on whitespace + delimiters `( ) : [ ] { } > < =`
- Joins tokens with `\s*` for whitespace-insensitive matching
- Captures and re-applies leading indentation

If all three tiers fail: a **4th LLM call** (`FixLLMEditWithInstruction`) asks the model to repair its own malformed edit. Telemetry logs `EditCorrectionEvent` when this fires.

`WriteFileTool` goes further: before writing any file, `getCorrectedFileContent()` runs an LLM validation call (`ensureCorrectEdit()` for existing files, `ensureCorrectFileContent()` for new ones). **Every file write can silently trigger an extra LLM API call.** This can be disabled with `config.getDisableLLMCorrection()`.

All edits: SHA256 hash of file content detects mid-edit conflicts. `restoreTrailingNewline()` preserves the file's original trailing newline behavior.

---

## 4. Memory and Long-Context Management

### Claude Code — JSONL + MEMORY.md + SQLite metadata index

**Source** (from `CLAUDE_REVERSE_REPORT.md` in the private research repo).

Three storage layers:

**1. Session history** — JSONL files at `~/.claude/projects/<project-hash>/history/`. Each turn is a typed event: `user`, `thought`, `call`, `response`, `error`, `checkpoint`. The full transcript is always preserved on disk even after compaction.

**2. Project memory** — `MEMORY.md` at `~/.claude/projects/<project-hash>/memory/MEMORY.md`. A persistent markdown file recording key decisions, project summary, and critical context. Read at the start of every session in that project. During compaction, a specialized sub-agent updates it via parallel `Edit` calls to different sections ("Learnings", "Progress", "State"). This agent is forbidden from creating new files — its sole job is maintaining semantic state.

**3. SQLite metadata index** — `sqlite3` is bundled and used for fast local lookups of file history, project mappings, and tool usage patterns without re-parsing the full JSONL.

**Compaction flow:**
- Triggered at a token/message threshold
- `summarize_tool_results` internal tool condenses prior observations
- Memory sub-agent updates `MEMORY.md`
- Full transcript remains on disk; orchestrator provides a path CTA: *"If you need details from before compaction... read the full transcript at: [path]"*

Prompt caching uses three scopes: `global` (across all sessions), `org` (team-wide), `tool-based` (tool definitions and common patterns).

### Codex — SQLite state DB + Two-Phase Ghost Compaction

**Source** (from `codex/architecture.md` and `codex/memory_history.md` in the private research repo).

**SQLite state DB** (`sqlx-sqlite`): tracks `thread_id`, `git_branch`, and `rollout_path`. Uses read-repair upsert logic for self-healing consistency.

**Two-phase memory consolidation:**

Phase 1 — Raw collection: captures all raw logs, tool outputs, and rollout summaries from the current session. Implemented in `core/src/memories/phase1.rs`.

Phase 2 — Consolidation: two operating modes:
- `INIT`: first-time generation of memory artifacts
- `INCREMENTAL UPDATE`: merges new rollout history into existing files, biasing toward newer entries

Ghost snapshot compaction prunes redundant history items by "ghosting" events no longer relevant to the current state window — preserving tokens while maintaining re-hydration potential.

Task-specific memory strategies: coding/debugging focuses on repo orientation and failure patterns; math/logic captures key transforms and lemmas. Phase 2 agents emit heartbeats to maintain ownership of the consolidation task.

### Gemini — In-Memory Compression + Exact Session Replay

**Source.** Two mechanisms:

**Compression (in-session):**
Triggered by `CONTEXT_COMPRESSION_THRESHOLD` (experiment flag ID `45740197`). `ChatCompressionService` summarizes the history in-place. Failure modes are explicit:

| Status | Meaning |
|---|---|
| `COMPRESSED` | Success |
| `COMPRESSION_FAILED_INFLATED_TOKEN_COUNT` | Summary larger than original — skipped |
| `COMPRESSION_FAILED_TOKEN_COUNT_ERROR` | Could not count tokens |
| `COMPRESSION_FAILED_EMPTY_SUMMARY` | Model returned empty summary |

Compression happens in-memory only — no on-disk audit trail of what was summarized.

**Session resume:**
`--resume <id>` passes the exact saved JSON conversation array back into the session. The original session ID is re-used. This is a full replay, not a summary — no information loss on resume.

**Dark corner:** Every unhandled API error writes a crash dump to `/tmp/gemini-client-error-<type>-<timestamp>.json` containing the full conversation history. This happens unconditionally on every turn-level error.

---

## 5. Model Routing

### Claude Code — Tiered Fallback with Remote Flags

**Source.** Tiered model selection: Sonnet → Opus → Haiku with automatic escalation. The actual routing logic is controlled by 663+ remote Tengu feature flags (GrowthBook). Behavior can vary between sessions with no user visibility into which flags are active.

### Codex — Single Model

**Source.** Uses a single model. No dynamic routing observed.

### Gemini — 5-Layer Composite Strategy

**Source.** `ModelRouterService` runs a `CompositeStrategy`. First non-null result wins:

| Priority | Strategy | Role |
|---|---|---|
| 1 | `FallbackStrategy` | Availability-aware — checks `ModelAvailabilityService` health state machine |
| 2 | `OverrideStrategy` | Hard user/admin overrides |
| 3 | `ClassifierStrategy` | LLM-based complexity classifier (own system prompt + 6 few-shot examples) |
| 4 | `NumericalClassifierStrategy` | Numeric threshold classifier |
| 5 | `DefaultStrategy` | Configured model as-is |

Every routing decision logs `source`, `latencyMs`, `reasoning`, and `failed` to telemetry.

**The hidden LLM call:** When `ENABLE_NUMERICAL_ROUTING` is off, every user prompt triggers a separate LLM call to classify complexity. The classifier has its own system prompt:
```
Choose between `flash` (SIMPLE) or `pro` (COMPLEX).
COMPLEX if: 4+ steps, strategic planning, high ambiguity, deep debugging.
SIMPLE if: highly specific, bounded, 1-3 tool calls.
```
Output is `{reasoning: string, model_choice: 'flash'|'pro'}` validated with Zod. If this call fails, the routing falls through to the numerical classifier or default.

`ModelAvailabilityService` tracks per-model health as a state machine:

| State | Meaning |
|---|---|
| (absent) | Healthy |
| `terminal` | Hard failure (quota/billing) — no retries for session |
| `sticky_retry` | Transient — one retry per turn, then blocked |

On quota exhaustion, an `upgrade` fallback intent opens a browser to `https://goo.gle/set-up-gemini-code-assist`. Only available for OAuth users; API key users get no fallback UI.

---

## 6. Multi-Agent Architecture

### Claude Code — Bark Teammates

**Source.** Claude Code has `teammate` routines for parallelism and `summarizer` sub-agents for long-context distillation. The sub-agent model is internal — not exposed as a public lifecycle API.

### Codex — Explicit Spawn/Resume/Close

**Source.** First-class multi-agent lifecycle:
```
spawn_agent → resume_agent → send_message → close_agent
```
Context forking allows sub-agents to inherit parent conversation history. This is the most explicit sub-agent model of the three — the orchestration is the public contract.

### Gemini — LocalAgentExecutor + A2A Federation

**Source.** Every sub-agent runs inside `LocalAgentExecutor`:
- **Isolated `ToolRegistry`**: each agent instance gets only the tools in its `toolConfig.tools[]`
- **Recursion blocked**: `allAgentNames` set prevents agents calling other agents
- **Loop termination**: agent must call `complete_task` tool to finish (not just stop generating)
- **Grace period**: 60 seconds after `maxTimeMinutes` before hard kill

Beyond local sub-agents, Gemini adds an **A2A (Agent-to-Agent) protocol** using `@agentclientprotocol/sdk`:
- Remote HTTP-based agent servers are discovered and registered
- `AcknowledgedAgentsService` stores user trust acknowledgments in `~/.gemini/acknowledgments/agents.json`
- Remote agents appear as callable tools via `SubagentTool` wrapper
- `AgentScheduler` coordinates parallel runs

As of v0.33.0: A2A supports OAuth2 authorization code flow, HTTP authentication headers, and authenticated agent card discovery. This is the only federated multi-agent protocol in the set.

---

## 7. Configuration and Remote Control

### Claude Code — 663+ Remote Flags

**Source.** More than 663 feature flags controlled remotely via GrowthBook (internal name: Tengu). Session behavior is remotely steerable in ways not visible to the user. `TENGU_LOCAL_OVERRIDES` provides a local escape hatch.

### Codex — Small Explicit Set

**Source.** Smaller surfaced flag set with more explicit local control primitives. `AGENTS.md` hierarchy gives repositories direct control over agent behavior.

### Gemini — 8 CCPA Flags + 5-Layer Config Merge

**Source.** Only 8 numeric experiment flags, all tied to enterprise CCPA (Code Assist) server:

| Flag | ID | Purpose |
|---|---|---|
| `CONTEXT_COMPRESSION_THRESHOLD` | 45740197 | Token count triggering compression |
| `USER_CACHING` | 45740198 | Prompt/response caching |
| `BANNER_TEXT_NO_CAPACITY_ISSUES` | 45740199 | UI banner (normal) |
| `BANNER_TEXT_CAPACITY_ISSUES` | 45740200 | UI banner (degraded) |
| `ENABLE_PREVIEW` | 45740196 | Access to Gemini 3 preview models |
| `ENABLE_NUMERICAL_ROUTING` | 45750526 | AI-based model router |
| `CLASSIFIER_THRESHOLD` | 45750527 | Routing confidence cutoff |
| `ENABLE_ADMIN_CONTROLS` | 45752213 | Enterprise admin panel |

API key users get none of these — all flags are enterprise-only. The minimal flag count makes behavior predictable across sessions.

Config is merged in 5 layers: `remote admin → workspace → user → system → defaults`. Every setting has a known provenance.

---

## 8. Hook System and Extensibility

### Claude Code — Post-Tool Hooks Only

**Source.** Hooks fire after tool execution for auto-formatting. No session lifecycle hooks.

### Codex — AGENTS.md Skills

**Source.** `AGENTS.md` provides repository-local instruction injection and a skill system. Skills are scoped to repos, not sessions.

### Gemini — Full Event-Driven Hook System

**Source.** Three hook event types with different injection points:

| Hook | When | What it can do |
|---|---|---|
| `SessionStart` | After config init, before first turn | Inject `systemMessage` and `additionalContext` |
| `SessionEnd` | On exit (`SessionEndReason.Exit`) | Cleanup; runs before telemetry flush |
| `ToolConfirmation` | Tool needs approval | Return `ALLOW`, `DENY`, or `ASK_USER` |

The `SessionStart` `additionalContext` is **prepended to the user's first message** wrapped in `<hook_context>` tags — not appended to the system prompt. This is important: it appears in the user turn, not the system turn.

`ToolConfirmation` hooks get typed serialized fields per tool type:
- `edit`: `fileName`, `filePath`, `fileDiff`, `originalContent`, `newContent`
- `exec`: `command`, `rootCommand`
- `mcp`: `serverName`, `toolName`

This enables external processes to implement governance scripts that intercept every tool decision.

Additionally: `SkillManager` (global `~/.gemini/skills/` + project `.gemini/skills/`) + `ActivateSkillTool` for runtime skill loading. Both AGENTS.md-style and runtime-activated skills exist.

---

## 9. Sandbox Architecture

| | Claude Code | Codex | Gemini CLI |
|---|---|---|---|
| macOS seatbelt | ✓ | ✗ | ✓ (customizable `.sb` profiles) |
| Linux bubblewrap | ✗ | ✓ | ✗ |
| Docker/Podman | ✗ | ✗ | ✓ (versioned image) |
| gVisor (runsc) | ✗ | ✗ | ✓ (user-space Go kernel, intercepts all syscalls) |
| LXC/LXD | ✗ | ✗ | ✓ (experimental, full-system container) |
| Cross-platform | ✗ | ✗ | ✓ |
| Evidence | Source | Source | Source |

Gemini's Docker image is versioned with the CLI binary (`us-docker.pkg.dev/.../sandbox:<version>`), meaning sandbox behavior is reproducible across machines.

gVisor (`runsc`) is the strongest isolation: the container runs inside a user-space Go kernel that intercepts all syscalls. No other tool in this set offers an equivalent.

**Sandbox re-entry flow (Gemini-specific):** Auth completes before sandbox re-entry (OAuth web redirect breaks inside sandbox). stdin is injected into args. Outer process calls `runExitCleanup()` after the sandbox process exits.

---

## 10. Telemetry and Privacy

### Claude Code
**Source.** Broadest collection surface: Statsig + Sentry + GrowthBook + beacon infrastructure. Project path hashing, git metadata, environment targeting, cloud fingerprinting.

### Codex
**Source.** OpenTelemetry-based observability. More conservative collection surface.

### Gemini
**Source.** Two parallel pipelines: OpenTelemetry (OTLP, configurable target) + ClearcutLogger (Google internal Clearcut analytics).

Key privacy decision: `logPrompts = false` by default. User prompts are **not sent to telemetry** unless `GEMINI_TELEMETRY_LOG_PROMPTS=true`.

**Dark corner:** Every turn-level API error unconditionally writes a JSON crash dump to `/tmp/gemini-client-error-<type>-<timestamp>.json` containing the full conversation history context. This is not gated by debug settings.

---

## 11. Source Transparency

| | Claude Code | Codex | Gemini CLI |
|---|---|---|---|
| Source form | ~11 MB minified single bundle | Rust binary + partial Node wrapper | Fully unminified TypeScript (Apache-2.0) |
| Can read tool implementations directly? | No | No (Rust) | Yes |
| Can read prompt text directly? | No | Partial | Yes |
| Can read policy logic directly? | No | Partial | Yes |
| Evidence | Source | Source | Source |

This asymmetry affects the confidence of every claim in this document. Gemini findings are the most reliable because they come from direct source reads. Claude Code and Codex findings come from bundle analysis, behavioral tests, and published research notes.

---

## Summary: Architectural Priorities

Reading each tool's architecture reveals what the team optimized for:

**Claude Code** optimized for product experience and experimentation velocity. The Bark loop is invisible by design — users don't manage it. 663+ remote flags mean the team can tune behavior without shipping. MCP proxied through Anthropic infrastructure means the team can observe and control tool traffic. The cost is opacity and variability.

**Codex** optimized for auditability and systems control. The explicit agent lifecycle (`spawn`/`resume`/`close`) is an API contract, not an implementation detail. SQLite-backed memory with ghost snapshots is designed for correctness under long sessions. The Rust core trades accessibility for performance and isolation guarantees.

**Gemini CLI** optimized for extensibility and inspectability. The typed turn state machine, 5-layer policy engine, full hook system, LLM-based model router, and 3-tier edit fallback all reflect a team that expected users and enterprises to build on top of the tool, audit its behavior, and customize its decisions. The tradeoff is complexity — Gemini has more moving parts than the other two, all of them visible.

---

## What Is Not Yet Measured

| Question | Required test |
|---|---|
| Memory correctness after compaction | Forced compaction + factual recall score across long sessions |
| Behavioral variance from remote flags | Same prompt × N runs on Claude Code, measure output diff |
| Edit quality under adversarial inputs | Malformed old_string, wrong indentation, mid-edit conflict |
| Gemini task completion on benchmarks | Supply `GEMINI_API_KEY` to benchmark runner |
| Sub-agent context handoff fidelity | Spawn sub-agent, verify parent state preserved correctly |
| LLM classifier routing accuracy | Query-type variation + log model choice vs expected |
| Crash dump exposure in CI | Confirm /tmp writes in sandboxed vs host Gemini runs |

---

## Source Pointers

### Claude Code
- Bundle analysis: `research/CLAUDE_REVERSE_REPORT.md` *(referenced in prior notes; not in this repo)*
- Behavioral observations: `research/benchmarks/runs/20260314-145850/claude/`

### Codex
- Architecture notes: referenced in prior notes; codex/ directory not present in this repo
- Behavioral observations: `research/benchmarks/runs/20260314-145850/codex/`

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

### Benchmarks
- [BENCHMARK_RESULTS.md](./BENCHMARK_RESULTS.md)
- Canonical run: `research/benchmarks/runs/20260314-145850/`
