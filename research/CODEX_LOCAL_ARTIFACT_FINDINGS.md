# Codex Local Artifact Findings

This note records what is directly observable from a local Codex installation on this machine. The aim is the same as the Claude artifact note: separate direct local evidence from architectural inference.

Date of inspection: 2026-03-14

Evidence labels:
- `Direct` — read directly from a local file, binary, DB schema, or directory listing
- `Inferred` — engineering conclusion drawn from direct evidence

## 1. Artifacts Located

### Installed npm package

Direct:
- CLI entrypoint symlink:
  - `/home/andrii/.nvm/versions/node/v22.22.0/bin/codex`
- Resolved package path:
  - `/home/andrii/.nvm/versions/node/v22.22.0/lib/node_modules/@openai/codex`
- Package metadata:
  - `name`: `@openai/codex`
  - `version`: `0.107.0`
  - `license`: `Apache-2.0`

Direct package files:
- `bin/codex.js`
- `bin/rg`
- `package.json`
- `README.md`

### Native binary artifact

Direct:
- Native binary inside the OpenAI extension:
  - `/home/andrii/.antigravity/extensions/openai.chatgpt-26.304.20706-linux-x64/bin/linux-x86_64/codex`

Direct binary facts:
- ELF 64-bit, static-pie, stripped
- size: 107 MB

### Local runtime state

Direct:
- User state directory:
  - `/home/andrii/.codex`

Observed contents:
- `state_5.sqlite`
- `state_5.sqlite-wal`
- `state_5.sqlite-shm`
- `session_index.jsonl`
- `sessions/`
- `memories/`
- `rules/default.rules`
- `auth.json`
- `models_cache.json`
- `config.toml`
- `shell_snapshots/`
- `tmp/`

## 2. Direct Evidence From the npm Package

### The npm package is a thin launcher, not the real runtime

Direct:
- `bin/codex.js` is a small Node wrapper that:
  - detects platform/arch
  - resolves an optional platform package such as `@openai/codex-linux-x64`
  - spawns the native `codex` binary
  - forwards `SIGINT`, `SIGTERM`, and `SIGHUP`

Inferred:
- The real implementation depth is in the native binary, not the Node entrypoint.

### Codex explicitly vendors ripgrep

Direct:
- `bin/rg` is a `dotslash` launcher for ripgrep `15.1.0`
- it contains platform-specific release metadata and upstream URLs for the ripgrep binaries

Inferred:
- Ripgrep is a first-class dependency in the shipped CLI, not something the user must install separately.

## 3. Direct Evidence From the Native Binary

The native binary is where the strongest architectural evidence lives.

### Tool names are directly present

Direct string hits include:
- `view_image`
- `request_user_input`
- `apply_patch`
- `read_file`

Direct source-path string hits include:
- `core/src/tools/handlers/read_file.rs`
- `core/src/tools/handlers/apply_patch.rs`
- `core/src/tools/handlers/view_image.rs`
- `core/src/tools/handlers/request_user_input.rs`
- `core/src/tools/runtimes/apply_patch.rs`
- `core/src/apply_patch.rs`

Inferred:
- Codex’s local build contains a typed tool surface with dedicated handlers, not one generic shell-only interface.

### Exec streaming is explicit

Direct string hits include event names:
- `exec_command_begin`
- `exec_command_output_delta`
- `exec_command_end`

Direct error strings include:
- `exec_command failed: `
- `write_stdin failed: `

Inferred:
- The runtime models command execution as a streaming lifecycle, not as a single opaque subprocess result.

### Bubblewrap sandboxing is directly evidenced

Direct string hits include:
- `bwrap`
- `bubblewrap built at codex build-time`
- `linux-sandbox/src/vendored_bwrap.rs`
- `error building bubblewrap command: `
- `failed to read bubblewrap stderr: `
- `failed to fork for bubblewrap: `

Direct UI/config strings include:
- `Bubblewrap sandbox`
- `use_linux_sandbox_bwrap`
- `Linux bubblewrap sandbox offers stronger filesystem and network controls than Landlock alone`

Inferred:
- Codex has a real Linux bubblewrap sandbox path and exposes it as an explicit runtime/config feature.

### Multi-agent support is directly evidenced

Direct string hits include:
- `subagent`
- `resumeAgent`
- `parent_thread_id`
- `receiver_thread_id`
- `sender_thread_id`
- `new_thread_id`
- `forked_from`
- `thread forked from`

Direct UI/config strings include:
- `Multi-agents`
- `spawn multiple agents`

Inferred:
- Codex has first-class thread/sub-agent orchestration and explicit parent/child thread relationships.

### Rollout/session persistence is directly evidenced

Direct string hits include:
- `rollout_path`
- `failed to load rollout`
- `Resuming rollout from `
- `Resumed rollout successfully from `
- `state db find_rollout_path_by_id failed`
- `cannot resume running thread`

