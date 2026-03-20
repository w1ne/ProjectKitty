# Gemini CLI Auth & Security

> **Dark corners**: Quota exhaustion triggers a browser-launched upgrade flow (`goo.gle/set-up-gemini-code-assist`). Plan Mode injects a full 4-phase workflow into the system prompt as a behavioral gate — not just a policy check. `PLAN_MODE_DENIAL_MESSAGE` is hardcoded into `coreToolScheduler.js`.

## Authentication Types
```typescript
enum AuthType {
  LOGIN_WITH_GOOGLE  = 'oauth-personal'     // Browser OAuth2 PKCE
  COMPUTE_ADC        = 'adc'                // Google Cloud ADC
  USE_GEMINI         = 'gemini'             // GEMINI_API_KEY env var
  USE_VERTEX_AI      = 'vertex-ai'          // GOOGLE_CLOUD_PROJECT + GOOGLE_CLOUD_LOCATION
  LEGACY_CLOUD_SHELL = 'legacy-cloud-shell' // Deprecated → migrated to ADC
}
```

## Auth Selection Logic
1. If `CLOUD_SHELL=true` or `GEMINI_CLI_USE_COMPUTE_ADC=true` → force `COMPUTE_ADC`.
2. If `useExternal=true` in settings → bypass all internal auth (external token provider).
3. Interactive mode: user selects via settings or first-run dialog.
4. Non-interactive mode: `validateNonInteractiveAuth()` validates before proceeding.

## OAuth Flow (LOGIN_WITH_GOOGLE)
- PKCE (Proof Key for Code Exchange) OAuth 2.0.
- Tokens stored: `~/.gemini/oauth_creds.json`.
- Browser launch suppressed inside sandbox; link is displayed instead.
- `GEMINI_SUPPRESS_BROWSER_LAUNCH` or `config.isBrowserLaunchSuppressed()` controls this.

## Vertex AI
- Requires `GOOGLE_CLOUD_PROJECT` + `GOOGLE_CLOUD_LOCATION`.
- Or `GOOGLE_API_KEY` for express (simplified) mode.
- MCP per-server auth options: `DYNAMIC_DISCOVERY`, `GOOGLE_CREDENTIALS`, `SERVICE_ACCOUNT_IMPERSONATION`.

## Policy Engine (Approval Modes)
```typescript
enum ApprovalMode {
  DEFAULT   = 'default'    // Ask for most operations
  PLAN      = 'plan'       // Read-only + plan file only
  AUTO_EDIT = 'auto-edit'  // Auto-approve edits, ask for shell
  YOLO      = 'yolo'       // Auto-approve everything
}
```
Equivalent of Claude Code's `--dangerously-skip-permissions` is `YOLO` mode.

## Policy Rule Matching
Rules are evaluated in priority order:
- `toolName`: exact match or `serverName__*` wildcard
- `argsPattern`: regex match against stable JSON-stringified args
- `modes[]`: only applies in specified approval modes
- `priority`: higher = evaluated first

Spoofing protection: if `serverName` is provided, it must **exactly** match the wildcard prefix. A server named `trusted-server__evil` cannot match the rule for `trusted-server`.

## Shell Redirection Downgrade
Shell commands with I/O redirection (`>`, `>>`, `<`) are downgraded to require user confirmation even in `AUTO_EDIT` mode. Only `AUTO_EDIT` and `YOLO` bypass this.

## MessageBus Confirmation Protocol
1. Tool call → `MessageBus.publish(TOOL_CONFIRMATION_REQUEST, {correlationId, toolCall, serverName})`
2. UI responds: `MessageBus.publish(TOOL_CONFIRMATION_RESPONSE, {correlationId, confirmed})`
3. 30-second timeout → default to `ASK_USER`
4. `ProceedAlwaysAndSave` → persists policy rule to `~/.gemini/policies/`

## Credential Storage
| File | Contents |
|---|---|
| `~/.gemini/oauth_creds.json` | Google OAuth refresh tokens |
| `~/.gemini/mcp-oauth-tokens.json` | Per-MCP-server OAuth tokens |
| `~/.gemini/installation_id` | Unique anonymous install ID |

## Plan Mode: Behavioral Gating via System Prompt

Plan Mode (`ApprovalMode.PLAN`) is not just a policy check — it physically rewrites the system prompt to inject a strict sequential planning workflow.

**Original (v0.27.3)**: 4-phase workflow.
**Updated (v0.30.0+)**: Formalized 5-phase sequential workflow with skills support, research subagents (v0.31.0+), annotation feedback (v0.33.0+), and external editor support (v0.32.0+).

Original 4-phase injected prompt (v0.27.3):
```
### Phase 1: Requirements Understanding
- Analyze the user's request to identify core requirements and constraints
- Do NOT explore the project or create a plan yet

### Phase 2: Project Exploration
- Only begin this phase after requirements are clear
- Use available read-only tools to explore the project

### Phase 3: Design & Planning
- Create a detailed implementation plan
- After saving the plan, present the full content of the markdown file to the user for review

### Phase 4: Review & Approval
- Ask user if they approve the plan, want revisions, or reject it
- **When the user approves**, prompt them to switch out of Plan Mode (Shift+Tab)
```

As of v0.30.0, a 5th phase was added and the workflow was expanded to include research subagent delegation, skills usage, and complexity-based model switching.

Constraints injected:
- Only read-only tools listed in `PLAN_MODE_TOOLS` are permitted.
- The scheduler has a hardcoded `PLAN_MODE_DENIAL_MESSAGE` for any write attempt:
  > `"You are in Plan Mode - adjust your prompt to only use read and search tools."`
- Plans are saved to `config.storage.getProjectTempPlansDir()` (project-scoped temp dir).

## Quota Error Hierarchy

Three classes of quota errors with distinct behavioral consequences:

| Class | Effect |
|---|---|
| `TerminalQuotaError` | Model marked `terminal` — no retries for rest of session |
| `RetryableQuotaError` | Model marked `sticky_retry` — one retry per turn allowed |
| `ModelNotFoundError` | Classified as `not_found` — triggers fallback or upgrade flow |

The upgrade flow opens `https://goo.gle/set-up-gemini-code-assist` via `openBrowserSecurely()`. Only available for OAuth (`LOGIN_WITH_GOOGLE`) users.

## Security Hardening (v0.28.0–v0.31.0)

- **Pluggable auth provider infrastructure** (v0.28.0): Auth providers formalized as a plugin interface. Enables third-party/enterprise auth beyond the four built-in types. `MCPOAuthProvider` added in v0.33.0.
- **MCP server OAuth consent** (v0.28.0): Users must explicitly consent before any MCP server can initiate an OAuth flow.
- **Trusted folder atomic writes** (v0.29.0): File writes to trusted project folders use atomic write + safety validation.
- **Tool output masking** (v0.29.0): Observation-level output filtering — the model can be prevented from seeing certain tool output patterns via remote configuration.
- **Deceptive URL detection** (v0.31.0): Tool confirmation dialogs flag URLs with deceptive patterns (e.g. typosquatting, misleading subdomains).
- **Unicode spoofing prevention** (v0.31.0): Unicode characters are stripped from terminal output to block terminal escape injection attacks.

## Sandbox Security
See `sandbox_details.md` for full sandbox documentation.
