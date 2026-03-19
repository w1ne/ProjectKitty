# ProjectKitty

ProjectKitty is an open-source terminal coding agent focused on practical repository inspection, controlled execution, durable memory, and a responsive terminal UI.

Architecture and article notes live under [`docs/articles/`](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/docs/articles/), including [`ARTICLE_1_FOUNDATIONS.md`](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/docs/articles/ARTICLE_1_FOUNDATIONS.md) and [`ARTICLE_2_READING_CODE.md`](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/docs/articles/ARTICLE_2_READING_CODE.md).

The current implementation includes:

- planner
- code intelligence
- typed tool runtime with policy checks, PTY-backed shell execution, and optional `bubblewrap` sandboxing
- durable memory
- Bubble Tea terminal UI

Right now the focus is the agentic loop and clean subsystem boundaries rather than production-depth code intelligence or broad model/provider integration.

## Current Capabilities

- understands a task and runs a deterministic meow loop
- gathers focused repository context without reading every file
- reads the best matching symbol before running validation
- executes typed runtime actions with explicit policy checks
- runs shell commands through a PTY with streamed output and inactivity timeouts
- supports `--sandbox=host|auto|bwrap` for shell execution control
- persists session logs and project facts under `.projectkitty/`
- streams status through a Bubble Tea interface

## Run

```bash
go run ./cmd/projectkitty -task "Inspect the repo and validate the Go test suite."
```

The current workspace is used as the default target. You can point it at another repository with `-workspace`.

For pragmatic local isolation on Linux, prefer:

```bash
go run ./cmd/projectkitty --sandbox=auto -task "Inspect the repo and validate the Go test suite."
```

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
