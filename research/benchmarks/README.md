# Benchmark Suite

This directory contains a reproducible benchmark harness for comparing Claude Code, Codex, and Gemini CLI on fixed local tasks.

## Goals

- Run the same benchmark prompts against all installed CLIs.
- Keep fixture workspaces identical between runs.
- Capture stdout, stderr, exit code, duration, and generated artifacts.
- Separate raw execution from later scoring.

## Benchmarks

- `repo_readonly_audit`
  - Uses the main repository as a read-only analysis target.
  - Measures repository comprehension and architecture extraction.
- `shell_workflow`
  - Uses a local fixture with TODO markers.
  - Measures shell-heavy file discovery and report generation.
- `bugfix_unittest`
  - Uses a local Python fixture with a real failing test.
  - Measures edit-test-debug loop quality.
- `long_context_recall`
  - Uses a local docs fixture with multiple fact-bearing files.
  - Measures multi-file reading and factual retention.
- `safety_boundary`
  - Attempts to cross the workspace boundary in a controlled way.
  - Measures whether the agent respects and explains execution boundaries.

## Outputs

Each run is written under `research/benchmarks/runs/<timestamp>/`.

Per benchmark and agent, the harness stores:

- `command.json`
- `stdout.txt`
- `stderr.txt`
- `result.json`
- copied workspace contents for fixture-based tasks

## Usage

```bash
python3 research/benchmarks/run_benchmarks.py
python3 research/benchmarks/run_benchmarks.py --agent codex
python3 research/benchmarks/run_benchmarks.py --agent gemini
python3 research/benchmarks/run_benchmarks.py --benchmarks bugfix_unittest,long_context_recall
```

## Notes

- The harness does not assume success. Auth failures, network failures, policy blocks, and timeouts are recorded as benchmark results.
- The harness captures raw execution data. Scoring should be applied in a follow-up evaluation pass.
