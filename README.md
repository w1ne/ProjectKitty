# ProjectKitty

ProjectKitty is an open-source terminal coding agent focused on practical repository inspection, controlled execution, durable memory, and a responsive terminal UI.

The current implementation includes:

- planner
- code intelligence
- typed tool runtime with policy checks
- durable memory
- Bubble Tea terminal UI

Right now the focus is the agentic loop and clean subsystem boundaries rather than deep code parsing, PTY execution, or model integration.

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

For a stricter local check, run:

```bash
files="$(find . -name '*.go' -type f)"
test -z "$(gofmt -l $files)"
go test ./...
```

CI runs the same formatting and test checks on every push and pull request through [`.github/workflows/ci.yml`](/home/andrii/Projects/projectKitty/.github/workflows/ci.yml).
