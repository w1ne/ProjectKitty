# Article 2 Research: Code Reading Architecture

This note narrows the broader Claude/Codex/Gemini research to one question:

> How should ProjectKitty's Article 2 code-reading subsystem be shaped if we want to copy the strongest architecture without dragging in unnecessary complexity?

This is not a generic comparison document. It is a design note for Whiskers.

Date: 2026-03-14

Evidence labels:
- `Direct` — visible in local artifacts or readable source
- `Source` — already documented in local research from readable source or reverse engineering
- `Inferred` — engineering conclusion, not fully proven

## 1. The Core Problem Article 2 Must Solve

A coding agent needs a retrieval layer that answers four questions cheaply and reliably:

1. which files are probably relevant
2. which symbols inside those files matter
3. which exact code item should be read next
4. when there is no strong match, how to refuse instead of pretending

That is the whole Article 2 contract.

If this layer is weak:
- the planner reads too much
- execution starts from the wrong code
- validation runs against bad assumptions
- the model looks worse than it is

## 2. What The Research Clearly Supports

### Claude

Strongest Article 2 signals:
- syntax-aware parsing infrastructure
- dedicated search path using ripgrep
- ignore-aware repository traversal

Evidence:
- local package ships `tree-sitter.wasm` and `tree-sitter-bash.wasm` [Direct]
- native binary contains `tree-sitter-yaml` [Direct]
- `cli.js` contains an explicit `--ripgrep` path [Direct]
- native binary contains embedded ripgrep crate paths including `ignore/src/gitignore.rs` [Direct]

Interpretation:
- Claude is the clearest evidence for structural code reading depth
- Claude strongly supports "search first, parse selectively"
- Claude supports one parser framework with multiple grammars more than bespoke parser logic per language

### Codex

Strongest Article 2 signals:
- typed tool boundaries
- rollout- and thread-aware persistence
- explicit runtime/tool separation

Evidence:
- native binary contains direct tool/runtime names: `read_file`, `apply_patch`, `request_user_input`, `view_image` [Direct]
- native binary contains streamed exec event names: `exec_command_begin`, `exec_command_output_delta`, `exec_command_end` [Direct]
- local SQLite state stores `thread_id`, `rollout_path`, `sandbox_policy`, `approval_mode`, `memory_mode`, `git_branch` [Direct]
- session files are JSONL rollouts with structured `session_meta` [Direct]

Interpretation:
- Codex is the clearest evidence for keeping search/read/runtime layers explicit
- Codex strongly supports modeling retrieval as typed tools and typed state, not one opaque “scan”
- Codex is less useful than Claude for parser-depth decisions, but more useful for interface cleanliness

### Gemini

Strongest Article 2 signals:
- explicit file discovery tools
- explicit search tools
- observable staged execution

Evidence:
- built-in tools include `glob`, `ls`, `grep`, `ripgrep`, `read_file`, `read_many_files` [Source]
- turn loop exposes typed events and tool calls [Source]
- tool scheduling and confirmation are explicit [Source]

Interpretation:
- Gemini strongly supports separate file-discovery and file-read tools
- Gemini supports inspectable staged behavior instead of one hidden retrieval phase
- Gemini is the best reference for “make the stages visible,” not for parser depth

## 3. The Best Hybrid Architecture For Whiskers

If we combine only the strongest Article 2 lessons, the target architecture is:

```text
Search -> Outline -> Read Symbol
```

with these properties:

### 1. `Search` is cheap and broad

Responsibilities:
- use repository-aware discovery first
- use ignore-aware search
- return file candidates, scores, and provider metadata
- stop before parsing full files unnecessarily

Best supporting references:
- Claude for ripgrep + ignore-aware search
- Gemini for explicit `glob` / `grep` / `ripgrep` tool split
- Codex for making this a typed tool stage

### 2. `Outline` is structural and selective

Responsibilities:
- parse candidate files only after search has narrowed them
- extract top-level symbols / code items
- return symbol metadata, not whole-file blobs
- score symbols structurally, not mainly by body-text overlap

Best supporting references:
- Claude for syntax-aware parsing depth
- Codex for typed `read_file`-style boundaries around the result

### 3. `Read Symbol` is exact and small

Responsibilities:
- read one symbol or code item
- return exact semantic bounds where possible
- use the same structural reader as `Outline`
- never use a totally different extraction path than the ranking stage

Best supporting references:
- Claude for structural code-item reading
- Codex for explicit tool identity

### 4. `No Strong Match` is a first-class outcome

Responsibilities:
- refuse weak symbol reads
- return structured no-match status
- let the planner decide whether to broaden, validate anyway, or ask for clarification

