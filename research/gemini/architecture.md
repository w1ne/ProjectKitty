# Gemini CLI Architecture

## Two-Package Design
Gemini CLI splits into two npm packages:
- **`@google/gemini-cli`** — CLI shell: argument parsing, Ink/React UI, sandbox launcher, session management.
- **`@google/gemini-cli-core`** — Engine: agents, tools, config, telemetry, MCP, policies, hooks, skills.

Both are compiled **unminified TypeScript** (`.js` + `.d.ts` + `.js.map`), making the full source readable directly from the install directory.

## Module Architecture
```
gemini-cli (shell)
  ├── config/          CLI config, settings loader, auth validator, sandbox config
  ├── ui/              React/Ink components (App, AppContainer, contexts, hooks, themes)
  ├── core/            App initializer, theme setup
  ├── commands/        Built-in slash command builders
  ├── services/        Command & file command loaders
  ├── utils/           Sandbox launcher, session cleaner, update checker, relaunch logic
  └── zed-integration/ Experimental Zed editor integration mode

gemini-cli-core (engine)
  ├── agents/          LocalAgentExecutor, GeneralistAgent, CodebaseInvestigator, A2A client
  ├── config/          Config class, settings, models, storage, policy
  ├── core/            GeminiChat, Turn loop, prompts, content generator, token limits
  ├── hooks/           HookRegistry, HookRunner, HookAggregator, HookPlanner, HookEventHandler
  ├── mcp/             MCP client + client manager
  ├── policy/          PolicyEngine, ApprovalMode, rule matching
  ├── prompts/         PromptProvider, snippets, substitutions
  ├── resources/       ResourceRegistry
  ├── routing/         ModelRouterService (classifier-based auto-routing)
  ├── safety/          SafetyCheckDecision, protocol
  ├── scheduler/       AgentScheduler for parallel agent runs
  ├── services/        FileDiscovery, Git, FileSystem, ContextManager, ChatCompression
  ├── skills/          SkillManager
  ├── telemetry/       OTel loggers, metrics, Clearcut logger, types, constants
  └── tools/           17 built-in tools + MCP tool wrappers
```

## Startup Sequence
1. **`startupProfiler.start('cli_startup')`** — profile every phase.
2. **`loadSettings()`** — merge workspace + user + system + remote admin layers.
3. **Memory args** — relaunch with `--max-old-space-size=<50% of RAM>` if needed.
4. **Admin controls listener** — IPC listener for enterprise remote settings.
5. **Auth refresh** — OAuth / ADC / API key validation (before sandbox).
6. **Sandbox check** — If enabled and not yet inside, relaunch into Docker or seatbelt.
7. **`loadCliConfig()`** — Full config with tools, MCP, extensions, agents.
8. **`initializeApp()`** — Register tools, load skills, start MCP servers.
9. **Interactive vs. Non-interactive** — Render Ink UI or `runNonInteractive()`.

## Key Runtime Behaviors
- **Child process relaunch**: The CLI always runs as a child of an outer process. The parent manages IPC for admin settings.
- **50% RAM heap target**: Automatically recomputed and relaunched with expanded heap.
- **Startup profiling**: Every phase is measured with `startupProfiler` and flushed to telemetry.
- **DNS resolution**: `settings.advanced.dnsResolutionOrder` (default: `ipv4first`) applied globally on startup.
