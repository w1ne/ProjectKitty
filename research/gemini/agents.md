# Gemini CLI Agent Architecture

> **Dark corners**: Model routing is a 5-layer composite strategy with an actual LLM-based classifier that uses its own system prompt and few-shot examples. The ModelAvailabilityService tracks per-model health as a state machine. The fallback handler has 5 intents including an "upgrade" path that opens a browser.

## LocalAgentExecutor
Every sub-agent call runs inside `LocalAgentExecutor`:

- **Isolated ToolRegistry**: Each agent gets its own registry, populated only from the agent's `toolConfig.tools[]`.
- **Recursion block**: `allAgentNames` set prevents agents calling other agents.
- **Loop termination**: Agent must call `complete_task` tool to finish (not just stop generating).
- **Grace period**: 60 seconds after `maxTimeMinutes` is reached before hard kill.
- **Compression**: `ChatCompressionService` is used if context fills up mid-agent run.

## Defined Local Agents

### GeneralistAgent
```
name:         'generalist'
displayName:  'Generalist Agent'
model:        'inherit'        ← uses same model as caller
tools:        all tools
maxTurns:     20
maxTimeMinutes: 10
```
Uses `getCoreSystemPrompt()` in non-interactive mode. **Enabled by default as of v0.32.0** (was previously experimental/opt-in).

### CodebaseInvestigatorAgent
Specialized for deep codebase exploration. Conditionally present in the system prompt (`enableCodebaseInvestigator` flag). Configurable via `agents.overrides.codebase_investigator` settings.

### CLIHelpAgent
Named `cli_help`. Queries internal CLI documentation. Configurable via `agents.overrides.cli_help`.

## AgentRegistry
- All agents (local + remote A2A) are registered here.
- `getDirectoryContext()` provides workspace directory context for the system prompt.
- `getAllAgentNames()` used by `LocalAgentExecutor` to block recursive calls.

## A2A (Agent-to-Agent) Protocol
Unique to Gemini CLI — uses `@agentclientprotocol/sdk`:
- `A2AClientManager` manages connections to external HTTP-based A2A agent servers.
- `AcknowledgedAgentsService` stores user trust acknowledgments in `~/.gemini/acknowledgments/agents.json`.
- `AgentScheduler` coordinates parallel agent runs.
- Remote agents appear as tools in the tool registry (via `SubagentTool` wrapper).

### A2A Updates (v0.32.0–v0.33.0)
- **`Kind.Agent`** type tag: Sub-agents now carry an explicit classification (`Kind.Agent`) for routing and introspection (v0.32.0).
- **Robust streaming reassembly**: A2A message stream fragmentation is now handled reliably (v0.32.0).
- **HTTP authentication**: Remote A2A agents can require and receive authentication via standard HTTP auth headers (v0.33.0).
- **Authenticated agent card discovery**: The CLI can discover A2A agents whose cards are protected by authentication (v0.33.0).
- **OAuth2 Authorization Code flow**: Full OAuth2 auth support for A2A agent connections — the CLI can perform an OAuth2 authorization code flow to authenticate with a remote agent (v0.33.0).

### Browser Agent *(experimental, v0.31.0)*
A new built-in agent for browser-based automation:
- Launches a headless browser session and can interact with web pages.
- Emits progress events to the UI during automation.
- Displays an automation overlay to signal when the browser is active.
- Not in the standard tool registry by default — activated via agent config.
- Integration test coverage added in v0.31.0+.

## Model Routing: 5-Layer Composite Strategy

`ModelRouterService` runs a `CompositeStrategy` that evaluates strategies in priority order. The first non-null result wins:

| Priority | Strategy | Role |
|---|---|---|
| 1 (highest) | `FallbackStrategy` | Checks if requested model is available; routes to first available fallback if not |
| 2 | `OverrideStrategy` | Applies hard user/admin overrides |
| 3 | `ClassifierStrategy` | LLM-based complexity classifier (disabled if numerical routing is on) |
| 4 | `NumericalClassifierStrategy` | Numeric threshold classifier (replaces LLM classifier when enabled) |
| 5 (lowest) | `DefaultStrategy` | Returns the configured model as-is |

