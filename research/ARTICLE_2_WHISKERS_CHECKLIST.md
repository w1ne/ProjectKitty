# Article 2 Whiskers Checklist

This checklist turns the Article 2 research into an implementation yardstick for ProjectKitty.

Use it to answer three questions:

1. what Whiskers already matches
2. what is only partially aligned
3. what should change next in code

Primary references:
- [ARTICLE_2_IMPLEMENTATION_DEEP_DIVE.md](/home/andrii/Projects/ClaudeReverse/research/ARTICLE_2_IMPLEMENTATION_DEEP_DIVE.md)
- [ARTICLE_2_CODE_READING_ARCHITECTURE.md](/home/andrii/Projects/ClaudeReverse/research/ARTICLE_2_CODE_READING_ARCHITECTURE.md)
- [ARTICLE_2_READING_CODE.md](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/docs/articles/ARTICLE_2_READING_CODE.md)

## 1. Target Architecture

The research-backed target for Article 2 is:

```text
Search -> Outline -> ReadCodeItem
```

with:
- Claude-style internal structural reader
- Codex-style typed tool boundaries
- Gemini-style visible staged execution

## 2. Current Whiskers Status

### A. File Discovery

Target:
- ignore-aware repository discovery
- cheap search-first narrowing
- explicit discovery stage

Status:
- `Matched`

Why:
- Whiskers already does search first
- it uses ignore-aware discovery and fallback order
- it exposes provider/passes in the search result path

Current code:
- [service.go](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/internal/intelligence/service.go)

### B. Search / Narrowing

Target:
- ripgrep-style first pass
- refined second pass
- file candidate ranking
- no full-repo parsing up front

Status:
- `Matched`

Why:
- Whiskers already uses multi-pass retrieval
- candidate ranking exists
- parsing is done after narrowing, not before

Current code:
- [service.go](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/internal/intelligence/service.go)

### C. Structural Symbol Extraction

Target:
- syntax-aware parser-backed outlines
- one structural reader interface
- per-language grammars underneath

Status:
- `Mostly matched`

Why:
- Whiskers already uses a Tree-sitter-backed path for supported languages
- supported languages are meaningful enough for the current slice
- unsupported files still fall back to regex

What is still missing:
- broader grammar coverage
- deeper code-item extraction fidelity
- less dependence on fallback regex

Current code:
- [service.go](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/internal/intelligence/service.go)

### D. Focused Symbol Read

Target:
- one exact code-item read
- same structural engine as `Outline`
- no alternate extraction path

Status:
- `Matched`

Why:
- `Read symbol` now uses the same structural extraction path as `Outline`
- focused reads are only performed when confidence is strong

Current code:
- [runtime.go](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/internal/runtime/runtime.go)
- [service.go](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/internal/intelligence/service.go)

### E. No-Strong-Match Behavior

Target:
- explicit no-match outcome
- no fake focused read
- planner can continue safely

Status:
- `Matched`

Why:
- Whiskers already refuses weak matches
- the agent skips focused reads when confidence is insufficient

Current code:
- [agent.go](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/internal/agent/agent.go)
- [planner.go](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/internal/agent/planner.go)

### F. Typed Tool Boundaries

Target:
- explicit `Search`
- explicit `Outline`
- explicit `ReadSymbol`
- tool state visible in the agent state

Status:
- `Mostly matched`

Why:
- the stages are explicit in the agent flow
- the state now models these tool stages directly

What is still missing:
- the intelligence stages still feel partly like subsystem methods rather than fully first-class runtime tools
- search/outline/read are cleaner than before, but not yet as reusable and isolated as Codex-style mature tool handlers

Current code:
- [types.go](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/internal/agent/types.go)
- [agent.go](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/internal/agent/agent.go)

### G. Visible Staged Execution

Target:
- separate visible `Search`, `Outline`, `Read` stages
- inspectable summaries
- machine-usable event boundaries

Status:
- `Mostly matched`

Why:
- event stream now exposes these stages clearly
- article examples now reflect them

