# What a Coding Agent Actually Reads

Task: *inspect the planner validation command*.

We ran it both ways against the actual projectKitty repo.

**Naive:** scan every `.go` file containing any of `planner`, `validation`, `command`, `run`, `test`. Result: 13 files, 29,465 tokens of raw source. The top three by token-hit count are `service_test.go`, `agent_test.go`, `planner_test.go` — all test files. The file with the actual answer, `planner.go`, ranks 7th.

Gemini 2.0 Flash read all of it and found the right answer:

> The "planner validation command" is determined by the `chooseValidationCommand` function in `internal/agent/planner.go`. This function checks if the State indicates the presence of a Go module. If a Go module is detected, it returns `"go test ./..."`. Otherwise, it returns `"git status --short"`.

Correct. The model is capable. But it burned 29,000 tokens to get there, most of them test file noise that had nothing to do with the question.

**Whiskers on the same task:**

```text
Candidate files (5):
  internal/agent/planner.go
  cmd/projectkitty/main.go
  internal/ui/model.go
  internal/agent/agent.go
  internal/intelligence/service.go

Focused symbol: chooseValidationCommand in internal/agent/planner.go (confidence 72)

Snippet:
func chooseValidationCommand(state State) string {
    if state.SearchTool != nil && state.SearchTool.Result != nil && state.SearchTool.Result.HasGoModule {
        return "go test ./..."
    }
    if state.SearchTool == nil || state.SearchTool.Result == nil {
        return "git status --short"
    }
    for _, file := range state.SearchTool.Result.CandidateFiles {
        if strings.EqualFold(file, "go.mod") || strings.HasSuffix(file, "/go.mod") {
            return "go test ./..."
        }
    }
    return "git status --short"
}
```

The planner receives the summary and focused snippet — roughly 300 tokens. Same answer, 100x cheaper.

The argument for Whiskers is not that the model fails without it. On a clean question against a small repo, a capable model finds the right answer even in 30K tokens of noise. The argument is what happens at scale: a 20-turn agent session running naive search at 29K tokens per turn costs ~$2 per task at current API rates, and on a 50,000-file repository the naive file list stops fitting in context entirely. Whiskers exists to keep the per-turn cost low enough that the loop can actually run.

This is the code-reading layer for projectKitty — **Whiskers**. Its job is to deliver `chooseValidationCommand` directly, without making the model search for it.

---

## 1. Retrieval Is a Filtering Problem

Before the model reasons well, the agent needs to answer four smaller questions: which files are probably relevant, which functions live inside them, which region should be shown, and what can be ignored. That's a retrieval problem — not vector search, not semantic magic, but a disciplined local pipeline that converts a vague task into a short list of likely code locations.

The pipeline Whiskers runs:

1. tokenize the task into search terms
2. first pass: ripgrep across the workspace (or `git ls-files`, or a directory walk when neither is available — with common build and dependency directories skipped at the walk layer to match what ripgrep and git filter via `.gitignore`)
3. second pass: re-search using names derived from the first slice — file base names, symbol names — to pull in structurally related files that keyword search missed
4. score and rank by path match, content match, symbol name overlap, and fuzzy path subsequence matching; keep the top five
5. extract symbols from likely files — function names, types, snippets capped at 20 lines
6. score symbols against task tokens; require a structural name match before declaring a focused hit
7. if no focused symbol, retry with the single longest task token and merge the expanded candidate list — once
8. trace related files around the strongest symbol: same package, imports it uses
9. read the focused symbol in full
10. outline the related files for one cross-file hop
11. emit `context_window_will_overflow` if estimated token cost exceeds 40K; emit `loop_detected` and stop if the session exceeds the turn limit

Here is what this produces on the same task:

```text
[search]  Focused context narrowed to 5 files via ripgrep (2 passes):
          internal/agent/planner.go, internal/agent/agent.go,
          cmd/projectkitty/main.go, internal/agent/types.go,
          internal/intelligence/service.go

[outline] Outlined 5 candidate files.
          Related files: internal/agent/agent.go, cmd/projectkitty/main.go.
          Best symbol match: chooseValidationCommand in internal/agent/planner.go.

[symbol]  Read symbol chooseValidationCommand from internal/agent/planner.go.
```

Compare that to "here are ten files that contain the word test." The model gets one function with the right name.

---

## 2. Symbol Extraction and the Interface Boundary

Candidate files are not enough. Handing full files to the planner is the same problem one level down.

