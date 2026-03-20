# Gemini CLI Hook System & Policy Engine

> **Dark corners**: `SessionStart` hook context is injected as `<hook_context>` tags prepended to the user's first message — not appended to the system prompt. `PLAN_MODE_DENIAL_MESSAGE` is hardcoded in the tool scheduler. The MessageBus uses a 30-second timeout before defaulting to ASK_USER, creating a race window.

## Hook System

### Components
| Component | Role |
|---|---|
| `HookRegistry` | Stores all registered hook definitions |
| `HookRunner` | Executes hook handlers (external processes or functions) |
| `HookAggregator` | Combines results from multiple hooks for the same event |
| `HookPlanner` | Decides which hooks to fire for each event type |
| `HookEventHandler` | Processes events, dispatches to HookPlanner |
| `TrustedHooks` | Registry of hooks that can bypass confirmation prompts |

### Hook Events
| Event | Trigger | Effect |
|---|---|---|
| `SessionStart` | CLI startup, after config init | Can inject `systemMessage` and `additionalContext` into the prompt |
| `SessionEnd` | On CLI exit (`SessionEndReason.Exit`) | Runs before telemetry flush — guaranteed cleanup |
| `ToolConfirmation` | Tool needs user approval | Hook can return `ALLOW`, `DENY`, or `ASK_USER` |

### SessionStart Injection
The `additionalContext` returned by `SessionStart` hooks is wrapped in `<hook_context>` tags and **prepended to the user's prompt**:
```
<hook_context>{additionalContext}</hook_context>

{user input}
```

### Hook Configuration
Hooks are defined in `settings.json` under the `hooks` key at either workspace or user scope:
```json
{
  "hooks": {
    "SessionStart": [{ "command": "./scripts/setup.sh" }],
    "ToolConfirmation": [{ "command": "./scripts/approve-tools.sh" }]
  }
}
```
Project hooks are passed at startup: `{ projectHooks: settings.workspace.settings.hooks }`.

### Confirmation Details Types (for ToolConfirmation hooks)
| Type | Serialized Fields |
|---|---|
| `edit` | `fileName`, `filePath`, `fileDiff`, `originalContent`, `newContent`, `isModifying` |
| `exec` | `command`, `rootCommand` |
| `mcp` | `serverName`, `toolName`, `toolDisplayName` |
| `info` | `prompt`, `urls` |

## Policy Engine

### ApprovalMode Enum
```typescript
DEFAULT   → ask for most operations
PLAN      → read-only tools + write to plans directory only
AUTO_EDIT → auto-approve file edits, ask for shell
YOLO      → approve everything automatically
```

### Policy Persistence
When user clicks "Always allow" (`ProceedAlwaysAndSave`):
- A rule file is created in `~/.gemini/policies/` (global) or `.gemini/policies/` (project).
- **Both JSON and TOML formats are now supported** (v0.33.0). TOML files with tool name validation were added.
- Rules are loaded on next startup.
- Shell "always allow" stores `commandPrefix` (root commands like `npm`, `git`).

### Policy Engine Changes (v0.30.0–v0.33.0)
- **Wildcard MCP tool policies**: `serverName__*` syntax now works for MCP tools (v0.31.0), not just local tools.
- **`--allowed-tools` / `excludeTools` deprecated** in favor of full PolicyEngine adoption (v0.30.0). Users should use policy rules instead.
- **Workspace auto-acceptance**: Workspace-level policy rules are auto-accepted by default (v0.32.0) — reduces friction for project-scoped policies.
- **Deceptive URL detection in tool confirmations**: When a tool's confirmation includes a URL, the engine checks for deceptive patterns and flags them explicitly (v0.31.0).

### MessageBus Architecture
```
tool invocation
  → BaseToolInvocation.shouldConfirmExecute()
  → PolicyEngine.getDecision()              [via MessageBus]
  → if ALLOW → execute()
  → if DENY → throw error
  → if ASK_USER → getConfirmationDetails()  [renders in UI]
  → user clicks confirm/deny/always
  → publishPolicyUpdate()                   [persists if ProceedAlwaysAndSave]
```

### Shell Command Policy
`checkShellCommand()` is called separately with additional context:
- `allowRedirection` flag — downgrade to ASK_USER if redirection is detected in non-YOLO modes.
- `subCommands[]` — each sub-command in a chained command is evaluated independently.
- `splitCommands()` breaks `cmd1 && cmd2 || cmd3` into individual segments.
