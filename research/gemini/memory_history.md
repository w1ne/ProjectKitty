# Gemini CLI Memory & Session History

> **Dark corners**: Compression failure leaves no audit trail. Error reporting writes full JSON crash dumps to `/tmp/gemini-client-error-*.json` including conversation history context. Retry uses exponential backoff with ±30% jitter.

## Memory File: GEMINI.md

Unlike Claude Code's `MEMORY.md`, Gemini uses `GEMINI.md` as the persistent memory file.

### File Locations
- **Global**: `~/.gemini/GEMINI.md` — cross-session, cross-project user facts.
- **Project**: `.gemini/GEMINI.md` — project-specific facts, higher priority in context.

### Memory Section Marker
```markdown
## Gemini Added Memories
```
Facts are appended under this header, with newline separation logic to avoid double-blank-lines.

### MemoryTool Schema
```
name:        "save_memory"
parameter:   fact (string) — the fact to remember
```
The tool description explicitly says **not** to use it for session-only context, only for persistent facts.

### Filename Flexibility
`setGeminiMdFilename()` supports a string or array of filenames, allowing backward compatibility with renamed files. `getAllGeminiMdFilenames()` returns the full list for multi-file scanning.

### Diff Display
When adding a memory, the tool uses the `diff` npm library to compute and display the change (like an edit), so users can see exactly what was written.

## Session History

### Storage Location
```
~/.gemini/history/<project-path-hash>/
```
Per-project history keyed by a hash of the absolute project path.

### Session Resume (`--resume`)
`SessionSelector.resolveSession(id)` resolves a past session:
- Loads saved conversation from history dir.
- Re-uses the original `sessionId` for continuing in the same history file.
- Full conversation is passed as `resumedSessionData.conversation` to Ink UI or non-interactive runner.

### Session Management Commands
| Flag | Action |
|---|---|
| `--resume <id>` | Continue a previous session |
| `--list-sessions` | Print all available sessions (with optional AI-generated summaries) |
| `--delete-session <id>` | Delete a session from disk |

### Session Cleanup
`cleanupExpiredSessions()` runs at startup, removes sessions older than `sessions.maxAgeSec`.
- **Original default**: `604800` (7 days)
- **Updated default (v0.33.0)**: `2592000` (30 days)

## Context Compression (In-session)

### Trigger
- `CONTEXT_COMPRESSION_THRESHOLD` experiment flag (ID `45740197`) sets the token threshold.
- `ContextWindowWillOverflow` event emitted proactively before the limit is hit.

### Process
1. `ChatCompressionService` is invoked.
2. `getCompressionPrompt()` provides a dedicated summarization prompt.
3. Model summarizes current history.
4. Summary replaces conversation history in-place.

### Failure Outcomes (`CompressionStatus`)
| Status | Meaning |
|---|---|
| `COMPRESSED` | Success |
| `COMPRESSION_FAILED_INFLATED_TOKEN_COUNT` | Summary would be larger than original — skipped |
| `COMPRESSION_FAILED_TOKEN_COUNT_ERROR` | Could not count tokens |
| `COMPRESSION_FAILED_EMPTY_SUMMARY` | Model returned empty summary |
| `NOOP` | Compression not needed |

### Comparison to Claude Code
Claude Code writes compacted content to `MEMORY.md` on disk. Gemini CLI compresses in-memory (in the active history array), which is simpler but provides no on-disk audit trail of what was summarized.

## Error Reporting: Crash Dumps to /tmp

Every unhandled API error triggers `reportError()` which writes a JSON file to disk:
```
/tmp/gemini-client-error-<type>-<ISO timestamp>.json
```
Contents: `{ error: { message, stack }, context: <conversation history> }` — the full conversation history is included in the crash dump file.

- If `JSON.stringify` fails (e.g., BigInt in history), it retries with only the error, excluding context.
- File format: `gemini-client-error-Turn.run-sendMessageStream-2025-03-14T12-00-00.json`
- This is **written to `/tmp` unconditionally** on every turn-level error, regardless of debug settings.

## API Retry Logic

All API calls use `retryWithBackoff()` with these parameters:
- `DEFAULT_MAX_ATTEMPTS = 2` (1 initial + 1 retry for content errors)
- Exponential backoff with **±30% jitter**: `currentDelay + (currentDelay * 0.3 * (random * 2 - 1))`
- Distinct 429 handling: uses `Retry-After` header if present, else exponential backoff.
- `maxDelayMs` caps the backoff ceiling.
- `retryFetchErrors` flag (configurable) controls whether network errors trigger retry.
- Per-attempt log messages distinguish 429 vs 5xx vs other failures.
