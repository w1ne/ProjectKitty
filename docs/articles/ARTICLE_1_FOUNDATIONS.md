# Article 2: What a Coding Agent Actually Reads: Cutting the Noise

In the first article, we outlined the general architecture of ProjectKitty. Now, let's give it its first task.

Task: *inspect the planner validation command*.

Let's run it both ways against the actual ProjectKitty repository.

**The Naive Approach:** Scan every `.go` file containing any of the words `planner`, `validation`, `command`, `run`, or `test`.

* **Result:** 13 files, 29,465 tokens of raw source.
* **The Catch:** The top three files by token-hit count were `service_test.go`, `agent_test.go`, and `planner_test.go` — all test files. The file with the actual answer, `planner.go`, ranked 7th.

Gemini 2.0 Flash read all of it and found the right answer. It correctly identified that the command is determined by the `chooseValidationCommand` function in `internal/agent/planner.go`.

The model is capable. But it burned 29,000 tokens to get there, mostly on test file noise that had nothing to do with the question.

That is why I built **Whiskers**, the local code-reading and context-gathering layer for **ProjectKitty** (our open-source CLI coding agent). Instead of blindly dumping entire files into the model's context window, Whiskers acts as a highly disciplined filter. It uses fast local search combined with syntax-aware parsing (via Tree-sitter) to extract specific, relevant symbols, drastically reducing the noise before the model ever sees it.

**The Whiskers Approach on the same task:**

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
// ... [truncated for brevity]

```

The planner receives the summary and focused snippet—roughly 300 tokens. **Same answer, 100x cheaper.**

The argument for Whiskers is not that the model fails without it. On a clean question against a small repo, a capable model finds the right answer even in 30K tokens of noise. The argument is what happens at scale. A 20-turn agent session running naive search at 29K tokens per turn costs ~$2 per task at current API rates. On a 50,000-file repository, the naive file list simply stops fitting in the context window.

Whiskers exists to keep the per-turn cost low enough that the agent loop can actually run. Its job is to deliver the exact function directly, without making the model dig for it.

---

## 1. Retrieval Is a Filtering Problem

Before the model can reason, the agent needs to answer four smaller questions: *Which files are relevant? Which functions live inside them? Which region should be shown? What can be ignored?* That's a retrieval problem. It isn't vector search or semantic magic; it's a disciplined local pipeline that converts a vague task into a short list of likely code locations.

Here is the pipeline Whiskers runs, broken down into three phases:

**Phase 1: Broad Search & Ranking**

* **Tokenize:** Convert the task into search terms.
* **First Pass:** Run `ripgrep` across the workspace (or `git ls-files`/directory walk, skipping common build directories).
* **Second Pass:** Re-search using names derived from the first slice (file base names, symbol names) to pull in structurally related files that keyword search missed.
* **Score & Rank:** Evaluate by path match, content match, symbol overlap, and fuzzy path subsequence matching. Keep the top five.

**Phase 2: Precision Extraction**

* **Extract Symbols:** Pull functions, types, and snippets (capped at 20 lines) from likely files.
* **Match:** Score symbols against task tokens. Require a structural name match before declaring a "focused hit." (If none is found, retry once using the single longest task token).
* **Trace:** Map related files around the strongest symbol (same package, imports it uses).

**Phase 3: Final Outline & Guardrails**

* **Read & Outline:** Read the focused symbol in full and outline related files for one cross-file hop.
* **Budget Check:** Emit `context_window_will_overflow` if the estimated token cost exceeds 40K, or emit `loop_detected` to stop if the session exceeds turn limits.

Compare that pipeline to simply telling the model, "Here are ten files that contain the word *test*." With Whiskers, the model gets the exact function.

---

## 2. Symbol Extraction and the Interface Boundary

Candidate files are not enough. Handing full files to the planner just shifts the noise problem one level down.

Whiskers extracts symbols using Tree-sitter grammars for major languages (Go, Java, JS/TS, Python, Rust, etc.), falling back to regex for unsupported files. So instead of a raw file path, the planner gets:

* `internal/agent/planner.go`
* **Symbols:** `chooseValidationCommand`, `NewPlanner`, `DefaultPlanner.Next`

That changes the model's next action from *"maybe read the whole file"* to *"read this specific symbol."*

**Two critical behaviors make this work:**

1. **Snippet truncation:** Snippets are capped at 20 lines. Full function bodies can run thousands of tokens; Whiskers handles budget control at the extraction layer, before the model ever sees the context.
2. **Weak match rejection:** If the outline can't produce a symbol name containing a task token, it tells the planner: "No strong symbol match yet." Agents often fail by reading something nearby and acting overly confident. Whiskers refuses to guess.

The interface that wraps this stays deliberately small:

* `Search(task, workspace)` → candidate files
* `Outline(task, workspace, files)` → symbols, focused match, related files
* `ReadSymbol(workspace, path, name)` → symbol content

This boundary matters. The moment Whiskers starts "reasoning" about what the user wants, you have two planners arguing. Whiskers provides the `ContextSnapshot`; the planner decides how to solve the task.

---

## 3. Budgets, Benchmarks, and Evolution

The filtering is cheap. The overhead of narrowing context is far smaller than the API cost of reading everything.

* **Fuzzy path scoring:** ~36 ns per call (zero allocations).
* **Two-file outline pass:** ~839 µs.
* **Full search pass (including I/O):** ~8 ms.

We also use per-extension heuristics for token estimation (code files = bytes/3; prose = bytes/5). This makes the 40K overflow threshold highly accurate. The `context_window_will_overflow` event isn't a silent truncation after the fact; it's a typed signal the planner can react to *before* hitting a hard model limit.

### Where It Still Gets It Wrong

Whiskers isn't perfect. For example, if a user asks, *"How does the agent decide when to stop?"* (tokens: `agent`, `decide`, `stop`), Whiskers struggles.

Because the tokens are broad, Whiskers scores the massive `agent.go` file highest. It finds the outer loop function (`Run`) instead of the specific two-line check (`if state.Steps >= maxSessionTurns`). The planner gets the whole `Run` function rather than just the six relevant lines. Short tasks with generic verbs are our consistent weak case because lexical scoring can't separate "a function containing a loop" from "the loop's exit condition."

Additionally, ranking is purely lexical/structural (no embeddings or call graphs yet), and Tree-sitter extraction still misses anonymous functions or complex decorators.

### The Model-Driven Planner

To navigate these edge cases, ProjectKitty uses a **model-driven planner**. Instead of following a hardcoded sequence, the planner exposes Whiskers' capabilities as callable tools (`search_repository`, `inspect_symbol`, `run_command`, etc.).

This allows the model to navigate the codebase iteratively based on the current state—much like Claude Code or the Gemini CLI. If Whiskers misses the exact line on the first pass, the planner can adjust its search parameters and try again until it finds what it needs.

---

## What's Next

Before ProjectKitty acts on a codebase, it needs to see it clearly enough to choose sensible actions. Whiskers handles that now.

In Article 3, we will build the runtime: executing commands safely, streaming output, handling interactive terminal behavior, and enforcing policy boundaries around operations that can't be undone.

---

*Article 2 is live. Implementation in progress: [github.com/w1ne/ProjectKitty*](https://github.com/w1ne/ProjectKitty) *Follow Entropora, Inc for Article 3.*