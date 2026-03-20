# Claude Local Artifact Findings

This note records what is directly observable from a local Claude Code installation on this machine. It is narrower than the comparison docs: the goal here is to separate direct artifact evidence from architectural inference.

Date of inspection: 2026-03-14

Evidence labels:
- `Direct` — read directly from a local file, binary, or directory listing
- `Inferred` — engineering conclusion drawn from direct evidence

## 1. Artifacts Located

### Installed npm package

Direct:
- CLI entrypoint symlink:
  - `/home/andrii/.nvm/versions/node/v22.22.0/bin/claude`
- Resolved package path:
  - `/home/andrii/.nvm/versions/node/v22.22.0/lib/node_modules/@anthropic-ai/claude-code`
- Package metadata:
  - `name`: `@anthropic-ai/claude-code`
  - `version`: `2.1.63`
  - `bin.claude`: `cli.js`

Direct package files:
- `cli.js` — 12 MB minified JS bundle
- `tree-sitter.wasm` — 201 KB
- `tree-sitter-bash.wasm` — 1.4 MB
- `resvg.wasm` — 2.4 MB
- `README.md`
- `LICENSE.md`
- `sdk-tools.d.ts`

### VS Code / Antigravity extension artifacts

Direct:
- Native binary:
  - `/home/andrii/.antigravity/extensions/anthropic.claude-code-2.1.63/resources/native-binary/claude`
- Compressed native binary:
  - `/home/andrii/.antigravity/extensions/anthropic.claude-code-2.1.63/resources/native-binary/claude.zst`

Direct binary sizes:
- `claude` — 225 MB ELF x86-64 executable, not stripped
- `claude.zst` — 51 MB

### Local runtime state

Direct:
- User state directory:
  - `/home/andrii/.claude`

Observed contents:
- `settings.json`
- `projects/`
- `plans/`
- `telemetry/`
- `shell-snapshots/`
- `ide/`
- `debug/`
- `cache/`
- `mcp-needs-auth-cache.json`
- `.credentials.json`

## 2. Direct Evidence From the npm Bundle

### Syntax-aware parsing assets exist

Direct:
- The installed package ships:
  - `tree-sitter.wasm`
  - `tree-sitter-bash.wasm`

Direct:
- `cli.js` contains loader code for WebAssembly language loading and includes the string:
  - `Language.load failed: no language function found in WASM file`

Inferred:
- Claude Code is not limited to plain text file reads. The local bundle includes real parser infrastructure and at least one language grammar.

### Ripgrep is a first-class CLI path

Direct:
- `cli.js` contains an explicit CLI fast path for `--ripgrep`:
  - if first arg is `--ripgrep`, Claude dispatches to a separate `ripgrepMain(...)` path

Inferred:
- Ripgrep is not just an incidental dependency. The CLI has a dedicated execution path for it.

### Teammate mode is real, not just benchmark behavior

Direct:
- `cli.js` contains teammate-related argument handling and state fields, including:
  - `--team-name`
  - `agentId`
  - `agentName`
  - `teamName`
  - `teammateMode`
- `teammateMode` accepts values:
  - `auto`
  - `tmux`
  - `in-process`

Direct:
- `cli.js` contains in-process teammate execution strings such as:
  - `inProcessRunner`
  - `compacting history`

Inferred:
- The multi-agent / teammate capability is directly implemented in the product, including at least one in-process mode.

### Chrome / browser bridge support is real

Direct:
- `cli.js` contains command paths for:
  - `--claude-in-chrome-mcp`
  - `--chrome-native-host`
- `cli.js` also contains strings and code paths for:
  - `Claude in Chrome`
  - browser detection
  - native messaging paths

Inferred:
- Claude Code includes a browser bridge / native host integration path, not just terminal-only behavior.

### MCP is heavily integrated

Direct:
- `cli.js` contains many `mcp` references and CLI subcommands
- Local state also contains:
  - `/home/andrii/.claude/mcp-needs-auth-cache.json`

