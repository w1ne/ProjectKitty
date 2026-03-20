# Gemini CLI Reverse Engineering Report

This report details the internal mechanics of the Gemini CLI, based on a direct analysis of its fully unminified TypeScript source code, compiled JavaScript artifacts, and configuration schemas.

- **Initial analysis**: `@google/gemini-cli` **v0.27.3**
- **Extended with updates through**: **v0.33.1** (March 12, 2026) — see [Section 24](#24-changes-v0280--v0331) for all new findings.

> **Key Difference vs. Claude Code & Codex**: Gemini CLI ships its source as compiled (but fully readable) JavaScript — no minification, no bundling. This makes it the most transparent AI CLI analyzed so far.

## Table of Contents
1. [Application Architecture](#1-application-architecture)
2. [Authentication System](#2-authentication-system)
3. [Model Hierarchy & Routing](#3-model-hierarchy--routing)
4. [The Agentic Loop (Turn Engine)](#4-the-agentic-loop-turn-engine)
5. [Agent Architecture & Sub-Agents](#5-agent-architecture--sub-agents)
6. [Toolset Catalog](#6-toolset-catalog)
7. [Memory & Session Persistence](#7-memory--session-persistence)
8. [Settings & Configuration System](#8-settings--configuration-system)
9. [Policy Engine & Approval Modes](#9-policy-engine--approval-modes)
10. [Hook System](#10-hook-system)
11. [Sandbox Mechanisms](#11-sandbox-mechanisms)
12. [Telemetry & Observability](#12-telemetry--observability)
13. [Experiment Flags](#13-experiment-flags)
14. [MCP Integration](#14-mcp-integration)
15. [Prompt Engineering & System Prompt](#15-prompt-engineering--system-prompt)
16. [UI Layer (Ink/React)](#16-ui-layer-inkreact)
17. [Admin Controls (Enterprise)](#17-admin-controls-enterprise)
18. [Environment Variables Catalog](#18-environment-variables-catalog)
19. [External Endpoints & Infrastructure](#19-external-endpoints--infrastructure)
20. [Skills System](#20-skills-system)
21. [Context Compression](#21-context-compression)
22. [Extension System](#22-extension-system)
23. [Dark Corners & Hidden Behaviors](#23-dark-corners--hidden-behaviors)
24. [Changes v0.28.0 → v0.33.1](#24-changes-v0280--v0331)

---

## 1. Application Architecture

Gemini CLI is a two-package Node.js application composed of an **ESM module** architecture with full TypeScript source.

### 1.1 Package Layout
- **Root Package**: `@google/gemini-cli` (v0.27.3) — CLI layer: React/Ink UI, argument parsing, sandbox launcher, session management.
- **Core Package**: `@google/gemini-cli-core` — Engine: agent loop, tool registry, config, telemetry, MCP, policy, hooks, skills.

### 1.2 Entry Point Sequence
The `main()` function in `gemini.js` follows this startup sequence:
1. **`startupProfiler.start('cli_startup')`** — Profile every startup phase.
2. **`loadSettings()`** — Load JSON settings from disk (workspace + user + system + remote admin layers).
3. **Memory Args Tuning** — Relaunch with `--max-old-space-size=<50% of RAM>` if needed (via `GEMINI_CLI_NO_RELAUNCH` bypass).
4. **Sandbox Detection** — If sandboxing is enabled and not yet inside the sandbox, re-exec into the container.
5. **Auth Refresh** — Pre-sandbox OAuth or API key validation.
6. **`initializeApp()`** — Loads extensions, MCP servers, skills, tool registry.
7. **Interactive vs. Non-interactive** — Renders Ink UI or runs `runNonInteractive()`.

### 1.3 Key Dependencies
| Dependency | Purpose |
|---|---|
| `ink` (forked `@jrichman/ink`) | React-based Terminal UI |
| `react` 19.x | UI rendering |
| `@google/genai` 1.30.0 | Gemini API SDK |
| `@modelcontextprotocol/sdk` | MCP client |
| `@agentclientprotocol/sdk` | A2A (Agent-to-Agent) protocol |
| `simple-git` | Git context detection |
| `zod` | Settings schema validation |
| `fzf` | Fuzzy file search |
| `highlight.js` / `lowlight` | Code syntax highlighting in terminal |
| `ripgrep` (via discovery) | Secondary grep engine (fallback) |
| `yargs` | CLI argument parsing |
| `undici` | HTTP client |
| `dotenv` | `.env` file loading at project root |

### 1.4 Self-Managed Memory
On startup, the CLI measures total system RAM and computes a target heap of **50% of total memory**. If this exceeds the current V8 heap limit, it relaunches itself as a child process with `--max-old-space-size=<target>`. This is bypassed via env var `GEMINI_CLI_NO_RELAUNCH=true`.

---

## 2. Authentication System

Gemini CLI supports **four authentication methods**, selectable via `settings.json` or CLI flags.

### 2.1 Auth Types
```typescript
enum AuthType {
  LOGIN_WITH_GOOGLE  = 'oauth-personal',  // Browser-based OAuth2 PKCE flow
  COMPUTE_ADC        = 'adc',             // Google Cloud Application Default Credentials
  USE_GEMINI         = 'gemini',          // Direct Gemini API key (GEMINI_API_KEY)
  USE_VERTEX_AI      = 'vertex-ai',       // Vertex AI (GOOGLE_CLOUD_PROJECT + GOOGLE_CLOUD_LOCATION or GOOGLE_API_KEY)
  LEGACY_CLOUD_SHELL = 'legacy-cloud-shell', // Deprecated, migrated to COMPUTE_ADC
}
```

### 2.2 Auth Selection Logic
- **Cloud Shell Detection**: If `CLOUD_SHELL=true` or `GEMINI_CLI_USE_COMPUTE_ADC=true`, automatically selects `COMPUTE_ADC`.
- **External Auth**: `settings.security.auth.useExternal = true` bypasses all internal auth flows.
- **ValidationCancelledError**: If the user cancels the OAuth flow, the CLI exits cleanly.
- **ValidationRequiredError**: Allows the React UI to render a `ValidationDialog` before proceeding.

### 2.3 OAuth Flow (LOGIN_WITH_GOOGLE)
- PKCE-enabled OAuth 2.0 flow (Proof Key for Code Exchange).
- Tokens stored in: `~/.gemini/oauth_creds.json`.
- Browser launch is suppressed during sandbox mode for security; the link is displayed instead.

### 2.4 Vertex AI Auth
- Requires `GOOGLE_CLOUD_PROJECT` + `GOOGLE_CLOUD_LOCATION` **or** `GOOGLE_API_KEY` (express mode).
- MCP OAuth extensions support `DYNAMIC_DISCOVERY`, `GOOGLE_CREDENTIALS`, or `SERVICE_ACCOUNT_IMPERSONATION` auth provider types for individual server connections.

---

## 3. Model Hierarchy & Routing

### 3.1 Default Models
| Constant | Value |
|---|---|
| `DEFAULT_GEMINI_MODEL` | `gemini-2.5-pro` |
| `DEFAULT_GEMINI_FLASH_MODEL` | `gemini-2.5-flash` |
| `DEFAULT_GEMINI_FLASH_LITE_MODEL` | `gemini-2.5-flash-lite` |
| `DEFAULT_GEMINI_EMBEDDING_MODEL` | `gemini-embedding-001` |
| `PREVIEW_GEMINI_MODEL` | `gemini-3-pro-preview` |
| `PREVIEW_GEMINI_FLASH_MODEL` | `gemini-3-flash-preview` |
| `PREVIEW_GEMINI_31_MODEL` | `gemini-3.1-pro-preview` *(added v0.31.0)* |
| `DEFAULT_THINKING_MODE` | `8192` tokens (cap on thinking budget) |

> **v0.31.0+**: Gemini 3.1 Pro Preview is now available via the `ENABLE_PREVIEW` experiment flag. Internal utility sub-agents (e.g. compression, correction) were upgraded to Gemini 3 internally (v0.30.0). Custom reasoning models are also supported by default.
>
> **v0.32.0+**: Model steering is available directly in the workspace via the `set models` interface (settable per-project in `settings.json`).

### 3.2 Model Aliases
User-selectable aliases that resolve to concrete models:
- `auto` / `pro` → `gemini-2.5-pro` (or `gemini-3-pro-preview` if preview enabled)
- `flash` → `gemini-2.5-flash` (or `gemini-3-flash-preview` if preview enabled)
- `flash-lite` → `gemini-2.5-flash-lite`
- `auto-gemini-2.5` → Auto-router within Gemini 2.5 family
- `auto-gemini-3` → Auto-router within Gemini 3 family

### 3.3 Intelligent Auto-Routing (NumericalRouting)
When `ENABLE_NUMERICAL_ROUTING` experiment is enabled, a **classifier** dynamically routes queries:
- Routes between Pro ↔ Flash models based on task complexity.
- `CLASSIFIER_THRESHOLD` controls the confidence cutoff.
- `resolveClassifierModel()` maps `GEMINI_MODEL_ALIAS_FLASH` or `pro` based on classifier decision.
- Telemetry event: `recordModelRoutingMetrics()`.
- Manual fallback logged with `logFlashFallback()` (telemetry event `FlashFallbackEvent`).

### 3.4 Thinking Budget
- Default: 8192 tokens for extended thinking.
- Can be overridden per-agent via `modelConfig.generateContentConfig.thinkingConfig.thinkingBudget`.
- Purpose: "Cap on thinking to prevent run-away thinking loops."

---

## 4. The Agentic Loop (Turn Engine)

### 4.1 GeminiChat Session
`GeminiChat` maintains the conversation history and wraps the streaming API:
- **History**: An array of `{role: 'user'|'model', parts: [...]}` content objects.
- **`validateHistory()`**: Ensures correct alternating roles before each call.
- **Token Estimation**: Synchronously estimates prompt token count via `estimateTokenCountSync()`.
- **Retry Logic**: `retryWithBackoff()` with `DEFAULT_MAX_ATTEMPTS=2` (1 initial + 1 retry).
- **Session Resume**: `resumedSessionData` parameter for continuing saved sessions.
- **`maxAttempts: 2`** — One retry on network or content errors.

### 4.2 Turn State Machine (`GeminiEventType`)
Each response cycle yields a stream of typed events:

| Event Type | Trigger |
|---|---|
| `Thought` | A `part.thought = true` in model response → shows model's thinking |
| `Content` | Text content from the model |
| `ToolCallRequest` | A `functionCalls[]` entry → dispatches a tool invocation |
| `Finished` | `finishReason` present → loop ends or continues |
| `Citation` | Citations in the response |
| `Retry` | Triggers UI to discard partial response and retry |
| `UserCancelled` | Abort signal received |
| `MaxSessionTurns` | Hard turn limit reached |
| `LoopDetected` | Infinite loop guard triggered |
| `ContextWindowWillOverflow` | Pre-emption before context overflow |
| `Error` | Structured error with `{message, status}` |
| `InvalidStream` | Malformed stream response |
| `AgentExecutionStopped` | Agent completed or was stopped |
| `AgentExecutionBlocked` | Policy/safety blocked the agent |

### 4.3 Context Window Overflow
- The system monitors token count and signals `ContextWindowWillOverflow` before reaching the limit.
- `CompressionStatus` enum tracks the outcome: `COMPRESSED`, `COMPRESSION_FAILED_INFLATED_TOKEN_COUNT`, `COMPRESSION_FAILED_TOKEN_COUNT_ERROR`, `COMPRESSION_FAILED_EMPTY_SUMMARY`, `NOOP`.

### 4.4 Tool Call ID Generation
```javascript
const callId = fnCall.id ?? `${fnCall.name}-${Date.now()}-${Math.random().toString(16).slice(2)}`;
```
Falls back to a timestamp+random hex nonce if no server-provided ID.

---

## 5. Agent Architecture & Sub-Agents

### 5.1 LocalAgentExecutor
Every sub-agent runs via `LocalAgentExecutor`, which implements an isolated agentic loop:
- **Isolated ToolRegistry**: Each agent instance gets its own `ToolRegistry`, populated only with tools listed in the agent's `toolConfig`.
- **Recursion Prevention**: Agents cannot call other agents (checked via `allAgentNames` set).
- **Loop Termination**: The agent must call the special `complete_task` tool to signal completion.
- **Grace Period**: `60,000ms` (1 minute) grace period after max time is reached.

### 5.2 Defined Agent Types

#### GeneralistAgent
- Name: `generalist`
- Gets access to **all tools** from the main tool registry.
- Uses the same `getCoreSystemPrompt()` as the main agent.
- Config: `maxTurns=20`, `maxTimeMinutes=10`.
- `model: 'inherit'` — Uses the same model as the parent context.

#### CodebaseInvestigatorAgent
- A specialized agent for deep codebase exploration.
- Conditionally included in system prompt (`enableCodebaseInvestigator` flag).

#### CLIHelpAgent
- Named tool for querying CLI help documentation.
- Name: `cli_help`.

### 5.3 A2A (Agent-to-Agent) Protocol
Unique to Gemini: supports the **`@agentclientprotocol/sdk`** for federated multi-agent communication.
- `A2AClientManager` manages connections to external A2A agent servers.
- `AcknowledgedAgentsService` stores user acknowledgments for discovered agents (`~/.gemini/acknowledgments/agents.json`).
- `AgentRegistry` tracks all registered agents (local + remote).
- `AgentScheduler` coordinates parallel agent execution.

---

## 6. Toolset Catalog

Gemini CLI has **20+ built-in tools** in `gemini-cli-core` (17 original in v0.27.3; `ask_user`, `enter_plan_mode`, `exit_plan_mode` added by v0.29.0), organized by capability:

### 6.1 File System Tools
| Tool | Description |
|---|---|
| `ReadFileTool` | Read single file contents |
| `ReadManyFilesTool` | Batch read multiple files |
| `WriteFileTool` | Write/create files |
| `EditTool` | Diff-based file editing (uses `diff` library with `DEFAULT_DIFF_OPTIONS`) |
| `GlobTool` | Glob pattern file discovery |
| `LSTool` | Directory listing |
| `GrepTool` | Pattern search (built-in JS) |
| `RipGrepTool` | Pattern search using ripgrep binary (preferred when available, with `logRipgrepFallback()`) |

### 6.2 Execution Tools
| Tool | Description |
|---|---|
| `ShellTool` | Execute shell commands (wraps with `pgrep` for PID tracking on Linux/macOS) |

### 6.3 Web Tools
| Tool | Description |
|---|---|
| `WebFetchTool` | HTTP fetch content from URLs |
| `WebSearchTool` | Web search integration |

### 6.4 Memory & State Tools
| Tool | Description |
|---|---|
| `MemoryTool` | Save facts to `GEMINI.md` persistent memory file |
| `WriteTodosTool` | Maintain a markdown to-do list |

### 6.5 Agent & MCP Tools
| Tool | Description |
|---|---|
| `SubagentTool` | Launch a sub-agent for complex delegated tasks |
| `McpTool` | Dynamic wrapper for MCP server tools |
| `ActivateSkillTool` | Load and activate a named skill |
| `GetInternalDocsTool` | Access internal CLI documentation |

### 6.6 ShellTool Security Details
- **PID Tracking**: On Linux/macOS, wraps command in: `{ <cmd> }; __code=$?; pgrep -g 0 >${tempFilePath}; exit $__code;`
- **Inactivity Timeout**: Configurable `getShellToolInactivityTimeout()` — kills idle processes.
- **Path Validation**: `config.validatePathAccess(cwd)` enforces workspace boundary (`ToolErrorType.PATH_NOT_IN_WORKSPACE`).
- **Windows Support**: Skips `pgrep` wrapping, uses native shell execution.
- **Confirmation Details**: Provides `rootCommand` + `rootCommands[]` to the policy engine for rule matching.

---

## 7. Memory & Session Persistence

### 7.1 Directory Layout (`~/.gemini/`)
```
~/.gemini/
├── settings.json          # Global user settings
├── GEMINI.md              # Global persistent memory (user facts)
├── oauth_creds.json       # OAuth tokens
├── mcp-oauth-tokens.json  # Per-MCP-server OAuth tokens
├── installation_id        # Unique installation identifier
├── history/
│   └── <project-hash>/    # Per-project conversation history
├── commands/              # Global slash commands
├── skills/                # Global skills
├── agents/                # Global agent definitions
├── policies/              # Global policy rules
└── acknowledgments/
    └── agents.json        # Acknowledged remote agents
```

### 7.2 Project-Level Layout (`.gemini/` in workspace root)
```
<project-root>/.gemini/
├── settings.json          # Project-specific settings (override global)
├── GEMINI.md              # Project-specific memory
├── commands/              # Project slash commands
├── skills/                # Project skills
└── system.md              # Custom system prompt (if GEMINI_SYSTEM_MD is set)
```

### 7.3 GEMINI.md Memory File
The `MemoryTool` manages a structured memory file:
- **Global memory**: `~/.gemini/GEMINI.md`
- **Project memory**: `.gemini/GEMINI.md` in current workspace.
- **Section header**: `## Gemini Added Memories`
- **File name override**: `setGeminiMdFilename()` supports arrays of filenames for backward compatibility.
- **Write logic**: Appends facts with proper newline separation, uses `diff` library to show changes.
- **Confirmation**: Uses `ToolConfirmationOutcome.ProceedAlwaysAndSave` to save "always allow" to `settings.json`.

### 7.4 Session History
- Stored in `~/.gemini/history/<project-hash>/`.
- Format: JSON arrays of conversation turns.
- `--resume` flag: `SessionSelector` resolves a session by ID and resumes it, preserving the original `sessionId`.
- `--list-sessions`: Lists all available sessions (with optional model-generated summaries if auth available).
- `--delete-session`: Removes a session from disk.

### 7.5 Session Cleanup
`cleanupExpiredSessions()` runs at startup (after config init) to remove old sessions per the `sessions.maxAgeSec` setting in `settings.json`.

---

## 8. Settings & Configuration System

### 8.1 Settings Scope Hierarchy (highest priority wins)
1. **Remote Admin** (`CCPA server` / enterprise) — highest
2. **Workspace** (`.gemini/settings.json` in project root)
3. **User** (`~/.gemini/settings.json`)
4. **System** (`/etc/gemini/settings.json` or equivalent)
5. **Defaults** — lowest

### 8.2 Key Settings Schema Sections
```json
{
  "model": { "name": "gemini-2.5-pro" },
  "security": {
    "auth": {
      "selectedType": "oauth-personal",
      "useExternal": false
    }
  },
  "ui": {
    "hideWindowTitle": false,
    "dynamicWindowTitle": true,
    "showStatusInTitle": false,
    "incrementalRendering": true,
    "theme": "Default",
    "accessibility": {
      "enableLoadingPhrases": true
    }
  },
  "context": {
    "fileFiltering": {
      "enableFuzzySearch": true,
      "respectGitIgnore": true
    }
  },
  "telemetry": {
    "enabled": true,
    "target": "gcp",
    "logPrompts": false,
    "otlpEndpoint": null,
    "otlpProtocol": "grpc"
  },
  "general": {
    "enableAutoUpdate": true,
    "enableAutoUpdateNotification": true,
    "debugKeystrokeLogging": false,
    "dnsResolutionOrder": "ipv4first"
  },
  "advanced": {
    "autoConfigureMemory": true,
    "dnsResolutionOrder": "ipv4first"
  },
  "agents": {
    "overrides": {
      "codebase_investigator": {
        "enabled": true,
        "runConfig": { "maxTurns": 20, "maxTimeMinutes": 10 },
        "modelConfig": { "thinkingConfig": { "thinkingBudget": 8192 } }
      },
      "cli_help": { "enabled": true }
    }
  },
  "sessions": {
    "maxAgeSec": 2592000  // 30 days (v0.33.0+); was 604800 (7 days) in v0.27.3
  }
}
```

### 8.3 Settings Migration
The settings loader has automatic migration for renamed settings:
- `disableAutoUpdate` → `enableAutoUpdate` (negated)
- `disableUpdateNag` → `enableAutoUpdateNotification` (negated)
- `disableLoadingPhrases` → `enableLoadingPhrases` (negated)
- `disableFuzzySearch` → `enableFuzzySearch` (negated)
- `experimental.codebaseInvestigatorSettings` → migrated to `agents.overrides.codebase_investigator`

### 8.4 `.env` File Loading
The settings system automatically loads `.env` files from the project root on startup with these rules:
- Variables in `.env` only load if **not already present** in the environment.
- The project `.env` can exclude specific variables via `excludeVariables` setting.

---

## 9. Policy Engine & Approval Modes

### 9.1 Approval Modes
```typescript
enum ApprovalMode {
  DEFAULT    = 'default',    // Ask for most operations
  PLAN       = 'plan',       // Read-only tools + plan file writing only
  AUTO_EDIT  = 'auto-edit',  // Auto-approve file edits, ask for shell
  YOLO       = 'yolo',       // Auto-approve everything (equivalent of --dangerously-skip-permissions)
}
```

### 9.2 Policy Rule System
`PolicyEngine` operates a prioritized rule list:
- Rules have `toolName`, `argsPattern` (regex), `modes[]`, and `priority`.
- **Wildcard server rules**: `serverName__*` syntax (e.g., `myServer__*` matches all tools from `myServer`).
- Spoofing protection: If `serverName` is provided, it MUST match the prefix exactly.
- **Decision types**: `ALLOW`, `DENY`, `ASK_USER`.

### 9.3 Shell Redirection Downgrade
- Shell commands with I/O redirection (`>`, `>>`, `<`, `|`) are **downgraded** to require user confirmation even in `AUTO_EDIT` mode.
- This is bypassed only in `AUTO_EDIT` and `YOLO` modes.
- `shouldDowngradeForRedirection()` controls this logic.

### 9.4 MessageBus Confirmation Protocol
- Every tool call is dispatched via `MessageBus` with a `correlationId`.
- The UI responds with `TOOL_CONFIRMATION_RESPONSE`.
- **Timeout**: 30 seconds before defaulting to `ASK_USER`.
- **ProceedAlwaysAndSave**: Persists the approval to `~/.gemini/policies/` as a JSON rule file.

---

## 10. Hook System

Gemini CLI has a rich **hook event system** for automation and IDE integration.

### 10.1 Hook Components
| Component | Role |
|---|---|
| `HookRegistry` | Stores all registered hook definitions |
| `HookRunner` | Executes hook handlers |
| `HookAggregator` | Combines results from multiple hooks |
| `HookPlanner` | Decides which hooks to fire for an event |
| `HookEventHandler` | Processes events and dispatches to the planner |

### 10.2 Hook Events
- **`SessionStart`** – Fired on startup. Can inject `systemMessage` and `additionalContext` into the prompt.
- **`SessionEnd`** – Fired on exit (reason: `Exit`). Reliably runs before telemetry flush.
- **`ToolConfirmation`** – Fired when a tool needs confirmation. Hooks can `ALLOW`, `DENY`, or `ASK_USER`.

### 10.3 Notification Types
For tool confirmation hooks:
- `edit` — file diff confirmation
- `exec` — shell command confirmation
- `mcp` — MCP tool confirmation
- `info` — informational prompt

### 10.4 Trusted Hooks
`trustedHooks.js` manages a trust registry for hooks that are allowed to bypass normal confirmation flows.

### 10.5 Hook Configuration
Defined in `settings.json` workspace section under `hooks:`. Project-level hooks are loaded via `settings.workspace.settings.hooks` at startup.

---

## 11. Sandbox Mechanisms

### 11.1 Sandbox Types
Gemini CLI supports **five** sandbox execution environments (four as of v0.33.0):

| Type | `GEMINI_SANDBOX` value | Platform | Mechanism |
|---|---|---|---|
| **macOS Seatbelt** | `sandbox-exec` | macOS | Apple's `sandbox-exec` with `.sb` profile files |
| **Docker / Podman** | `docker` or `podman` | Linux/macOS | Container image: `us-docker.pkg.dev/gemini-code-dev/gemini-cli/sandbox:<version>` |
| **gVisor (runsc)** | `runsc` | Linux | Strongest isolation — user-space kernel; intercepts all syscalls via a Go kernel; requires Docker + gVisor runtime |
| **LXC/LXD** | `lxc` | Linux (experimental) | Full-system container (runs systemd, snapd); for tools requiring full OS (e.g. Snapcraft) that don't work in standard Docker |
| **None** | unset | All | Direct host execution |

**Activation precedence** (highest → lowest): CLI flag `-s`/`--sandbox` → `GEMINI_SANDBOX` env var → `settings.json` `"sandbox"` field.

### 11.2 macOS Seatbelt
- Profile selection: `SEATBELT_PROFILE` env var (default: `permissive-open`).
- Built-in profiles: `permissive-open` and others in the `src/patches/` directory.
- Supports custom profiles: place `sandbox-macos-<profile>.sb` in `.gemini/` directory.
- Injected variables: `TARGET_DIR`, `TMP_DIR`, `HOME_DIR`, `CACHE_DIR`, `INCLUDE_DIR_0..4`.
- Up to 5 additional workspace directories are whitelisted via `INCLUDE_DIR_*`.

### 11.3 Docker Sandbox
- Image: `us-docker.pkg.dev/gemini-code-dev/gemini-cli/sandbox:0.27.3`
- Network: `SANDBOX_NETWORK_NAME` (isolated docker network)
- Proxy: `SANDBOX_PROXY_NAME` — internal proxy routing
- Entry point: `entrypoint` from `sandboxUtils.js`
- User isolation: `shouldUseCurrentUserInSandbox()` — may run as current user inside container.
- `BUILD_SANDBOX` env var controls sandbox image building.

### 11.4 Sandbox Re-entry Flow
The main process checks `!process.env['SANDBOX']` at startup. If sandboxing is needed:
1. Auth is completed **before** entering sandbox (OAuth would fail inside).
2. stdin data is injected into args if piped.
3. `start_sandbox()` relaunches the process inside the container or seatbelt.
4. After sandbox exits, `runExitCleanup()` finalizes.

---

## 12. Telemetry & Observability

### 12.1 Telemetry Stack
Gemini CLI uses **two parallel telemetry pipelines**:

#### OpenTelemetry (OTLP)
- Standard `@opentelemetry/api-logs`, `@opentelemetry/api` instrumentation.
- Configurable exporter targets: `local` (file) or `gcp` (Google Cloud).
- Default target: `DEFAULT_TELEMETRY_TARGET` (GCP for most users).
- Default endpoint: `DEFAULT_OTLP_ENDPOINT`.
- Protocols: `grpc` or `http`.
- `telemetryAttributes.js` maps common session attributes.

#### ClearcutLogger (Google Internal)
- A separate, parallel logging system (`clearcut-logger/clearcut-logger.js`).
- Logs: `StartSession`, `NewPrompt`, `ToolCall` events.
- This is Google's internal analytics infrastructure.

### 12.2 Telemetry Events
| Event | Trigger | Key Fields |
|---|---|---|
| `StartSessionEvent` | CLI startup | `model`, `approval_mode`, `auth_type`, `mcp_servers_count`, `sandbox_enabled`, `extensions_count` |
| `UserPromptEvent` | Each user message | `prompt_length`, `prompt_id`, `auth_type`, `input` (if log_prompts enabled) |
| `ToolCallEvent` | Each tool invocation | `function_name`, `success`, `decision`, `duration_ms`, `tool_type`, `model_added_lines` |
| `ApiResponseEvent` | API response received | Token usage, model, finish_reason |
| `ContentRetryEvent` | API retry | `attempt`, `retry_type`, `delay_ms` |
| `AgentStartEvent` / `AgentFinishEvent` | Sub-agent lifecycle | Agent name, run config |
| `RecoveryAttemptEvent` | Loop recovery triggered | Reason |
| `RipgrepFallbackEvent` | ripgrep not found | Fallback to grep |
| `FlashFallbackEvent` | Auto-routing to Flash | Routing decision |
| `ApprovalModeSwitchEvent` | User changes approval mode | Old mode, new mode |
| `HookCallMetrics` | Hook execution | Hook name, duration |

### 12.3 Prompt Privacy
- `telemetry.logPrompts = false` by default — **prompts are NOT sent to telemetry by default**.
- Can be enabled via `GEMINI_TELEMETRY_LOG_PROMPTS=true`.

### 12.4 Telemetry Configuration Hierarchy (highest → lowest)
1. CLI argv (`--telemetry`, `--telemetry-target`)
2. Environment variables (`GEMINI_TELEMETRY_ENABLED`, `GEMINI_TELEMETRY_TARGET`, `GEMINI_TELEMETRY_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `GEMINI_TELEMETRY_LOG_PROMPTS`, `GEMINI_TELEMETRY_OUTFILE`, `GEMINI_TELEMETRY_USE_COLLECTOR`, `GEMINI_TELEMETRY_USE_CLI_AUTH`)
3. `settings.json` values

---

## 13. Experiment Flags

Unlike Claude Code's 663+ remote feature flags, Gemini CLI has a much smaller, **numeric** experiment flag system tied to the **Code Assist server** (CCPA).

### 13.1 Experiment Flag Registry
```typescript
export const ExperimentFlags = {
  CONTEXT_COMPRESSION_THRESHOLD: 45740197,  // Token threshold for compression
  USER_CACHING:                   45740198,  // Prompt/response caching
  BANNER_TEXT_NO_CAPACITY_ISSUES: 45740199,  // UI banner text (normal)
  BANNER_TEXT_CAPACITY_ISSUES:    45740200,  // UI banner text (under load)
  ENABLE_PREVIEW:                 45740196,  // Access to preview models (gemini-3)
  ENABLE_NUMERICAL_ROUTING:       45750526,  // AI-based model routing (pro/flash)
  CLASSIFIER_THRESHOLD:           45750527,  // Routing confidence threshold
  ENABLE_ADMIN_CONTROLS:          45752213,  // Enterprise admin panel
};
```

### 13.2 Flag Access
- Flags are fetched from the CCPA server on startup (`getExperiments()`).
- Not available for users using `USE_GEMINI` (direct API key) — those users always use the default model.
- `isPreviewModel()` checks if the resolved model is a Gemini 3 preview.

---

## 14. MCP Integration

Gemini CLI is a first-class MCP (Model Context Protocol) host.

### 14.1 MCP Transport Types
MCP servers can be connected via four transport mechanisms:
1. **stdio** — `command`, `args`, `env`, `cwd` (spawns subprocess)
2. **SSE** — `url` (Server-Sent Events)
3. **Streamable HTTP** — `httpUrl` or `url + type: 'http'`
4. **WebSocket/TCP** — `tcp`

### 14.2 MCPServerConfig Parameters
```typescript
class MCPServerConfig {
  command?:        string    // stdio
  args?:           string[]
  env?:            object
  url?:            string    // SSE or Streamable HTTP
  httpUrl?:        string    // Deprecated, use url + type
  type?:           'sse'|'http'
  timeout?:        number
  trust?:          boolean   // Bypass confirmation for this server's tools
  description?:    string
  includeTools?:   string[]  // Allow-list specific tools
  excludeTools?:   string[]  // Block-list specific tools
  oauth?:          object    // OAuth config per-server
  authProviderType?: 'dynamic_discovery'|'google_credentials'|'service_account_impersonation'
  targetAudience?: string    // For OAuth: CLIENT_ID.apps.googleusercontent.com
  targetServiceAccount?: string  // For GCP service account impersonation
}
```

### 14.3 MCP Prompts
`McpPromptLoader` loads prompts registered by MCP servers into the `PromptRegistry`, making them available as slash commands or system prompt additions.

### 14.4 MCP Tool Naming
MCP tools use a qualified naming convention: `serverName` + `__` (MCP_QUALIFIED_NAME_SEPARATOR) + `toolName`.
- Example: `my-server__search-docs`.
- This prevents tool name collisions across servers.

---

## 15. Prompt Engineering & System Prompt

### 15.1 PromptProvider Architecture
The system prompt is not a static string — it's **composed from modular sections** via `snippets.js`:

| Section | Condition |
|---|---|
| `preamble` | Always included |
| `coreMandates` | Always; adapts for Gemini 3 (`isGemini3`) and skills |
| `agentContexts` | Directory context from AgentRegistry |
| `agentSkills` | Only if skills are loaded |
| `hookContext` | If `isSectionEnabled('hookContext')` |
| `primaryWorkflows` | Only in non-plan mode; adapts for codebase investigator and todos |
| `operationalGuidelines` | Adapts for Gemini 3 and shell efficiency setting |
| `sandbox` | Includes sandbox mode details (`macos-seatbelt`, `generic`, `outside`) |
| `gitRepo` | Only if workdir is a Git repository |
| `finalReminder` | Always; includes `ReadFile` tool name |

### 15.2 Memory Integration
After composing the sections, `snippets.renderFinalShell()` appends:
- User's persistent memory (from `GEMINI.md`).
- Plan mode tools list (if `ApprovalMode.PLAN`).
- Plans directory path.

### 15.3 System Prompt Override
- Set `GEMINI_SYSTEM_MD=/path/to/system.md` (or just `GEMINI_SYSTEM_MD=1` to use `.gemini/system.md`).
- Overrides the entire compositional system.
- Custom substitutions are still applied via `applySubstitutions()`.
- Debug: Set `GEMINI_WRITE_SYSTEM_MD=1` to dump the computed system prompt to `.gemini/system.md`.

### 15.4 Compression Prompt
`getCompressionPrompt()` provides the prompt used during context window compression — a separate, simpler prompt for the history summarization task.

---

## 16. UI Layer (Ink/React)

### 16.1 React Context Providers
The UI is wrapped in a layered context tree:
```tsx
<SettingsContext.Provider>
  <KeypressProvider>
    <MouseProvider>
      <ScrollProvider>
        <SessionStatsProvider>
          <VimModeProvider>
            <AppContainer />
          </VimModeProvider>
        </SessionStatsProvider>
      </ScrollProvider>
    </MouseProvider>
  </KeypressProvider>
</SettingsContext.Provider>
```

### 16.2 Terminal Capabilities
- **Alternate Buffer**: Enters alternate screen (`\x1b[?1049h`) for immersive terminal UI. Controlled by user settings and screen reader mode detection.
- **Mouse Events**: Enabled when using alternate buffer.
- **Kitty Keyboard Protocol**: Uses `useKittyKeyboardProtocol()` hook for enhanced key detection.
- **Vim Mode**: Full vim keybinding support via `VimModeProvider`.
- **Window Title**: Dynamic title updates via `\x1b]0;...\x07`. Format: `[state] folder-name`.
- **Slow Render Detection**: Frames taking `>200ms` are logged via `recordSlowRender()`.
- **Incremental Rendering**: Opt-in (`ui.incrementalRendering`) for progressive frame updates.

### 16.3 Screen Reader Support
- `config.getScreenReader()` disables alternate buffer and animations.
- `isScreenReaderEnabled` is passed to Ink's `render()`.

### 16.4 ConsolePatcher
- Intercepts `console.log/warn/error` to route them through `coreEvents.emitConsoleLog()`.
- Prevents raw console output from breaking the Ink UI's terminal control.

### 16.5 IDE Integration
- **Zed Editor**: `runZedIntegration()` — dedicated integration mode activated via `--experimental-zed-integration`.
- `IdeIntegrationNudge` component hints users to install IDE extensions.
- `ideContextStore` shares file/symbol context between the IDE plugin and CLI.

---

## 17. Admin Controls (Enterprise)

### 17.1 CCPA Server
Enterprise users connect to a **Code Assist API (CCPA)** server on startup:
- Fetches `admin_controls` on startup if `ENABLE_ADMIN_CONTROLS` experiment is active.
- Polls periodically for live setting updates.
- Settings are validated via `FetchAdminControlsResponseSchema` (Zod schema).
- Non-enterprise users get a 403 response → controls disabled gracefully.

### 17.2 Admin Control Propagation
1. Parent process fetches admin controls.
2. Sends them to child process via **IPC** (`process.send({ type: 'admin-settings', settings: ... })`).
3. Child process's `setupAdminControlsListener()` receives and applies them.
4. If child starts before IPC arrives, settings are queued (`pendingSettings`) until config is ready.

### 17.3 Remote Settings Merge
Admin settings are merged into the settings hierarchy at the highest priority via `settings.setRemoteAdminSettings()`. They follow a custom deep merge strategy (`customDeepMerge`) per path.

---

## 18. Environment Variables Catalog

| Variable | Purpose |
|---|---|
| `GEMINI_API_KEY` | Direct Gemini API key |
| `GEMINI_MODEL` | Override the model (`argv.model` takes priority) |
| `GOOGLE_API_KEY` | Vertex AI express mode or alternative Gemini key |
| `GOOGLE_CLOUD_PROJECT` | Vertex AI project ID |
| `GOOGLE_CLOUD_LOCATION` | Vertex AI location |
| `CLOUD_SHELL` | Auto-select `COMPUTE_ADC` auth |
| `GEMINI_CLI_USE_COMPUTE_ADC` | Force `COMPUTE_ADC` auth |
| `GEMINI_CLI_NO_RELAUNCH` | Disable memory-optimized relaunch |
| `GEMINI_SANDBOX` | Activate sandbox: `sandbox-exec`, `docker`, `podman`, `runsc`, `lxc`, or `true` *(updated v0.31.0+)* |
| `SANDBOX` | Internal: set inside sandbox process to indicate current sandbox type |
| `SEATBELT_PROFILE` | macOS seatbelt profile name (default: `permissive-open`) |
| `BUILD_SANDBOX` | Trigger sandbox image build |
| `DEBUG` | Enable React StrictMode + debug output |
| `GEMINI_SYSTEM_MD` | Override system prompt with file path (1/true = use `.gemini/system.md`) |
| `GEMINI_WRITE_SYSTEM_MD` | Write computed system prompt to file for inspection |
| `GEMINI_TELEMETRY_ENABLED` | Enable/disable telemetry (`true`/`1`) |
| `GEMINI_TELEMETRY_TARGET` | Telemetry destination: `local` or `gcp` |
| `GEMINI_TELEMETRY_OTLP_ENDPOINT` | Custom OTLP endpoint |
| `GEMINI_TELEMETRY_OTLP_PROTOCOL` | OTLP protocol: `grpc` or `http` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Standard OTLP endpoint override |
| `GEMINI_TELEMETRY_LOG_PROMPTS` | Log user prompts to telemetry (false by default) |
| `GEMINI_TELEMETRY_OUTFILE` | Write telemetry to local file |
| `GEMINI_TELEMETRY_USE_COLLECTOR` | Route through OpenTelemetry Collector |
| `GEMINI_TELEMETRY_USE_CLI_AUTH` | Use CLI auth for telemetry requests |

---

## 19. External Endpoints & Infrastructure

| Endpoint | Purpose |
|---|---|
| `generativelanguage.googleapis.com` | Gemini API (USE_GEMINI auth) |
| `aiplatform.googleapis.com` | Vertex AI API |
| `accounts.google.com` | OAuth 2.0 token endpoint |
| `oauth2.googleapis.com` | Token exchange and refresh |
| CCPA server (enterprise) | Admin controls, experiments, Code Assist |
| MCP servers (user-configured) | Tool extensions via MCP protocol |
| A2A agent servers (user-configured) | Remote agent communication |
| OTLP endpoint (configurable) | Telemetry export (OTel) |
| Clearcut (internal Google) | Internal event analytics |
| Docker Hub / Artifact Registry | Sandbox container: `us-docker.pkg.dev/gemini-code-dev/gemini-cli/sandbox:0.27.3` |
| Update notifier | npm registry via `latest-version` |

---

## 20. Skills System

Skills are reusable, activatable instruction sets that augment the agent's capabilities.

### 20.1 Skill Discovery
- **Global skills**: `~/.gemini/skills/`
- **Project skills**: `.gemini/skills/`
- `SkillManager.getSkills()` aggregates from both locations.

### 20.2 Skill Activation
- Skills are listed in the system prompt under `agentSkills` section.
- `ActivateSkillTool` allows the agent to dynamically load and apply a skill mid-session.
- Skills are included in the prompt as: `{name, description, location}` triples.

---

## 21. Context Compression

When the context window fills up, Gemini CLI performs intelligent compression.

### 21.1 Compression Trigger
- `CONTEXT_COMPRESSION_THRESHOLD` experiment flag (ID `45740197`) sets the token threshold.
- `ContextWindowWillOverflow` event is emitted proactively before the limit.
- `ChatCompressionService` handles the actual compression.

### 21.2 Compression Process
1. `getCompressionPrompt()` provides a dedicated summarization system prompt.
2. The current history is summarized by the model.
3. Result replaces the conversation history.
4. `CompressionStatus` enum tracks outcomes (6 states including `COMPRESSION_FAILED_INFLATED_TOKEN_COUNT`).
5. `logChatCompressionMetrics()` records the operation for telemetry.

### 21.3 Compression Failure Handling
The `LocalAgentExecutor.hasFailedCompressionAttempt` flag tracks if a compression attempt was made; if the summary would be larger than the original, `COMPRESSION_FAILED_INFLATED_TOKEN_COUNT` is recorded and the compression is skipped.

---

## 22. Extension System

### 22.1 Extension Loading
`SimpleExtensionLoader` / `ExtensionManager` load extensions from:
- Global: `~/.gemini/extensions/`
- Project: `.gemini/extensions/`

### 22.2 Extension Capabilities
Extensions can contribute:
- Additional MCP servers
- Additional skills
- Hooks
- Agent definitions
- Custom tools

### 22.3 Extension Management
- `--list-extensions` flag: Lists all installed extensions and exits.
- Extensions are loaded **after** sandbox setup (extensions don't affect sandbox).
- `config.getExtensions()` returns the loaded extension list.
- `extension_ids` and `extensions_count` tracked in `StartSessionEvent` telemetry.

---

## Summary: Gemini CLI vs Claude Code vs Codex

> **Updated for v0.33.1** — original analysis was v0.27.3.

| Feature | Gemini CLI (v0.33.1) | Claude Code | Codex |
|---|---|---|---|
| **Source** | Fully unminified JS | Single minified bundle | Rust binary + Node wrapper |
| **Auth Options** | 4 (OAuth, ADC, API Key, Vertex) + pluggable provider API | 1 (OAuth or ANTHROPIC_API_KEY) | API key |
| **Sandboxing** | Docker/Podman + macOS Seatbelt + gVisor + LXC (5 total) | macOS Seatbelt | Linux bubblewrap |
| **Feature Flags** | 8 numeric flags (CCPA server) | 663+ remote Tengu flags | N/A |
| **Multi-agent** | A2A protocol (OAuth2 + HTTP auth) + local sub-agents + browser agent | Bark sub-agents (teammate) | Codex agents |
| **Memory** | GEMINI.md (global + project) | MEMORY.md per-project | SQLite |
| **Telemetry** | OTel + ClearcutLogger | Statsig + Sentry + GrowthBook | Minimal |
| **UI Framework** | React 19 + Ink (forked) | React + Ink | Ratatui (Rust) |
| **Config Format** | JSON with 5-layer merge + TOML policies | JSON (settings.json) | TOML/flags |
| **Approval Modes** | 4 (default/plan/auto-edit/yolo) | 1 (isAuthorized) | Policy-based |
| **Policy Engine** | Regex rules + TOML files + wildcard MCP | SHA256 command hashing | `approved_exec_policy` |
| **Plan Mode** | First-class: 5-phase workflow, research subagents, annotations, external editor | Plan-as-prompt injection | N/A |
| **Hook System** | Full event-driven hooks | Post-tool auto-format | N/A |
| **Skills** | First-class skill management + slash command activation | N/A | Explicit skill system (AGENTS.md) |
| **Enterprise** | CCPA admin controls + A2A federated agents | OrgPenguin mode | N/A |
| **Model Routing** | AI classifier (pro/flash) + Gemini 3/3.1 preview tier | Manual tier selection | Single model |
| **Parallel tool calls** | Yes (read-only tools) | Not observed | N/A |
| **Browser automation** | Experimental browser agent | N/A | N/A |

---

## 23. Dark Corners & Hidden Behaviors

These are the most surprising behaviors discovered in the deep-dive second pass through the Gemini CLI source code. They are not documented externally.

### 23.1 EditTool Has a 4th LLM Call
When the model's edit fails all three replacement tiers (exact → indentation-flexible → regex-token), `FixLLMEditWithInstruction` makes a **separate LLM API call** asking the model to repair its own malformed edit. This is a hidden 4th call invisible to the user. Logged as `EditCorrectionEvent` in telemetry.

### 23.2 WriteFileTool Makes a Validation LLM Call Before Every Write
`getCorrectedFileContent()` runs before every file write:
- For existing files: `ensureCorrectEdit()` — validates the proposed content via LLM against the current file.
- For new files: `ensureCorrectFileContent()` — validates the content via LLM.
These are **silent hidden LLM calls** that can double the effective API usage for write-heavy sessions. Disabled by `config.getDisableLLMCorrection()`.

### 23.3 The Model Classifier Ignores Tool Call History
`ClassifierStrategy` strips all tool call and tool response turns from the history before sending to the classifier. It only sees the last 4 **user/model text turns** from a 20-turn window. A long tool-heavy session will be classified based on the last few text exchanges only.

### 23.4 ModelAvailabilityService Is Per-Session and Non-Persistent
The availability state machine (`terminal` / `sticky_retry`) only persists for the current session — it lives in RAM in the `ModelAvailabilityService.health` Map. If the CLI is restarted after a quota error, the terminal state is cleared and the model will be attempted again. `resetTurn()` also resets sticky-retry at the start of each user turn.

### 23.5 Error Reports Are Written to /tmp Unconditionally
Every turn-level API error triggers `reportError()` which writes `gemini-client-error-<type>-<timestamp>.json` to `/tmp/`. This file **includes the full conversation history as context** and is written regardless of debug mode. These files accumulate silently in `/tmp/` over time.

### 23.6 Plan Mode Is a System Prompt Injection, Not Just a Policy
`ApprovalMode.PLAN` physically rewrites the system prompt to inject a 4-phase planning workflow. The model's behavior in Plan Mode is governed by these injected instructions, not by the policy engine. The policy engine only enforces the tool allowlist (`PLAN_MODE_TOOLS`). A hardcoded string `PLAN_MODE_DENIAL_MESSAGE` is emitted by `CoreToolScheduler` as the tool error when a write tool is attempted.

### 23.7 Hook Context Injection Bypasses System Prompt
`SessionStart` hook `additionalContext` is NOT appended to the system prompt. It is wrapped in `<hook_context>...</hook_context>` XML tags and **prepended to the user's first message**. This means it counts against the user's turn context budget, not the system prompt budget, and is visible to the model as user-turn content.

### 23.8 CoreToolScheduler Uses a Static WeakMap Against Memory Leaks
`CoreToolScheduler.subscribedMessageBuses` is a static class-level WeakMap. This is a direct countermeasure against React's re-render cycle creating new scheduler instances per render. Without this, each render would double-subscribe to the MessageBus, causing duplicate confirmation dialogs.

### 23.9 The Fallback Handler Has an Upgrade Intent
When a user's quota is exhausted and they're on the OAuth plan, one of the 5 fallback intents is `'upgrade'` — which opens `https://goo.gle/set-up-gemini-code-assist` in the browser. This is not documented anywhere in the CLI's help text.

### 23.10 Retry Jitter Can Make Delay Negative (Clamped to 0)
The exponential backoff applies ±30% jitter: `delay + (delay * 0.3 * (Math.random() * 2 - 1))`. The result is passed through `Math.max(0, ...)` to prevent negative delay. For very short initial delays, the jitter can mathematically produce a 0ms wait.

### 23.11 Stream-JSON Output Mode Exists for CI/Scripting
Non-interactive mode supports `--output stream-json`, which emits newline-delimited JSON events (`init`, `message`, `tool_use`, `tool_result`, `error`, `result`). This is a structured streaming API over stdout, enabling programmatic Gemini CLI consumption. Not prominent in docs.

### 23.12 Gemini-3 Models Get Extra System Prompt Mandates
When the resolved model is a preview (`gemini-3-*`), the `coreMandates` section injects:
> "**Explain Before Acting:** Never call tools in silence. You MUST provide a concise, one-sentence explanation of your intent or strategy immediately before executing tool calls."

This means Gemini-3 model behavior is explicitly more verbose by design — it's a prompt-level behavioral flag, not a model capability difference.

---

## 24. Changes v0.28.0 → v0.33.1

This section documents all significant architectural and behavioral changes introduced from **v0.28.0** through **v0.33.1**, extending the original v0.27.3 analysis.

### 24.1 Sandbox Expansion (v0.31.0–v0.33.0)

Two new sandbox types were added (see updated Section 11):
- **gVisor / runsc** (`GEMINI_SANDBOX=runsc`): Linux-only. Strongest isolation available — all syscalls handled by a user-space Go kernel. Requires Docker with the gVisor runtime (`runsc`) installed.
- **LXC/LXD** (`GEMINI_SANDBOX=lxc`): Linux-only, experimental. Full-system containers that can run `systemd`, `snapd`, etc. Designed for tools like Snapcraft/Rockcraft that require a full OS environment.
- **Podman** support added alongside Docker (`GEMINI_SANDBOX=podman`).
- Sandbox activation now supports a CLI flag (`-s`/`--sandbox`) as highest-priority override.

### 24.2 New Model: Gemini 3.1 Pro Preview (v0.31.0)

`gemini-3.1-pro-preview` added as a new preview tier alongside `gemini-3-pro-preview`. Accessible via `ENABLE_PREVIEW` experiment flag. Internal utility models (LLM correction, context compression summarizer) upgraded to Gemini 3 (v0.30.0).

### 24.3 Plan Mode Overhaul (v0.28.0–v0.33.0)

Plan Mode underwent the largest series of changes across these releases:

| Release | Change |
|---|---|
| v0.28.0 | `exit_plan_mode` tool registered; markdown rendering in `ask_user` |
| v0.29.0 | `/plan` slash command; `enter_plan_mode` tool; `replace` tool for plan editing; MCP servers usable in plan mode |
| v0.30.0 | Formalized 5-phase sequential planning workflow; skills enabled within Plan Mode; `AskUser` tool multi-line + required types |
| v0.31.0 | Automatic model switching in Plan Mode; work summarization; read-only constraint enforcement; research subagent support |
| v0.32.0 | Open and modify plans in external editors; adapt workflow based on task complexity |
| v0.33.0 | Built-in research subagents in Plan Mode; annotation support for feedback during iteration; `copy` subcommand for plans; Plan Mode enabled by default (preview) |

### 24.4 New Tool: AskUser (`ask_user`) (v0.28.0–v0.30.0)

A first-class interactive user-prompt tool was added:
- Renders in the Ink UI as a dialog with labeled options.
- Supports multi-line text responses and multi-select options (v0.30.0).
- `ask_user` label limit: 16 characters (v0.29.0).
- Markdown rendering in `ask_user` tool responses (v0.28.0).
- The tool count (Section 6) is now **19+** (17 original + `ask_user` + `enter_plan_mode`).

### 24.5 Browser Agent (Experimental) (v0.31.0)

A new experimental browser agent was introduced:
- Allows the CLI to interact with and automate web pages.
- Progress emission during browser actions.
- Automation overlay with visual feedback in the UI.
- Activated via agent config; not exposed in the standard tool registry by default.
- Integration tests added in v0.31.0+.

### 24.6 A2A Protocol Enhancements (v0.32.0–v0.33.0)

The Agent-to-Agent protocol received significant security upgrades:
- **Robust streaming reassembly** for A2A message streams (v0.32.0).
- **`Kind.Agent`** classification: sub-agents now carry an explicit type tag (v0.32.0).
- **HTTP authentication** for remote A2A agents (v0.33.0).
- **Authenticated agent card discovery** — auth-required states directly indicated (v0.33.0).
- **OAuth2 Authorization Code** flow for A2A agent authentication (v0.33.0).

### 24.7 Policy Engine Improvements (v0.29.0–v0.33.0)

- **TOML policy files**: `~/.gemini/policies/` now supports TOML format with tool name validation (v0.33.0).
- **Wildcard MCP tool policies**: `serverName__*` wildcard now works with MCP tools (v0.31.0).
- **Deprecation of `--allowed-tools` / `excludeTools`**: Users pushed toward full `PolicyEngine` adoption (v0.30.0).
- **Workspace auto-acceptance**: Workspace-level policies now auto-accept by default (v0.32.0).

### 24.8 Security Hardening (v0.28.0–v0.31.0)

- **Deceptive URL detection**: Tool confirmation dialogs now flag URLs that may be misleading (e.g., typosquatting) (v0.31.0).
- **Unicode spoofing prevention**: Unicode characters are stripped from terminal output to block terminal escape injection (v0.31.0).
- **Tool output masking**: Observation-level filtering with remote configuration support — the model can be prevented from seeing certain tool output patterns (v0.29.0).
- **MCP server OAuth consent**: Users must explicitly consent before a MCP server can initiate an OAuth flow (v0.28.0).
- **Trusted folder atomic writes**: File writes to trusted project folders use atomic write + validation (v0.29.0).

### 24.9 System Prompt Overhaul (v0.29.0)

The `getCoreSystemPrompt()` composition underwent a significant rewrite:
- Described internally as "overhaul for rigor, integrity, and intent alignment."
- Sub-agent definitions are now serialized in **XML format** (was previously plain text/Markdown).
- Tool output masking rules injected from remote configuration.

### 24.10 New Slash Commands (v0.28.0–v0.33.0)

| Command | Added | Purpose |
|---|---|---|
| `/prompt-suggest` | v0.28.0 | AI-generated prompt suggestions for the current context |
| `/plan` | v0.29.0 | Enter plan mode and manage structured plans |
| `/footer` | v0.33.0 | Configure a custom persistent footer line in the CLI UI |
| `/memory`, `/init`, `/extensions`, `/restore` | v0.33.0 | Available in ACP (Agent Communication Protocol) context |
| `skill activation` | v0.34.0 preview | Skills activatable directly via slash command syntax |

### 24.11 Background Shell Commands (v0.28.0)

The `ShellTool` now supports **background shell execution** — commands can be launched non-blocking, allowing the agent to start a long-running process and continue interacting while it runs. This is separate from the existing inactivity timeout mechanism.

### 24.12 Pluggable Auth Provider Infrastructure (v0.28.0)

The authentication system was refactored to support pluggable providers:
- Internal `authProviderType` now formalized as a plugin interface.
- Enables third-party or enterprise auth integrations beyond the four built-in types.
- MCP OAuth provider (`MCPOAuthProvider`) implementation added (v0.33.0).

### 24.13 Performance: Parallel Operations (v0.31.0–v0.32.0)

- **Parallel function calling for read-only tools**: Multiple read-only tool calls (e.g. `read_file`, `grep`) in a single model turn can now execute in parallel, not sequentially. Significant speedup for codebase exploration sessions.
- **Parallel extension loading**: Extensions are loaded concurrently on startup (v0.32.0).
- **Ranged reads with limited searches**: File read and search tools support byte/line ranges to avoid loading entire large files (v0.31.0).
- **Tool descriptions optimized for Gemini 3**: Shorter, more precise descriptions reduce prompt token usage when targeting Gemini 3 models.

### 24.14 Generalist Agent Enabled by Default (v0.32.0)

`GeneralistAgent` is now **on by default** (was previously opt-in via `agents.overrides.generalist.enabled`). This means the CLI now automatically delegates complex tasks to the generalist sub-agent without requiring configuration.

### 24.15 Session Retention Policy (v0.33.0)

`sessions.maxAgeSec` now defaults to **30 days** (`2592000` seconds) instead of the previous 7 days (`604800`). Old sessions outside this window are still cleaned up at startup by `cleanupExpiredSessions()`.

### 24.16 UI / UX Changes (v0.28.0–v0.33.0)

| Feature | Release |
|---|---|
| Automatic theme switching based on terminal background color | v0.28.0 |
| Solarized Dark / Solarized Light themes added | v0.30.0 |
| Vim motion operators: `W`, `B`, `E` | v0.29.0 |
| Text wrapping for markdown tables | v0.30.0 |
| Minimal-mode / clean UI toggle (prototype) | v0.30.0 |
| Interactive shell autocompletion (Ctrl+R style reverse search) | v0.32.0 |
| Compact header redesign with ASCII icon | v0.33.0 |
| Context window display **inverted** — now shows *usage* (used tokens), not remaining capacity | v0.33.0 |
| Custom footer via `/footer` command | v0.33.0 |
| Better thinking block visualization | v0.33.0 |
| Semantic focus colors standardized | v0.33.0 |

### 24.17 Behavioral Evaluation Framework

Starting v0.29.0+, Gemini CLI ships with an internal **behavioral evaluation framework** (evals infrastructure) for testing agent behavior against reference task sets. Not user-facing, but expands the test surface for agent quality regressions. Mentioned explicitly in v0.29.0 notes as "behavioral evaluation framework expansion."

---

