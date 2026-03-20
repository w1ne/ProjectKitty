# ProjectKitty

An open-source terminal coding agent built around four clean subsystems: repository intelligence, PTY-backed execution, durable memory, and a Bubble Tea UI.

The build series documenting every architectural decision lives in [`docs/articles/`](docs/articles/):

| Article | Topic |
|---------|-------|
| [Article 1 — Foundations](docs/articles/ARTICLE_1_FOUNDATIONS.md) | Agent loop, planner, event model, Bubble Tea UI |
| [Article 2 — Reading Code](docs/articles/ARTICLE_2_READING_CODE.md) | Repository search, symbol extraction, context trimming |
| [Article 3 — Taking Action](docs/articles/ARTICLE_3_TAKING_ACTION.md) | PTY execution, policy gate, streaming output, concurrent jobs |

## What It Can Do

- Understands a task and runs a structured agentic loop
- Searches a repository for relevant files without reading everything
- Extracts the best matching symbol before acting
- Runs shell commands in a PTY subprocess with environment sterilization, process group kill, inactivity timeout, and streaming output
- Writes and edits files atomically with 3-tier fuzzy matching
- Enforces a configurable policy gate (`manual` / `auto` / `yolo`) before any shell execution
- Fires independent jobs concurrently via `ExecuteAsync` — tests and linting run in parallel
- Persists session logs and project facts under `.projectkitty/`
- Streams every event through a Bubble Tea terminal UI

## Run

```bash
go run ./cmd/projectkitty -task "Inspect the repo and validate the Go test suite."
```

The current directory is used as the workspace by default. Point it elsewhere with `-workspace`:

```bash
go run ./cmd/projectkitty -task "Find the auth handler" -workspace ../other-repo
```

## Test

```bash
go test ./...
```

For a stricter local check matching CI:

```bash
test -z "$(gofmt -l $(find . -name '*.go' -type f))"
go test ./...
```

CI runs formatting and tests on every push via [`.github/workflows/ci.yml`](.github/workflows/ci.yml).

## Publishing Articles

To generate LinkedIn-ready images from the Mermaid diagrams and tables in any article:

```bash
node docs/scripts/render-article.mjs docs/articles/ARTICLE_3_TAKING_ACTION.md
```

Rendered PNGs land in `docs/articles/images/<article>/` and a `-linkedin.md` file is written alongside the source.