Every routing decision logs a `ModelRoutingEvent` with `source`, `latencyMs`, `reasoning`, and `failed` fields.

### ClassifierStrategy: The Hidden LLM Call
When `ENABLE_NUMERICAL_ROUTING` is **off**, every user prompt triggers a separate LLM call (using `baseLlmClient.generateJson()`) to classify complexity:
- **History context**: Last 4 user/model turns (not tool calls) from the last 20 turns (`HISTORY_TURNS_FOR_CONTEXT=4`, `HISTORY_SEARCH_WINDOW=20`).
- **Model config key**: `{ model: 'classifier' }` — uses a separate model config for the classifier.
- **Classifier system prompt** (excerpt):
```
You are a specialized Task Routing AI. Choose between `flash` (SIMPLE) or `pro` (COMPLEX).
A task is COMPLEX if it meets ONE OR MORE:
  1. High Operational Complexity (Est. 4+ Steps/Tool Calls)
  2. Strategic Planning & Conceptual Design
  3. High Ambiguity or Large Scope
  4. Deep Debugging & Root Cause Analysis
A task is SIMPLE if it is highly specific, bounded, Est. 1-3 tool calls.
Operational simplicity overrides strategic phrasing.
```
- **Output**: JSON `{reasoning: string, model_choice: 'flash'|'pro'}` validated with Zod.
- **6 few-shot examples** embedded in the prompt covering all complexity categories.
- **Failure mode**: If the classifier call fails for any reason, returns `null` and falls through to `NumericalClassifierStrategy` or `DefaultStrategy`.

### ModelAvailabilityService: Per-Model Health State Machine
Tracks health of each model in a `Map<string, HealthState>`:

| State | Meaning |
|---|---|
| (absent) | Model is healthy / available |
| `terminal` | Hard failure (quota exhausted, billing issue) — will not retry |
| `sticky_retry` | Transient failure — one retry allowed per turn, then blocked |

- `resetTurn()` — called between user turns to reset `consumed` flag on `sticky_retry` models (allows re-attempt each turn).
- `consumeStickyAttempt()` — marks the one retry as used; subsequent calls in the same turn are blocked.
- `markTerminal()` is permanent — persists for the full session.

### FallbackStrategy: Availability-Aware Routing
- Checks `ModelAvailabilityService.snapshot(model)` before every request.
- If model is unavailable: `selectFirstAvailable(candidates)` picks the first healthy fallback in the policy chain.
- `isLastResort` flag on a policy marks the last fallback before complete failure.

### Fallback Handler: 5 User Intents
When a model fails and a fallback is possible, `handleFallback()` prompts the user via `config.getFallbackModelHandler()` and processes one of these intents:

| Intent | Action |
|---|---|
| `retry_always` | Switch to fallback model permanently for this session via `activateFallbackMode()` |
| `retry_once` | Switch just for this turn (FallbackStrategy handles routing) |
| `stop` | Keep failed model, don't retry |
| `retry_later` | Keep failed model, don't retry (same as stop) |
| `upgrade` | Opens browser to `https://goo.gle/set-up-gemini-code-assist` |

Only available for `LOGIN_WITH_GOOGLE` auth. API key users get no fallback UI.

**Error classification** (`classifyFailureKind()`):
- `TerminalQuotaError` → `terminal`
- `RetryableQuotaError` → `transient`
- `ModelNotFoundError` → `not_found`
- Anything else → `unknown`

## SubagentTool Pattern
Any registered agent is automatically exposed as a callable tool via `SubagentTool`. The tool schema is derived from the agent's `inputConfig.inputSchema`. This allows the main model to call specialized agents as if they were regular tools.

## Agent Scheduling
`agent-scheduler.js` — handles running multiple agents in parallel when tools specify parallel execution. Uses `AgentTerminateMode` to control early exit behavior.
