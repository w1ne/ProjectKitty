# ProjectKitty

ProjectKitty is an open-source terminal coding agent prototype that implements the Article 1 foundation:

- planner
- code intelligence
- typed tool runtime with policy checks
- durable memory
- Bubble Tea terminal UI

The repository is intentionally scoped to the first article. It focuses on the agentic loop and clean subsystem boundaries rather than deep code parsing, PTY execution, or model integration.

## Current Capabilities

- understands a task and runs a deterministic meow loop
- gathers focused repository context without reading every file
- executes typed runtime actions with explicit policy checks
- persists session logs and project facts under `.projectkitty/`
- streams status through a Bubble Tea interface

## Run

```bash
go run ./cmd/projectkitty -task "Inspect the repo and validate the Go test suite."
```

The current workspace is used as the default target. You can point it at another repository with `-workspace`.

## Test

```bash
go test ./...
```