Inferred:
- MCP is part of the product’s normal runtime model, not a thin addon.

### Local settings and trust boundaries are explicit

Direct:
- `cli.js` references:
  - `.claude/settings.json`
  - `.claude/settings.local.json`
- The bundle contains sandbox / policy logic referencing allowed and denied paths, domains, and tool rules.

Inferred:
- Claude Code has a real local policy/settings layer and project-scoped behavior overrides.

## 3. Direct Evidence From the Native Binary

The native binary provides stronger evidence for the lower-level runtime than the JS bundle alone.

### Ripgrep is embedded deeply, with ignore support

Direct string hits from the binary include:
- `ripgrep`
- `/tmpfs/bun/embedded/ripgrep/crates/ignore/src/gitignore.rs`
- `/tmpfs/bun/embedded/ripgrep/crates/ignore/src/walk.rs`
- `/tmpfs/bun/embedded/ripgrep/crates/searcher/src/lines.rs`
- `/tmpfs/bun/embedded/ripgrep/crates/searcher/src/searcher/core.rs`
- `/tmpfs/bun/embedded/ripgrep/crates/printer/src/json.rs`
- `/tmpfs/bun/embedded/ripgrep/crates/printer/src/hyperlink.rs`

Inferred:
- The native runtime likely embeds ripgrep functionality directly, including gitignore-aware traversal, structured output, and hyperlink-capable printers.

### PTY support is directly evidenced

Direct string hit:
- `node-pty`

Inferred:
- Claude Code’s runtime stack includes PTY-oriented execution support, which fits the interactive terminal behavior we have observed previously.

### Tree-sitter coverage is broader than the npm package alone shows

Direct string hit:
- `tree-sitter-yaml`

Direct package assets already included:
- `tree-sitter.wasm`
- `tree-sitter-bash.wasm`

Inferred:
- The local system contains evidence of parser support beyond Bash alone. The native binary likely carries or references additional grammars or parsing paths not exposed as standalone npm package files.

## 4. Local State Model Signals

Direct:
- `~/.claude/projects/` exists and is populated
- `~/.claude/telemetry/` exists and is populated
- `~/.claude/shell-snapshots/` exists and is populated
- `~/.claude/debug/` exists and is populated
- `~/.claude/plans/` exists and is populated

Inferred:
- Claude Code maintains durable local project state, shell context snapshots, telemetry buffers, and debug logs. This supports the prior conclusion that it is a stateful coding agent, not just a stateless request wrapper.

## 5. What This Strengthens

These local artifacts materially strengthen several prior claims.

### Stronger now than before

- Syntax-aware code reading
  - Directly supported by shipped Tree-sitter assets and WASM loader code.
- Ignore-aware search
  - Directly supported by embedded ripgrep crate paths including `gitignore.rs`.
- PTY-style terminal execution
  - Directly supported by `node-pty` in the native binary.
- Multi-agent / teammate orchestration
  - Directly supported by teammate flags and in-process teammate strings in `cli.js`.
- MCP integration
  - Directly supported by command/code references and local auth cache state.

## 6. What Is Still Not Proven

This inspection improves confidence, but it does not prove every architectural detail.

Still unproven from this pass:
- the exact internal API boundaries between search, read, memory, and execution
- the full set of supported Tree-sitter grammars
- whether all interactive execution routes go through the same PTY path
- the exact compaction and memory data model on disk in this local install

## 7. Practical Takeaways For ProjectKitty

The strongest Claude patterns supported by direct local evidence are:
- syntax-aware reading is worth doing
- ripgrep + ignore-aware traversal is foundational
- PTY-backed execution is likely part of the serious runtime path
- teammate / sub-agent orchestration is real, but more complex than Article 2 should absorb
- local policy/settings and persistent state matter

The cleanest interpretation is:
- use Claude as evidence for parser depth and interactive runtime depth
- use Codex and Gemini as references for cleaner exposed tool/state boundaries

