# Gemini CLI Telemetry & Experiment Flags

> **Dark corners**: Every edit operation logs an `EditStrategyEvent` indicating which of the 3 replacement tiers succeeded. Failed edits trigger `EditCorrectionEvent` (the 4th LLM repair call). Model routing logs `latencyMs` and `reasoning` for every routing decision. File operations log MIME type and language.

## Telemetry Stack

Gemini CLI uses **two parallel telemetry pipelines**:

### 1. OpenTelemetry (OTLP)
- Standard `@opentelemetry/api-logs` + `@opentelemetry/api` instrumentation.
- Configurable targets: `local` (outfile) or `gcp` (Google Cloud).
- Default: GCP for production users, local file for debugging.
- Protocols: `grpc` or `http`.
- Startup: `initializeTelemetry()` runs during config init.
- Flush: `registerTelemetryConfig(config)` ensures telemetry flushes before exit.

### 2. ClearcutLogger (Google Internal Analytics)
- Runs in parallel with OTel.
- `ClearcutLogger.getInstance(config)` — singleton per config.
- Logs: `logStartSessionEvent`, `logNewPromptEvent`, `logToolCallEvent`.
- This is Google's internal Clearcut analytics infrastructure.

## Logged Events
| Event | Fields |
|---|---|
| `StartSessionEvent` | model, embedding_model, sandbox_enabled, core_tools_enabled, approval_mode, api_key_enabled, vertex_ai_enabled, debug_enabled, mcp_servers_count, mcp_tools_count, telemetry_enabled, file_filtering_respect_git_ignore, extensions_count, auth_type |
| `UserPromptEvent` | prompt_length, prompt_id, auth_type, input (if logPrompts=true) |
| `ToolCallEvent` | function_name, success, decision, duration_ms, tool_type, model_added_lines, model_removed_lines |
| `ApiResponseEvent` | token usage, model, finish_reason |
| `ContentRetryEvent` | attempt, retry_type, delay_ms, model |
| `AgentStartEvent/AgentFinishEvent` | agent name, run config |
| `RecoveryAttemptEvent` | reason |
| `EditStrategyEvent` | Which tier (exact/flexible/regex) succeeded in edit |
| `EditCorrectionEvent` | Edit was malformed and LLM repair was invoked |
| `FileOperationEvent` | file path, MIME type, language, operation (create/overwrite) |
| `ModelRoutingEvent` | model, source (strategy name), latencyMs, reasoning, failed, enableNumericalRouting, classifierThreshold |
| `RipgrepFallbackEvent` | ripgrep not found, used grep instead |
| `FlashFallbackEvent` | auto-routing to flash model |
| `ApprovalModeSwitchEvent` | old mode → new mode, duration |
| `HookCallMetrics` | hook name, duration |

## Privacy Controls
- **`logPrompts = false`** by default — prompts and user input are **NOT sent to telemetry**.
- Enable via `GEMINI_TELEMETRY_LOG_PROMPTS=true` or `settings.telemetry.logPrompts`.
- `telemetry.enabled` can be set to `false` entirely.

## Configuration Precedence (highest to lowest)
1. CLI flags (`--telemetry`, `--telemetry-target`, `--telemetry-otlp-endpoint`)
2. Environment variables (`GEMINI_TELEMETRY_*`, `OTEL_EXPORTER_OTLP_ENDPOINT`)
3. `settings.json` values

## Experiment Flags

Gemini CLI uses **8 numeric flags** tied to the enterprise CCPA (Code Assist) server.
Not available for direct API key (`USE_GEMINI`) users — they always get defaults.

| Flag Name | ID | Purpose |
|---|---|---|
| `CONTEXT_COMPRESSION_THRESHOLD` | 45740197 | Token count that triggers context compression |
| `USER_CACHING` | 45740198 | Enable prompt/response caching |
| `BANNER_TEXT_NO_CAPACITY_ISSUES` | 45740199 | UI banner text (normal state) |
| `BANNER_TEXT_CAPACITY_ISSUES` | 45740200 | UI banner text (degraded state) |
| `ENABLE_PREVIEW` | 45740196 | Access to Gemini 3 preview models |
| `ENABLE_NUMERICAL_ROUTING` | 45750526 | AI-based model router (pro/flash selection) |
| `CLASSIFIER_THRESHOLD` | 45750527 | Routing classifier confidence cutoff |
| `ENABLE_ADMIN_CONTROLS` | 45752213 | Enable enterprise admin panel (CCPA) |

## Telemetry Environment Variables
| Variable | Purpose |
|---|---|
| `GEMINI_TELEMETRY_ENABLED` | Enable or disable (`true`/`1`) |
| `GEMINI_TELEMETRY_TARGET` | `local` or `gcp` |
| `GEMINI_TELEMETRY_OTLP_ENDPOINT` | Custom OTLP endpoint |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Standard OpenTelemetry env override |
| `GEMINI_TELEMETRY_OTLP_PROTOCOL` | `grpc` or `http` |
| `GEMINI_TELEMETRY_LOG_PROMPTS` | Log user input to telemetry |
| `GEMINI_TELEMETRY_OUTFILE` | Write events to local file |
| `GEMINI_TELEMETRY_USE_COLLECTOR` | Route through OTel Collector |
| `GEMINI_TELEMETRY_USE_CLI_AUTH` | Use CLI credentials for telemetry export |
