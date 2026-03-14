# Gemini CLI Tools

> **Dark corners**: EditTool has a 3-tier fallback replacement strategy; WriteFileTool runs an LLM correction pass before writing; CoreToolScheduler uses a WeakMap to prevent duplicate MessageBus subscriptions across React re-renders.

## Built-in Tools (17 in v0.27.3; 20+ as of v0.29.0)

### File System
| Tool | Class | Notes |
|---|---|---|
| `read_file` | `ReadFileTool` | Single file read |
| `read_many_files` | `ReadManyFilesTool` | Batch file read |
| `write_file` | `WriteFileTool` | Create or overwrite files |
| `edit` | `EditTool` | Diff-based patch editing (uses `diff` npm library + `DEFAULT_DIFF_OPTIONS`) |
| `glob` | `GlobTool` | Pattern-based file discovery |
| `ls` | `LSTool` | Directory listing |
| `grep` | `GrepTool` | Pattern search (pure JS fallback) |
| `ripgrep` | `RipGrepTool` | Preferred grep using ripgrep binary if found; logs `RipgrepFallbackEvent` on missing |

### Execution
| Tool | Class | Notes |
|---|---|---|
| `run_shell_command` | `ShellTool` | Full shell execution; wraps with `pgrep` on Linux/macOS for PID tracking; inactivity timeout |

### Web
| Tool | Class | Notes |
|---|---|---|
| `web_fetch` | `WebFetchTool` | HTTP GET/POST request |
| `web_search` | `WebSearchTool` | Web search integration |

### Memory & Planning
| Tool | Class | Notes |
|---|---|---|
| `save_memory` | `MemoryTool` | Appends facts to `GEMINI.md` (global and/or project) |
| `write_todos` | `WriteTodosTool` | Maintains a markdown to-do file |

### Interactive & Plan Mode *(added v0.28.0–v0.29.0)*
| Tool | Class | Notes |
|---|---|---|
| `ask_user` | `AskUserTool` | Interactive dialog: labeled options, multi-select, multi-line text, markdown rendering |
| `enter_plan_mode` | `EnterPlanModeTool` | Programmatically enter Plan Mode from within a session |
| `exit_plan_mode` | `ExitPlanModeTool` | Signal end of plan mode; transitions back to normal mode |

### Agent & Extension
| Tool | Class | Notes |
|---|---|---|
| `<agent-name>` | `SubagentTool` | Wraps a registered agent as a callable tool |
| `<serverName>__<toolName>` | `McpTool` | Dynamically discovered MCP server tools |
| `activate_skill` | `ActivateSkillTool` | Loads a named skill from `.gemini/skills/` |
| `get_internal_docs` | `GetInternalDocsTool` | Access CLI internal documentation |

> **Total tool count**: Originally 17 (v0.27.3). Now **20+** with `ask_user`, `enter_plan_mode`, and `exit_plan_mode`.

## EditTool: 3-Tier Replacement Strategy

This is one of the most complex hidden behaviors in Gemini CLI. When the model provides an `old_string` to replace, the edit tool tries three strategies in sequence:

### Tier 1 — Exact Match
- CRLF-normalized, counts occurrences of `old_string` verbatim.
- Uses `safeLiteralReplace()` which protects against `$` substitution sequences.
- If ≥1 exact match found: apply and stop.

### Tier 2 — Flexible / Indentation-Aware Match
- All lines are `.trim()`-stripped, then matched window-by-window.
- If match found, the indentation of the **first matched line** is applied to the entire replacement block.
- This handles cases where the model guesses wrong indentation.

### Tier 3 — Regex Match with Token Flexibility
- Tokenizes `old_string` on whitespace + delimiters `( ) : [ ] { } > < =`.
- Joins tokens with `\s*` to create a flexible whitespace-insensitive pattern.
- Captures leading indentation and re-applies it to replacement.
- Logs `EditStrategyEvent` to telemetry.

All tiers:
- Normalize CRLF to LF before matching.
- Call `restoreTrailingNewline()` to preserve the file's original trailing newline behavior.
- Use `SHA256` hash of file content to detect mid-edit conflicts.