What is still missing:
- richer typed event taxonomy
- stronger separation between internal state updates and user-facing event model
- Gemini-level observable state machine depth

Current code:
- [agent.go](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/internal/agent/agent.go)
- [model.go](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/internal/ui/model.go)

### H. Relationship-Aware Retrieval

Target:
- at least some nearby-file awareness around the chosen symbol
- better than pure single-file ranking

Status:
- `Partially matched`

Why:
- Whiskers now traces a few related files around the strongest symbol

What is still missing:
- call graph tracing
- import/use-site analysis
- stronger structural relationship scoring

Current code:
- [service.go](/home/andrii/Projects/ClaudeReverse/implementation/projectkitty/internal/intelligence/service.go)

### I. Semantic Depth

Target:
- code-item extraction good enough to act reliably
- less lexical dependence in ranking

Status:
- `Partially matched`

Why:
- current ranking is better than naive keyword search
- but still mostly lexical + structural, not deeply semantic

What is still missing:
- stronger declaration-aware scoring
- better cross-file signal usage
- less body-text overlap dependence

## 3. What Whiskers Already Gets Right

These are the parts that are solid enough to keep:

- search before parse
- ignore-aware discovery
- multi-pass narrowing
- syntax-aware outlines for supported languages
- focused symbol reads
- explicit no-match behavior
- explicit search/outline/read stages in the agent loop
- tests around positive and no-match behavior

This is enough to call Article 2 real, not aspirational.

## 4. What Is Still Weak

These are the remaining Article 2 weaknesses worth fixing later.

### 1. Ranking is still shallow

Problem:
- strong file/symbol guesses still depend too much on lexical overlap

Desired direction:
- more declaration-aware ranking
- more relationship-aware scoring

### 2. Cross-file reasoning is light

Problem:
- nearby related files are useful, but the subsystem still does not trace real program structure

Desired direction:
- imports
- references
- local caller/callee evidence

### 3. Unsupported languages still degrade hard

Problem:
- unsupported files still rely on regex fallback

Desired direction:
- expand grammar-backed support only where it adds real value

### 4. Tool boundaries are good, but not fully mature

Problem:
- search/outline/read are visible stages, but not yet as independently reusable as mature Codex-style handlers

Desired direction:
- preserve the current interface
- keep tightening the boundaries internally

### 5. Event model is useful, but still simple

Problem:
- stages are visible, but the event model is still thinner than Gemini’s typed state machine

Desired direction:
- richer stage typing
- cleaner machine-readable status transitions

## 5. Next Changes To Make In Code

If Whiskers gets another Article 2 pass, the order should be:

### 1. Improve ranking without changing the public interface

Do:
- shift more weight to symbol names and declaration evidence
- use related-file evidence earlier in scoring
- reduce reliance on body-text token matches

Why:
- biggest quality gain for the least architectural churn

### 2. Strengthen cross-file relationship tracing

Do:
- add lightweight import/reference-based relationship hints
- keep it local and cheap

Why:
- this is the weakest part of current retrieval quality

### 3. Expand syntax-aware support selectively

Do:
- add grammar-backed support only for languages that appear often in target repos

Why:
- avoid over-engineering parser coverage for its own sake

### 4. Tighten typed event/state boundaries

Do:
- keep search/outline/read state explicit
- make event output easier to consume programmatically

Why:
- this brings Whiskers closer to Gemini/Codex cleanliness without changing its behavior

## 6. What Not To Do

Do not:
- replace the current tool shape with one opaque `Scan`
- parse the whole repo before the first action
- broaden Tree-sitter support just to claim universality
- add planner-like reasoning into the intelligence layer
- hide no-match outcomes behind plausible but weak reads

## 7. Final Call

Whiskers is already a valid Article 2 subsystem.

Its current standing is:
- architecture: correct
- interface: mostly correct
- depth: useful but still limited
- next work: quality improvements, not redesign

So the practical answer is:
- keep the `Search -> Outline -> ReadSymbol` shape
- improve ranking and relationship evidence
- keep parser depth growing behind the same interface
- do not reopen the architectural decision unless the research changes

