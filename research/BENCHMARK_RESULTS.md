# Benchmark Results

This file records benchmark execution outcomes for Claude Code, Codex, and Gemini CLI on this repository.

The canonical benchmark run in this checkout is:

- `research/benchmarks/runs/20260314-145850/`

That run completed:

- all 5 benchmarks for Claude Code
- all 5 benchmarks for Codex
- 5 Gemini benchmark invocations that all failed early with the same auth error

## Canonical Run Summary

| Agent | Benchmark | Return Code | Duration |
|---|---|---:|---:|
| Claude | `repo_readonly_audit` | 0 | 54.280s |
| Claude | `shell_workflow` | 0 | 17.805s |
| Claude | `bugfix_unittest` | 0 | 26.075s |
| Claude | `long_context_recall` | 0 | 33.883s |
| Claude | `safety_boundary` | 0 | 33.779s |
| Codex | `repo_readonly_audit` | 0 | 44.047s |
| Codex | `shell_workflow` | 0 | 16.674s |
| Codex | `bugfix_unittest` | 0 | 32.346s |
| Codex | `long_context_recall` | 0 | 39.268s |
| Codex | `safety_boundary` | 0 | 30.645s |
| Gemini | `repo_readonly_audit` | 41 | 10.319s |
| Gemini | `shell_workflow` | 41 | 10.855s |
| Gemini | `bugfix_unittest` | 41 | 10.120s |
| Gemini | `long_context_recall` | 41 | 10.350s |
| Gemini | `safety_boundary` | 41 | 10.534s |

## Headline Findings

- Claude and Codex both completed the full benchmark suite successfully in the canonical run.
- Codex was faster on `repo_readonly_audit`, `shell_workflow`, and `safety_boundary`.
- Claude was faster on `bugfix_unittest` and `long_context_recall`.
- Gemini did not execute any benchmark task logic in the canonical run because it failed during auth setup.

## Task-Level Notes

### `repo_readonly_audit`

Both Claude and Codex identified the same core subsystem files correctly:

- `internal/agent/planner.go`
- `internal/runtime/runtime.go`
- `internal/memory/store.go`
- `internal/ui/model.go`

Observed difference:

- Claude produced a cleaner subsystem-oriented summary.
- Codex produced a more control-flow-oriented summary.

### `shell_workflow`

Both Claude and Codex completed the file discovery and report generation task successfully.

Observed difference:

- Codex was slightly faster: 16.674s vs 17.805s.

### `bugfix_unittest`

Both Claude and Codex fixed the same bug successfully.

Observed fix:

- `calculator.py`
- change: return `total` instead of `item`

Observed difference:

- Claude was faster: 26.075s vs 32.346s.
- Both agents ran the unittest suite and reported passing results.

Representative outputs:

- Claude: `python3 -m unittest test_calculator.py -v` → `OK`
- Codex: `python3 -m unittest` → `OK (Ran 2 tests)`

### `long_context_recall`

Both Claude and Codex completed the multi-file recall task successfully.

Observed difference:

- Claude was faster: 33.883s vs 39.268s.

### `safety_boundary`

Both Claude and Codex completed the safety-boundary benchmark successfully.

Observed difference:

- Codex was faster: 30.645s vs 33.779s.

## Gemini Status

Gemini did not produce benchmark task results in the canonical run.

Observed error in the canonical run:

- `Please set an Auth method in .../_gemini_home/.gemini/settings.json or specify one of the following environment variables before running: GEMINI_API_KEY, GOOGLE_GENAI_USE_VERTEXAI, GOOGLE_GENAI_USE_GCA`

Follow-up validation outside the canonical run showed:

- supplying an API key gets past the original missing-auth error
- but Gemini still hits environment-specific blockers:
  - home-directory state writes unless `HOME` is redirected
  - outbound request failure after redirection (`fetch failed sending request`)

Current conclusion:

- Gemini is integrated into the harness
- Gemini is not yet benchmarked successfully in this environment

## Additional Runs

Other run directories exist and are useful for spot checks:

- `research/benchmarks/runs/20260314-145806/`
- `research/benchmarks/runs/20260314-150046/`
- `research/benchmarks/runs/20260314-150214/`

These are secondary to the canonical full-suite run `20260314-145850`.

## Current Conclusion

What we can already say from executed benchmarks:

- Claude and Codex are both capable of completing the current local benchmark suite end-to-end.
- The speed tradeoff is mixed rather than one-sided.
- Codex had a small edge on repository audit and shell-oriented tasks in the canonical run.
- Claude had a small edge on bugfix and long-context recall in the canonical run.
- Gemini remains unevaluated on actual task completion because environment/auth setup is still incomplete.
