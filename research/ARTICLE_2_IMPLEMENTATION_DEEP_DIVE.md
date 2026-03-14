# Article 2 Deep Dive: Proven Implementation Signals

This document goes deeper than the comparison notes. Its purpose is narrow:

> For the Article 2 problem space, what is actually proven in Claude, Codex, and Gemini implementations, and what should ProjectKitty copy as architecture rather than guess?

Article 2 scope:
- repository discovery
- search / narrowing
- symbol or code-item extraction
- focused code reads
- visible tool/state boundaries around those operations

Date: 2026-03-14

Evidence labels:
- `Direct` — observed from local binaries, bundles, schemas, or files in this workspace
- `Source` — stated in readable local research source docs
- `Inferred` — engineering interpretation

## 1. The Real Decision Surface

There are four independent architecture choices inside Article 2:

1. file discovery
2. search / narrowing
3. structural code extraction
4. tool/state shape exposed to the planner

The mistake is trying to choose one product as the winner for all four.

The research does not support that.

## 2. Claude: What Is Actually Proven

### 2.1 Search and file discovery

Direct:
- `cli.js` contains an explicit `--ripgrep` fast path that dispatches to `ripgrepMain(...)`
- the native binary contains embedded ripgrep paths:
  - `crates/ignore/src/gitignore.rs`
  - `crates/ignore/src/walk.rs`
  - `crates/searcher/src/searcher/core.rs`
  - `crates/printer/src/json.rs`
  - `crates/printer/src/hyperlink.rs`

What this proves:
- Claude has a real ripgrep-backed search path
- Claude has ignore-aware traversal
- Claude does not rely only on ad hoc JS file scanning

What this does **not** prove:
- the exact public tool interface presented to the model for file discovery
- whether discovery is exposed as separate `glob` / `grep` tools

### 2.2 Structural code extraction

Direct:
- local package ships:
  - `tree-sitter.wasm`
  - `tree-sitter-bash.wasm`
- `cli.js` contains WASM language loading code and the string:
  - `Language.load failed: no language function found in WASM file`
- native binary contains:
  - `tree-sitter-yaml`

What this proves:
- Claude definitely includes syntax-aware parsing infrastructure
- the parser path is grammar-backed, not just regex
- the parser coverage is broader than a single Bash grammar

What this does **not** prove:
- the exact code-item API shape used internally
- the exact list of all supported grammars
- the exact AST query or symbol extraction implementation

### 2.3 Tool/state shape

Direct:
- teammate and in-process orchestration strings exist:
  - `teammateMode`
  - `inProcessRunner`
- there is no equally direct local evidence of a clean typed `Search` / `Outline` / `ReadSymbol` API in the public sense

What this proves:
- Claude likely has substantial internal orchestration around retrieval

What this does **not** prove:
- that Claude exposes cleaner planner-facing retrieval tools than Codex or Gemini

### Claude verdict for Article 2

Claude is the strongest implementation evidence for:
- parser depth
- syntax-aware code extraction
- ripgrep + ignore-aware narrowing

Claude is **not** the strongest evidence for:
- explicit clean tool boundaries
- inspectable retrieval state shape

## 3. Codex: What Is Actually Proven

### 3.1 Search and file discovery

Direct:
- npm package ships a dedicated `bin/rg`
- `bin/rg` is a platform-aware launcher for ripgrep `15.1.0`
- native binary contains:
  - `core/src/tools/handlers/grep_files.rs`
  - `fuzzyFileSearch/sessionUpdated`
  - `fuzzyFileSearch/sessionCompleted`
  - `failed to start fuzzy file search session`
  - `invalid glob pattern`
  - `globset-0.4.18/...`

What this proves:
- Codex has a real search subsystem beyond simple single-file read
- ripgrep is first-class and bundled
- Codex has both grep-style search and fuzzy file search concepts
- glob parsing is part of the implementation

What this does **not** prove:
- the exact user-facing tool names for every search primitive
- whether fuzzy file search is always model-invocable or partly UI/runtime-managed

### 3.2 Structural code extraction

Direct:
- native binary directly evidences `read_file`
- no direct local evidence of Tree-sitter or another syntax parser was found in Codex artifacts during this pass

What this proves:
- Codex definitely has explicit read primitives

What this does **not** prove:
- that Codex has Claude-level parser depth for symbol extraction
- that Codex uses Tree-sitter specifically

### 3.3 Tool/state shape

Direct:
- native binary contains typed tool/runtime names:
  - `read_file`
  - `apply_patch`
  - `request_user_input`
  - `view_image`
- native binary contains streamed exec lifecycle events:
  - `exec_command_begin`
  - `exec_command_output_delta`
  - `exec_command_end`
- local SQLite state contains:
  - `threads.rollout_path`
  - `threads.sandbox_policy`
  - `threads.approval_mode`
  - `threads.memory_mode`
  - `thread_dynamic_tools`