Inferred:
- Codex persists conversations as rollouts and can resume them by thread/session identity.

### SQLite-backed state is directly evidenced

Direct string hits include:
- `sqlite`
- `sqlx-sqlite`
- `state db`

Direct source-path string hits include:
- `sqlx-sqlite-0.8.6/...`
- `core/src/codex/rollout_reconstruction.rs`
- `core/src/rollout/session_index.rs`
- `core/src/rollout/recorder.rs`

Inferred:
- SQLite is part of the normal state layer, not just a cache or optional debug artifact.

## 4. Direct Evidence From Local State

### SQLite schema shows thread-centric durable state

Direct:
- `~/.codex/state_5.sqlite` contains tables:
  - `_sqlx_migrations`
  - `threads`
  - `thread_dynamic_tools`
  - `logs`
  - `stage1_outputs`
  - `agent_jobs`
  - `agent_job_items`
  - `jobs`
  - `backfill_state`

Direct:
- `threads` schema includes:
  - `id`
  - `rollout_path`
  - `source`
  - `model_provider`
  - `cwd`
  - `sandbox_policy`
  - `approval_mode`
  - `git_sha`
  - `git_branch`
  - `git_origin_url`
  - `memory_mode`

Direct:
- `stage1_outputs` schema includes:
  - `raw_memory`
  - `rollout_summary`
  - `selected_for_phase2`

Inferred:
- The local state model matches the “SQLite + rollout + staged memory” direction from prior Codex research very closely.

### Session files are JSONL rollouts

Direct:
- `~/.codex/sessions/YYYY/MM/DD/` contains files named like:
  - `rollout-2026-03-14T11-49-01-019cebf6-bc2a-7a22-b1c6-739f1ec9c1da.jsonl`

Direct:
- A sampled rollout file starts with a `session_meta` JSON object including:
  - `id`
  - `cwd`
  - `originator`
  - `cli_version`
  - `source`
  - `model_provider`
  - `git.commit_hash`
  - `git.branch`
  - `git.repository_url`

Inferred:
- Rollouts are durable structured transcripts, not just temp logs.

### Dynamic tools are persisted per thread

Direct:
- `thread_dynamic_tools` schema includes:
  - `thread_id`
  - `position`
  - `name`
  - `description`
  - `input_schema`

Inferred:
- Tool availability can be customized or extended at thread scope, then persisted.

### Agent jobs are persisted separately from threads

Direct:
- `agent_jobs` and `agent_job_items` tables exist
- schemas include:
  - `instruction`
  - `output_schema_json`
  - `input_csv_path`
  - `output_csv_path`
  - `assigned_thread_id`
  - `result_json`

Inferred:
- Codex has a batch/agent-job layer beyond simple single-thread chat sessions.

## 5. Additional Local Runtime Signals

### Sandbox helper paths are materialized under `~/.codex/tmp`

Direct:
- `~/.codex/tmp/arg0/.../codex-linux-sandbox`
- `~/.codex/tmp/arg0/.../codex-execve-wrapper`

Direct:
- sampled helper paths are symlinks to the same native Codex binary

Inferred:
- The native binary likely serves multiple execution roles via argv/path-based dispatch or internal mode switching.

### Shell snapshot persistence exists

Direct:
- `~/.codex/shell_snapshots/` exists and is populated

Inferred:
- Codex stores shell environment context as durable local state, not only in-memory per turn.

## 6. What This Strengthens

These local artifacts materially strengthen several prior Codex claims.

### Stronger now than before

- Codex is a native-binary-first runtime with only a thin JS launcher
- typed tools are real and directly implemented
- command execution is streamed as structured events
- bubblewrap sandboxing is real and productized
- SQLite-backed thread state and rollout persistence are real
- sub-agent / thread-fork orchestration is real
- ripgrep is bundled as a first-class tool dependency

## 7. What Is Still Not Proven

This pass improves confidence, but it does not prove everything.

Still unproven from this pass:
- the exact public/internal boundary between CLI, app-server, and native runtime layers
- whether every exec path uses the same sandbox implementation
- the full behavior of memory phase 2 consolidation from artifacts alone
- the exact semantics of every agent job mode

## 8. Practical Takeaways For ProjectKitty

The strongest Codex patterns supported by direct local evidence are:
- keep the external interface clean and typed
- treat ripgrep as foundational infrastructure
- make sandbox policy explicit and inspectable
- persist thread/session state in a structured DB, not only loose files
- treat rollouts as durable resumable session artifacts
- separate thread state, dynamic tools, memory outputs, and batch job state

The cleanest interpretation is:
- Claude still gives clearer parser-depth evidence
- Codex gives stronger direct evidence for clean runtime/tool boundaries, rollout persistence, SQLite state, and explicit sandboxing