If all three tiers fail, `FixLLMEditWithInstruction` is invoked — a **4th LLM call** that asks the model to repair its own malformed edit. This triggers `EditCorrectionEvent` in telemetry.

## WriteFileTool: LLM Correction Pass

Before writing, `getCorrectedFileContent()` runs:
1. Reads the existing file (if present).
2. For **existing files**: calls `ensureCorrectEdit()` — an LLM call that validates and fixes the proposed content against the current file.
3. For **new files**: calls `ensureCorrectFileContent()` — an LLM call that validates the content.
4. Both can be disabled with `config.getDisableLLMCorrection()`.

This means **every file write can silently trigger an extra LLM API call** to self-validate content. Telemetry: `logFileOperation()` + `FileOperationEvent`.

WriteFileTool also:
- Detects MIME type: `getSpecificMimeType()` for binary file safety.
- Detects language: `getLanguageFromFilePath()` for syntax-aware correction.
- Integrates with `IdeClient` to notify IDE of file changes.

## CoreToolScheduler: WeakMap Deduplication

The scheduler uses a `static WeakMap` (`CoreToolScheduler.subscribedMessageBuses`) to ensure it subscribes to each `MessageBus` only once, regardless of how many scheduler instances are created (e.g., on every React render cycle).

This prevents memory leaks and duplicate confirmations. The WeakMap allows the MessageBus to be garbage collected when abandoned.

When `PolicyDecision.ASK_USER` is returned:
- The scheduler publishes `TOOL_CONFIRMATION_RESPONSE` with `requiresUserConfirmation: true`.
- This delegates back to the legacy in-tool confirmation flow.

## Output Formats

Three output formats are supported for non-interactive use (set via `--output` flag or `config.getOutputFormat()`):

| Format | Value | Use Case |
|---|---|---|
| `TEXT` | `text` | Human-readable terminal output |
| `JSON` | `json` | Single JSON object at end |
| `STREAM_JSON` | `stream-json` | Newline-delimited JSON stream |

Stream-JSON event types (`JsonStreamEventType`):
- `init` — session started
- `message` — model text content
- `tool_use` — tool call request
- `tool_result` — tool result
- `error` — error event
- `result` — final response

## ShellTool Deep Dive

### Command Wrapping (Linux/macOS)
```bash
# Gemini wraps every shell command like this:
{ <user-command> }; __code=$?; pgrep -g 0 >${tempFile} 2>&1; exit $__code;
```
This captures all PIDs in the process group so background processes can be tracked and killed.

### Security Checks
- `validatePathAccess(cwd)` — enforces workspace boundary before execution.
- Result: `ToolErrorType.PATH_NOT_IN_WORKSPACE` if path is outside the allowed workspace.
- Shell parser (`shell-utils.js`): `parseCommandDetails()`, `getCommandRoots()`, `hasRedirection()`, `splitCommands()`.

### Inactivity Timeout
- `config.getShellToolInactivityTimeout()` — kills processes that produce no output for the configured duration.
- Timer is reset on each output chunk (`resetTimeout()`).

### Policy Integration
- `getPolicyUpdateOptions()` returns `commandPrefix: rootCommands[]` — allows "Always allow `npm`" type rules.
- `stripShellWrapper()` removes the pgrep wrapper before policy matching.

## Tool Base Classes
All tools extend either:
- `BaseDeclarativeTool` — JSON schema-declared tools with static `parametersJsonSchema`.
- `BaseToolInvocation` — Instance created per-call, with `MessageBus` confirmation flow.

## Confirmation Flow
Every tool call goes through:
1. `shouldConfirmExecute()` → checks `PolicyEngine` via `MessageBus`
2. `getMessageBusDecision()` → resolves: `ALLOW` | `DENY` | `ASK_USER`
3. Timeout: 30 seconds before defaulting to `ASK_USER`
4. If `ASK_USER`: `getConfirmationDetails()` provides typed UI data (`'edit'`, `'exec'`, `'mcp'`, `'info'`)
5. On confirm: `publishPolicyUpdate()` persists if outcome is `ProceedAlwaysAndSave`