Whiskers extracts symbols using Tree-sitter grammars for the languages that matter first: Go, Java, JavaScript, TypeScript, Python, Rust, Ruby, Bash, C, C++, C#, PHP, Scala. Unsupported files fall back to regex. So instead of a file path, the planner gets:

- `internal/agent/planner.go`
- symbols: `chooseValidationCommand`, `NewPlanner`, `DefaultPlanner.Next`

That changes the next action from "maybe read the whole file" to "read this specific symbol."

Two behaviors worth noting:

**Snippet truncation.** Snippets are capped at 20 lines. Full function bodies can run thousands of tokens; Whiskers doesn't send them. Budget control at the extraction layer, before the model ever sees the context.

**Weak match rejection.** If the outline can't produce a symbol whose name contains a task token, it says "No strong symbol match yet" and the planner decides what to do next. The typical agent failure is reading something nearby and acting confident. Whiskers refuses.

The interface that wraps this stays small on purpose:

```
Search(task, workspace) → candidate files
Outline(task, workspace, files) → symbols, focused match, related files
ReadSymbol(workspace, path, name) → symbol content
```

The `Search` / `Outline` / `ReadSymbol` split came directly from studying how Codex separates find-tools from read-tools — two capabilities with a clean boundary, not one context dump. The planner asks the same question regardless of language: what files matter, what symbols are inside them, which one should I read? Whiskers answers and gets out of the way.

The boundary matters because the moment Whiskers starts reasoning about what the user probably wants, there are two planners arguing. Everything the planner decides should flow from a `ContextSnapshot`: candidate files, symbols, related files, focused match, basic project signals. Whiskers doesn't solve the task.

---

## 3. Budget, Benchmarks, and Where It Still Gets It Wrong

The filtering is cheap. Fuzzy path scoring runs at ~36 ns per call with zero allocations. A two-file outline pass takes ~839 µs. A full search pass including file I/O runs at ~8 ms. The overhead of narrowing is far smaller than the cost of not narrowing. Token estimation uses per-extension heuristics — code files at bytes/3, prose at bytes/5 — so the 40K overflow threshold is better calibrated than a flat bytes/4 guess.

The `context_window_will_overflow` event mirrors a named overflow state from Gemini's API surface — a typed signal the planner can react to before hitting a hard model limit, not a silent truncation after the fact.

Whiskers does not always get it right.

Task: *how does the agent decide when to stop*. The tokens are `agent`, `decide`, `stop` — common, broad. Whiskers scores `agent.go` highest because it's large and matches heavily. The actual answer is a two-line guard: `if state.Steps >= maxSessionTurns`. Whiskers finds the right file but scores `Run` as the focused symbol — the outer loop function — instead of the specific check. The planner gets the whole `Run` function rather than the six relevant lines. Not wrong enough to fail, but noisier than it should be. Short tasks with generic verbs are the consistent weak case; the lexical scoring has no way to separate "a function that contains a loop" from "the loop exit condition."

**What's still incomplete:**

- ranking is lexical and structural, not semantic — no embeddings, no call graph, no data flow
- cross-file hop is one level deep: outline related files after reading the focused symbol, nothing further
- Tree-sitter extraction handles top-level declarations well; anonymous functions, interface fields, and decorators within supported languages are still missed

One weakness that is no longer on the list: the fixed-sequence planner. The original planner ran a hardcoded `Search → Outline → Read → Validate` sequence regardless of what the model actually needed. That is now replaced by a model-driven planner that exposes the same Whiskers tools as callable functions — `search_repository`, `outline_context`, `inspect_symbol`, `outline_related`, `run_command`, `save_memory`, `finish` — and lets the model decide which one to call next based on current state. This is how Claude Code and Gemini CLI work: the model navigates iteratively with tools, not a script. The planner also reformulates the search query — "how does the agent stay within budget" becomes "contextOverflowTokens maxSessionTurns" before hitting ripgrep — which fixes the conceptual query failure mode entirely. A `DefaultPlanner` fallback handles offline runs and API errors.

---

## What's Next

Before projectKitty acts on a codebase, it needs to see it clearly enough to choose sensible actions. Whiskers handles that now.

Article 3 builds the runtime: executing commands safely, streaming output, handling interactive terminal behavior, and enforcing policy boundaries around operations that can't be undone.

---

Article 2 is live. Implementation in progress: [github.com/w1ne/ProjectKitty](https://github.com/w1ne/ProjectKitty)

Follow Entropora, Inc for Article 3.