- session rollouts are JSONL and begin with `session_meta`

What this proves:
- Codex has explicit typed tool identities
- Codex has explicit runtime lifecycle states
- Codex persists structured thread state, not only flat transcripts
- Codex’s architecture is cleaner and more inspectable around tool boundaries

### Codex verdict for Article 2

Codex is the strongest implementation evidence for:
- explicit tool boundaries
- search/read/runtime separation
- structured state around threads, rollouts, and tools
- treating retrieval as part of a typed runtime instead of a hidden blob

Codex is **not** the strongest evidence for:
- deep syntax-aware symbol extraction

## 4. Gemini: What Is Actually Proven

Gemini is different because the strongest evidence comes from readable source-based research rather than local binary reverse engineering.

### 4.1 Search and file discovery

Source:
- built-in file/discovery tools include:
  - `GlobTool`
  - `LSTool`
  - `GrepTool`
  - `RipGrepTool`
  - `ReadFileTool`
  - `ReadManyFilesTool`

What this proves:
- Gemini has the clearest explicit separation of discovery and read tools
- file discovery and search are first-class tool concepts

### 4.2 Structural code extraction

Source:
- available evidence strongly covers file discovery and reading
- current local research does not prove Claude-like syntax-aware AST extraction depth for Gemini

What this proves:
- Gemini is strong on discovery/read tool organization

What this does **not** prove:
- deep code-item or symbol extraction equal to Claude

### 4.3 Tool/state shape

Source:
- typed event/state loop includes:
  - `ToolCallRequest`
  - `Finished`
  - `ContextWindowWillOverflow`
  - `LoopDetected`
- non-interactive output supports:
  - `text`
  - `json`
  - `stream-json`

What this proves:
- Gemini is the strongest reference for observable staged execution
- Gemini’s loop and tool activity are deliberately inspectable

### Gemini verdict for Article 2

Gemini is the strongest implementation evidence for:
- visible staged execution
- explicit discovery tool catalog
- machine-readable output around tool usage

Gemini is **not** the strongest evidence for:
- parser-backed structural code extraction depth

## 5. Side-By-Side: What Is Proven Best By Which Product

### File discovery

Best proven interface:
- Gemini

Reason:
- explicit `glob`, `ls`, `grep`, `ripgrep`, `read_file`, `read_many_files`

### Search / narrowing

Best proven runtime depth:
- Claude and Codex, for different reasons

Claude proves:
- ripgrep + ignore-aware traversal

Codex proves:
- bundled ripgrep + fuzzy file search + grep handler + glob handling

Practical winner for ProjectKitty:
- Codex/Gemini shape
- Claude/Codex search backend choices

### Structural symbol / code-item extraction

Best proven depth:
- Claude

Reason:
- direct Tree-sitter evidence
- multiple grammar signals

### Visible planner-facing retrieval stages

Best proven shape:
- Codex and Gemini

Codex proves:
- typed runtime/tool boundaries

Gemini proves:
- explicit discovery/read tool catalog and typed event stream

## 6. The Architecture We Can Choose Without Guessing

If the standard is “choose based on the strongest proven implementation signal,” then the answer is:

### For internals

Copy Claude:
- ripgrep-first narrowing
- ignore-aware traversal
- syntax-aware parser-backed code extraction
- one structural reading engine with multiple grammars

### For public tool shape

Copy Codex/Gemini:
- explicit `Search`
- explicit `Outline`
- explicit `ReadSymbol` or `ReadCodeItem`
- explicit no-match state

### For state/event shape

Copy Codex/Gemini:
- typed stage transitions
- observable retrieval events
- tool identity preserved through the loop

That is not a compromise born of uncertainty. It is the only architecture fully supported by the current implementation evidence.

## 7. What We Should Stop Saying

Based on the deeper implementation evidence, these weak claims should be avoided:

- “Claude probably has cleaner tool boundaries”
  - not proven locally
- “Codex probably has the same syntax-aware depth as Claude”
  - not proven locally
- “Gemini probably has comparable code-item extraction”
  - not proven locally
- “one product should be copied wholesale for Article 2”
  - not supported by the evidence

## 8. The Strongest Defensible Article 2 Target For Whiskers

The strongest defensible target is:

```text
Search -> Outline -> ReadCodeItem
```

with the following provenance:

- `Search`
  - Gemini/Codex interface shape
  - Claude/Codex backend behavior

- `Outline`
  - Claude parser depth
  - Codex-style typed result boundary

- `ReadCodeItem`
  - Claude structural extraction direction
  - Codex/Gemini explicit tool identity

- `NoStrongMatch`
  - Codex/Gemini-style explicit state modeling

## 9. Final Decision

If the goal is to copy the best proven architecture for Article 2 without guessing:

- choose Claude for the internal reader
- choose Codex for the runtime/tool contract
- choose Gemini for observability and explicit discovery staging

That is the architecture to copy.

