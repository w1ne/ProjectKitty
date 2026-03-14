# Claude Code vs Codex: Comparative Findings

This note summarizes the main findings from the Claude Code reverse-engineering report and the Codex research notes in this repository.

The goal is not to crown a winner. The goal is to identify architectural differences, state which claims are directly supported by evidence, and mark which comparisons still require benchmarking.

## Scope

- Claude Code evidence comes from [CLAUDE_REVERSE_REPORT.md](./CLAUDE_REVERSE_REPORT.md).
- Codex evidence comes from the files under [codex/](./codex/).
- `Observed` means the claim is directly supported by the documents in this repository.
- `Hypothesis` means the claim is an engineering inference that still needs testing.
- We should not treat hypotheses as product facts until they are measured.

## Comparison Table

| Area | Claude Code | Codex | Status | Current Conclusion |
|---|---|---|---|
| Memory model | JSONL session history plus aggressive compaction into `MEMORY.md` | Two-phase memory writers, ghost snapshot compaction, SQLite-backed state DB | Observed | The memory architectures are materially different. Whether one preserves correctness better still needs measurement. |
| Context recovery | Full transcript remains on disk and may need to be reread after compaction | Ghosted history preserves re-hydration potential within a structured memory pipeline | Observed | Claude and Codex use different recovery strategies. Reliability under long sessions is still unmeasured. |
| Agent architecture | Internal Bark loop plus teammate routines and summarizer sub-agents | Explicit multi-agent lifecycle with `spawn_agent`, `resume_agent`, `close_agent`, and `send_message` | Observed | Codex exposes more explicit agent lifecycle primitives in the research notes. |
| Context handoff | Isolated sub-contexts report key findings back to main memory | Context forking can preserve parent history for sub-agents | Hypothesis | Handoff fidelity should be tested with controlled multi-agent tasks before claiming one is better. |
| Safety model | Custom `SandboxedBash` with heuristic misparse checks and environment sterilization | `bubblewrap` sandbox, explicit policies, approval modes, and host/sandbox boundary controls | Observed | Codex documents a more explicit sandbox model; Claude Code documents a more heuristic shell-safety layer. |
| Shell usability | Security checks target chaining and shell expansion patterns | Shell and PTY tools are first-class inside explicit sandbox and approval policy | Hypothesis | Power-user shell ergonomics need benchmark tasks, not inference. |
| Runtime foundation | Node.js bundle with custom PTY and orchestration layers | Rust core, dedicated exec layer, PTY tools, structured IPC | Observed | The implementation stacks are materially different. Stability and performance need tests. |
| Telemetry | Broad telemetry, project hashing, git context, environment targeting, remote experimentation | OpenTelemetry-based observability with rollout serialization and diagnostics | Observed | Claude Code exposes a broader telemetry and experimentation surface in the current research. |
| Feature flags | 663+ remotely controlled flags plus local overrides | Smaller surfaced flag set in the research notes | Observed | Claude Code appears more heavily feature-flagged in the available evidence. |
| Model routing | Tiered fallback among Sonnet, Opus, and Haiku with automatic escalation | No equally prominent dynamic fallback system surfaced in the notes | Observed | Claude Code clearly documents fallback behavior; Codex notes do not surface an equivalent mechanism here. |
| MCP integration | Tool traffic is proxied through Anthropic infrastructure | Native MCP client support is indicated in the architecture notes | Observed | Claude Code introduces an extra proxy boundary in the observed architecture. |
| Local instruction control | Internal prompts and product modes dominate the report | Explicit `AGENTS.md` hierarchy and skill system | Observed | Codex exposes more repository-local instruction machinery in the current notes. |

## Evidence-Based Findings

### 1. Claude Code is weaker on long-session memory reliability

Observed:
Claude Code relies on aggressive session compaction and `MEMORY.md` updates to keep context within budget. Codex uses phase-based consolidation, ghost snapshots, and a SQLite state database.

Hypothesis:
Claude Code may be more vulnerable to summary drift in long sessions.

Required measurement:
- Run the same long-horizon task in both systems.
- Force at least one compaction event.
- Measure retained factual correctness after compaction.
- Measure how often the agent must reread prior transcript to recover lost detail.

### 2. Claude Code is weaker on reproducibility

Observed:
The reverse-engineering report surfaced more than 600 remote feature flags and explicit model fallback logic in Claude Code. The Codex notes surface a smaller flag set and more explicit local control primitives.

Hypothesis:
Claude Code may be less reproducible across runs because more behavior is remotely steerable.

Required measurement:
- Repeat identical tasks across multiple runs and machines.
- Record tool-selection variance, edit variance, and completion variance.
- Compare results with flags fixed where possible.

### 3. Claude Code is weaker on privacy and trust posture

Observed:
The Claude Code report shows a broad telemetry and experimentation stack, including project path hashing, git metadata, environment targeting, and cloud/environment fingerprinting. The Codex notes describe observability, but the current evidence surfaces a narrower set of details.

Current conclusion:
Claude Code has the larger exposed trust surface in the current research set.

### 4. Codex appears stronger on explicit safety boundaries

Observed:
Claude Code wraps shell execution in a custom `SandboxedBash` layer with heuristic checks for command chaining and misparsing. Codex documents `bubblewrap`, approval levels, policy names, and host/sandbox socket boundaries.

Current conclusion:
Codex documents a more explicit sandbox and approval model in the current notes.

### 5. Claude Code appears more productized, Codex more infrastructural

Observed:
Claude Code shows orchestration modes, teammate routines, auto-format hooks, dynamic reasoning effort, and model fallback tiers.

Observed:
Codex shows split-process design, Rust execution core, persistent state, explicit agent lifecycle, and repository-local instruction systems.

Hypothesis:
Claude Code may optimize for product smoothness, while Codex may optimize for architectural control, auditability, and operational governability.

Required measurement:
- Time-to-first-useful-action
- Task completion rate
- User intervention count
- Auditability of why a given action happened
- Failure isolation when orchestration or tool execution breaks

### 6. Both systems are complex, but in different ways

Observed:
Neither system is simple. Claude Code's visible complexity centers on orchestration and experimentation. Codex's visible complexity centers on infrastructure and explicit control surfaces.

Current conclusion:
The systems are complex in different layers. Claims about which architecture is easier to govern, audit, and operate still require empirical testing.

## Bottom Line

If the question is what the current research already supports, the strongest answers are:

- The memory architectures are different.
- Claude Code exposes more telemetry and feature-flag machinery in the current research.
- Codex exposes a more explicit sandbox and local-instruction model in the current research.
- Claude Code exposes more explicit model fallback behavior in the current research.

If the question is what still needs measurement, the key items are:

- Long-session memory correctness after compaction
- Reproducibility across repeated runs
- Shell ergonomics under safety controls
- Task completion quality and intervention rate
- Performance and runtime stability

## Proposed Test Matrix

We should benchmark both systems on the same tasks and record outputs in a reproducible format.

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

Observed blocker:

- The Codex harness passed sandbox configuration incorrectly for `codex exec`.
- As a result, at least one writable Codex benchmark (`shell_workflow`) executed under `read-only` sandbox and could not create its output file.

Current conclusion:

- The Claude results from that run are usable as raw artifacts.
- The Codex results from that run should be treated as partially invalid for benchmark comparison.
- We need a corrected Codex rerun before drawing any benchmark-based conclusions.

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