Best supporting references:
- this is more an architectural consequence than a directly named tool in the artifacts
- it is consistent with Codex/Gemini’s explicit state modeling

## 4. What We Should Copy Exactly

These are the Article 2 patterns worth copying with minimal reinterpretation.

### Copy from Claude

- use ripgrep or equivalent as the first narrowing pass
- respect ignore files instead of product-specific hardcoded exclusions
- use a single syntax-aware reading engine with per-language grammars
- parse only narrowed candidates, not the whole repo

### Copy from Codex

- make search, outline, and read explicit typed stages
- persist retrieval state in structured form, not only prose summaries
- keep the runtime/tool boundary clean
- treat the external interface as stable even if internals improve later

### Copy from Gemini

- keep `glob`/`grep`/`read` style operations conceptually separate
- expose retrieval stages in observable event/state output
- keep non-interactive output inspectable and machine-friendly

## 5. What We Should Not Copy Yet

These are real patterns in the research, but they do not belong in the current Article 2 slice.

### Not yet from Claude

- hidden orchestration complexity
- teammate/sub-agent expansion inside the retrieval layer
- broader runtime behaviors unrelated to code reading

### Not yet from Codex

- full SQLite rollout/state model inside Whiskers itself
- batch agent-job machinery
- execution/runtime concerns that belong to later articles

### Not yet from Gemini

- full policy engine
- in-tool correction loops
- broad UX/state-machine depth outside retrieval

## 6. Minimal Architecture We Can Defend

If we want the simplest defensible Article 2 architecture, it should look like this:

```text
Task
  -> Search(task, workspace)
      returns candidate files + provider + confidence
  -> Outline(candidates, task)
      returns symbols + related files + best match confidence
  -> ReadSymbol(bestMatch)
      returns exact code item
  -> NoMatch
      if confidence is below threshold
```

Data returned by each stage should be typed.

Suggested shape:

### `SearchResult`
- `provider`
- `passes`
- `candidate_files`
- `summary`

### `OutlineResult`
- `symbols`
- `best_symbol`
- `related_files`
- `confidence`
- `summary`

### `ReadSymbolResult`
- `file`
- `symbol`
- `language`
- `snippet`
- `summary`

### `NoMatchResult`
- `reason`
- `candidate_files`
- `summary`

This is close to Codex/Gemini cleanliness and compatible with Claude-style parser depth.

## 7. Parser Strategy For Article 2

The research does **not** support “one magic universal parser.”

What it supports is:
- one structural reading interface
- one parser framework when possible
- multiple language grammars underneath it

That means the right internal strategy is:

- one public reader interface
- language detection
- Tree-sitter-backed parsing for supported languages
- fallback path for unsupported files

This is Claude-aligned and still operationally simple.

What we should avoid:
- custom parser logic per language as separate architecture concepts
- language-specific public tools
- regex ranking as the primary strategy once structural parsing is available

## 8. Ranking Strategy For Article 2

The retrieval stack should rank in this order of trust:

1. path / filename evidence
2. symbol-name evidence
3. declaration-level structural evidence
4. nearby related-file evidence
5. body-text token overlap

That ordering is important.

Why:
- Claude evidence suggests structural reading depth matters more than raw text search
- Codex evidence suggests explicit, reliable tool boundaries matter more than clever hidden heuristics
- Gemini evidence suggests the stages should be inspectable, so ranking should be explainable

So Whiskers should not behave like “semantic search.” It should behave like:
- cheap narrowing
- structural outlining
- exact focused read

## 9. The Architecture We Should Actually Copy

If we compress the research into one concrete recommendation, it is this:

### Public shape

Use a Codex/Gemini-style explicit interface:
- `Search`
- `Outline`
- `ReadSymbol`

### Internal depth

Use a Claude-style reader:
- Tree-sitter-backed where supported
- one engine, many grammars
- exact code-item extraction

### Execution behavior

Use Gemini-style observability:
- emit separate search / outline / read events
- expose provider and confidence
- expose no-match explicitly

That is the cleanest “copy the architecture” answer from the current research.

## 10. What This Means For ProjectKitty

For Whiskers, the research-backed target is:

- `Search` should stay cheap, ignore-aware, and repo-first
- `Outline` should be the main structural ranking step
- `ReadSymbol` should use the same structural engine as `Outline`
- weak matches should terminate in `No strong symbol match`
- typed tool state should be part of the agent state, not hidden in prose
- parser depth should improve internally without changing the external tool contract

In short:

- copy Claude for structural code reading
- copy Codex for tool boundaries and typed state
- copy Gemini for visible staged execution

That is the Article 2 architecture to copy.

