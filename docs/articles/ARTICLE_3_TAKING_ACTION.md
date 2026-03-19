# Taking Action: PTYs, Safe Bash, and Long-Running Tasks

In Article 2, we gave ProjectKitty the ability to read code without drowning the model in noise. Whiskers can locate a function across a 50,000-file repository and hand the planner 300 tokens instead of 29,000.

Now the planner needs to *do* something with what it found.

---

Task: *run the test suite, then check whether the build still passes*.

Let's run it both ways.

**The naive approach:** Pass the command string to `exec.Command`, collect stdout and stderr into buffers, and return when the process exits.

```go
cmd := exec.Command("bash", "-lc", "go test ./...")
var stdout, stderr bytes.Buffer
cmd.Stdout = &stdout
cmd.Stderr = &stderr
err := cmd.Run()
```

This works fine for `go test ./...` on a green repo. But the moment you run something interactive — `git log` with a pager, `npm install` with a missing lock file, `python setup.py develop` asking for a password — the process stalls waiting for input that will never arrive. You get no output, no error, and a context deadline that eventually kills it. The planner has no idea why it failed.

**The deeper problem:** Most terminal programs don't write to stdout the way a library function does. They check whether their output is attached to a real terminal. When it isn't, they either buffer aggressively, suppress color codes, disable interactive prompts, or change behavior entirely. The subprocess has no terminal, so it acts differently than it does when a human runs it.

**The PTY-backed approach:** Allocate a pseudoterminal, connect the subprocess to its slave end, and read from the master end in a goroutine. The process believes it is talking to a real terminal. Prompts appear. Progress bars render. Pagers open and close. Output arrives incrementally so the UI can show it as it happens.

```text
Parent process
  └── master PTY fd  ────────────────────────────────┐
                                                      │ reads output line by line
  └── child process (bash -c "go test ./...")         │
        └── stdin/stdout/stderr → slave PTY fd ───────┘
```

Same answer. Correct behavior. No frozen process.

That gap — from buffered `os/exec` to PTY-backed execution — is what this article is about. We are building **Claws**, the execution layer for ProjectKitty. Its job is to run commands safely, observe output correctly, and not let one long-running job freeze everything else.

The current prototype does not fully implement that runtime yet. This article mixes two things on purpose:

- what exists in the repo today
- what the next runtime iteration should look like

But before we write code, it's worth looking at what the three most mature CLI agents already learned the hard way.

---

## 1. What Claude Code, Codex, and Gemini Do

The execution layer is where the biggest differences between agents show up. Here is what each one actually implements, with evidence levels noted: **Source** (read directly from binary or unminified source), **Benchmark** (observed behavior), **Inferred** (engineering conclusion).

### Claude Code — PTY + UUID Correlation + Environment Sterilization

**Source.** Claude Code does not call `exec`. It uses `node-pty` to spawn every command inside a pseudoterminal — confirmed by direct string evidence in the 225 MB native binary. What makes its approach notable is the extra layers on top of the PTY:

**Environment sterilization.** Before the child process starts, Claude snapshots its environment (`~/.claude/shell-snapshots/` exists on disk for this purpose) and strips internal variables — API keys, internal credential paths, agent control variables. The subprocess gets a clean environment that can't accidentally leak or misuse the parent's credentials.

**`TERM_PROGRAM=claude-code`.** Claude sets this in every subprocess. Tools that check it can suppress heavy animations, disable interactive prompts, or change their output format to be more machine-readable. It's a signal: *you are running under supervision, behave accordingly*.

**UUID-tagged output.** Every command execution gets a `latestBashOutputUUID` — a random identifier injected into the output stream and matched against the model's expected response. This prevents a subtle class of bugs where the model confuses output from a previous turn with the current one. When a command takes thirty seconds and the model's next turn arrives before it finishes, the UUID ensures the observation is correctly associated.

**SHA256 permission hashing.** The permission system hashes approved command patterns. Each permission mode (`default`, `auto`, `bypassPermissions`) gates tool calls before any OS call is made. This is agent-layer interception: in `don't ask` mode, even a `Read` call is denied before it reaches the filesystem — **Benchmark** shows no output file was produced in the safety boundary test. The cost is that bypassing the mode check bypasses the safety system entirely.

**`isBashSecurityCheckForMisparsing`.** Before execution, Claude scans the generated command string for injection patterns — multi-command chains where one segment looks like it came from external input, variable expansions into sub-shells (`$()`), and similar constructions. The model is capable; it can still be tricked by a sufficiently crafted repository file. This check runs regardless.

### Codex — OS-Layer Enforcement + Streaming Lifecycle + Bubblewrap

Codex's clearest architectural signal is visible in its event names: `exec_command_begin`, `exec_command_output_delta`, `exec_command_end`. Command execution is not a call that returns a result — it's a lifecycle with typed state transitions. Every observer (the UI, the logger, the policy layer) can subscribe to these events independently.

**OS-layer vs agent-layer safety.** This is the key distinction from Claude. **Benchmark:** In a safety boundary test, Codex attempted `cat /etc/shadow`, received `Permission denied` from the OS, and then successfully wrote `safety_report.md` documenting what happened. The agent reached the filesystem; the OS said no; the agent reported it. Write was not blocked at the agent layer. The bubblewrap sandbox (`bwrap`) on Linux provides explicit, inspectable containment — the command gets a restricted filesystem view and the OS enforces it. No separate blocklist is needed because the kernel is the enforcer.

**Explicit agent lifecycle.** Codex exposes `spawn_agent → resume_agent → send_message → close_agent` as first-class operations. Each agent is an object with an ID. Context forking allows a sub-agent to inherit parent conversation history. The orchestration is the public interface, not an implementation detail.

**`write_stdin` capability.** Codex maintains an explicit `write_stdin` path alongside `exec_command_failed`. This means Codex can respond to interactive prompts — it knows when a command is waiting for input and can write to its stdin. Buffered `os/exec` cannot do this at all.

### Gemini — Full Policy Engine + Widest Sandbox Coverage

Gemini has the most layered execution safety architecture of the three. Every tool invocation travels through a typed decision pipeline:

```
tool invocation
  → shouldConfirmExecute()
  → PolicyEngine.getDecision()    [via MessageBus — 30-second timeout]
  → ALLOW | DENY | ASK_USER
  → if ASK_USER: getConfirmationDetails() → typed UI
  → user: confirm / deny / always allow
  → if ProceedAlwaysAndSave → persist rule to ~/.gemini/policies/ (JSON or TOML)
```

The `PolicyEngine` evaluates rules with four fields: `toolName` (exact match or `serverName__*` wildcard for MCP), `argsPattern` (regex against JSON-stringified args), `modes[]` (rule applies only in specified modes), and `priority` (explicit ordering). Rules persist across restarts — "always allow `npm test`" survives a session restart.

**Four `ApprovalMode`s, not three.** Unlike most agents that offer two or three modes, Gemini has four:

| Mode | Behavior |
|------|----------|
| `DEFAULT` | Ask for most operations |
| `PLAN` | Read-only tools only — enforced by prompt rewrite |
| `AUTO_EDIT` | Auto-approve file edits, ask for shell |
| `YOLO` | Approve everything |

`PLAN` mode does something unusual: it physically rewrites the system prompt to inject a 5-phase sequential workflow and registers a hardcoded `PLAN_MODE_DENIAL_MESSAGE` in the tool scheduler for any write attempt. This is behavioral gating via prompt engineering on top of a policy check — not just a permission flag.

**Shell command policy.** `checkShellCommand()` runs the same command-splitting Gemini is known for, but the policy context is richer:

```bash
{ <user-command> }; __code=$?; pgrep -g 0 >${tempFile} 2>&1; exit $__code;
```

After the user's command exits, this snippet writes every PID in the process group to a temp file. Gemini reads that file and kills all of them — not just the immediate child. Any command containing `>` or `>>` is automatically downgraded to require confirmation in all modes except `YOLO`. Deceptive URLs in tool confirmations trigger an explicit warning in the UI (added v0.31.0).

**Widest sandbox coverage.** Gemini is the only agent in this set with cross-platform container support:

| Sandbox | `GEMINI_SANDBOX` | Platform | Mechanism |
|---------|-----------------|----------|-----------|
| macOS Seatbelt | `sandbox-exec` | macOS | Apple `sandbox-exec` + customizable `.sb` profiles |
| Docker | `docker` | Linux/macOS | Versioned container image |
| Podman | `podman` | Linux/macOS | Drop-in Docker alternative |
| gVisor | `runsc` | Linux | User-space Go kernel — intercepts all syscalls |
| LXC/LXD | `lxc` | Linux | Full-system container (experimental) |

gVisor is the strongest isolation: the container runs inside a user-space Go kernel that intercepts every syscall before it reaches the real kernel. No other tool in this set offers an equivalent. Claude Code has macOS seatbelt only. Codex has Linux bubblewrap. Neither is cross-platform. Claws currently has none.

**`ToolConfirmation` hooks with typed serialized fields.** External processes can intercept every tool decision. An `exec` hook receives `{command, rootCommand}`; an `edit` hook receives `{fileName, filePath, fileDiff, originalContent, newContent, isModifying}`. This enables external governance scripts to implement arbitrary approval logic without modifying the agent.

---

## 2. The PTY Execution Path

The next runtime iteration should add PTY support using `github.com/creack/pty`. That changes shell execution from "buffer everything, return at exit" to "stream everything, return at exit."

Target shell runner:

```go
func (r *Runtime) runShell(ctx context.Context, call Call) (Result, error) {
    if err := r.checkPolicy(call.Command); err != nil {
        return Result{}, err
    }

    cmd := exec.CommandContext(ctx, "bash", "-lc", call.Command)
    cmd.Dir = call.Workspace

    // Sterilize: remove agent-internal variables from the subprocess environment.
    cmd.Env = sterilizeEnv(os.Environ())
    // Signal to tools that they're running under agent supervision.
    cmd.Env = append(cmd.Env, "KITTY_SHELL=1", "TERM_PROGRAM=projectkitty")

    // Create a new process group so we can kill all descendants, not just the shell.
    cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

    started := time.Now().UTC()
    pt, err := pty.Start(cmd)
    if err != nil {
        return Result{}, fmt.Errorf("start pty: %w", err)
    }
    defer pt.Close()

    _ = pty.Setsize(pt, &pty.Winsize{Rows: 40, Cols: 220})

    execID := newExecID() // UUID for this execution
    var buf bytes.Buffer
    done := make(chan error, 1)
    outputArrived := make(chan struct{}, 1)

    go func() {
        scanner := bufio.NewScanner(pt)
        for scanner.Scan() {
            line := scanner.Text()
            buf.WriteString(line + "\n")
            if call.Stream != nil {
                call.Stream(execID, line)
            }
            // Non-blocking signal: inactivity timer resets on each line.
            select {
            case outputArrived <- struct{}{}:
            default:
            }
        }
        done <- scanner.Err()
    }()

    inactivity := time.NewTimer(r.policy.InactivityTimeout)
    defer inactivity.Stop()

    for {
        select {
        case <-ctx.Done():
            _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // kill process group
            return Result{}, ctx.Err()
        case <-inactivity.C:
            _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
            return Result{}, fmt.Errorf("command timed out after %s with no output", r.policy.InactivityTimeout)
        case <-done:
            goto wait
        case <-outputArrived:
            inactivity.Reset(r.policy.InactivityTimeout)
        }
    }

wait:
    exitCode := 0
    if err := cmd.Wait(); err != nil {
        var exitErr *exec.ExitError
        if errors.As(err, &exitErr) {
            exitCode = exitErr.ExitCode()
        }
    }

    output := stripANSI(strings.TrimSpace(buf.String()))
    ended := time.Now().UTC()

    return Result{
        Tool:      ToolShell,
        ExecID:    execID,
        Summary:   buildSummary(call.Command, exitCode, output),
        Output:    output,
        ExitCode:  exitCode,
        StartedAt: started,
        EndedAt:   ended,
    }, nil
}
```

Six things would change from the naive version:

1. **PTY allocation.** `pty.Start` opens the pseudoterminal. The process believes it has a terminal.
2. **New process group.** `Setpgid: true` puts the child in its own process group. Killing `-cmd.Process.Pid` kills the group — not just the shell. This is Gemini's approach to process group leaks, applied at the `SysProcAttr` level rather than a post-exit `pgrep` scan.
3. **Environment sterilization.** `sterilizeEnv` strips internal agent variables before the child process starts. `KITTY_SHELL=1` tells tools they're running under supervision.
4. **UUID tagging.** `execID` is generated per execution and travels with the `Result`. The model's observation is always tied to a specific run, not to whatever the most recent output happened to be.
5. **Inactivity timeout.** `outputArrived` is a buffered channel written to (non-blocking) on each output line. The timer resets on each write. A process that goes silent for longer than `InactivityTimeout` is killed via process group signal, not just the immediate child.
6. **Context cancellation.** `ctx.Done()` kills the entire process group mid-run. Long builds become cancellable.

---

## 3. The Permission Gate

Before any command runs, it passes through `checkPolicy`. This is the boundary between the agent and the system. Getting it wrong in either direction is expensive: too loose and the agent deletes something; too tight and it can't do useful work.

The current prototype has a smaller `Policy` struct. The next useful shape is:

```go
type Policy struct {
    ApprovalMode      string
    AllowedCommands   []string
    AllowDestructive  bool
    InactivityTimeout time.Duration
}
```

In the target design, `checkPolicy` should enforce three layers, informed by what we learned from the production tools:

**Layer 1: Command splitting.** Borrowed from Gemini's `splitCommands()`. Before any other check, `checkPolicy` breaks the command on `&&`, `||`, and `;` and evaluates each segment independently. `go test ./... && git push` is two operations with different risk profiles.

```go
func (r *Runtime) checkPolicy(command string) error {
    for _, segment := range splitCommands(command) {
        if err := r.checkSegment(segment); err != nil {
            return err
        }
    }
    return nil
}
```

**Layer 2: Redirection detection.** Any segment containing `>` or `>>` is blocked in non-`yolo` modes. Borrowed from Gemini — a command that writes to disk is a write operation regardless of what runs before the redirect.

**Layer 3: Destructive pattern matching.** If `AllowDestructive` is false, a blocklist of dangerous fragments — `rm`, `git reset`, `git clean`, `sudo`, `chmod` — blocks commands containing them.

```go
func (r *Runtime) checkSegment(segment string) error {
    if hasRedirection(segment) && r.policy.ApprovalMode != "yolo" {
        return fmt.Errorf("command requires approval: output redirection detected in %q", segment)
    }
    if !r.policy.AllowDestructive {
        for _, fragment := range destructiveFragments {
            if strings.Contains(" "+segment, fragment) {
                return fmt.Errorf("command blocked by runtime policy: %s", segment)
            }
        }
    }
    for _, allowed := range r.policy.AllowedCommands {
        if strings.TrimSpace(segment) == allowed {
            return nil
        }
    }
    return fmt.Errorf("command requires approval in %s mode: %s", r.policy.ApprovalMode, segment)
}
```

### Approval Modes

Three modes cover the common cases. Gemini has four — the missing one is `PLAN`, which rewrites the system prompt to physically prevent the model from requesting write operations. We don't implement that yet, but it is the right direction for a future "read-only audit" mode.

| Mode | Shell | Redirection | Destructive |
|------|-------|-------------|-------------|
| `manual` | Allowlist only | Blocked | Blocked |
| `auto` | Read-only commands free | Blocked | Blocked |
| `yolo` | Everything | Allowed | Allowed |

The gap between Claws and the production tools is worth naming directly:

- **Gemini** persists "always allow" rules to `~/.gemini/policies/` (JSON or TOML) with per-tool `argsPattern` regex matching. Rules survive restarts. Users are never prompted for the same approval twice.
- **Codex** stores `approval_mode` per thread in SQLite with read-repair upsert logic.
- **Claws** rebuilds policy from config on each run. Users will be re-prompted every session until we add a persistence layer — which is part of the Article 4 work anyway.

---

## 4. Streaming Output and the Event Channel

The agent loop already communicates through a channel of typed `Event` values:

```go
func (a *Agent) Run(ctx context.Context, input RunInput) <-chan Event
```

The UI reads from this channel and renders each event as it arrives. But the original `ActionRunCommand` handler emits a single `EventObserved` after the runtime returns — which only happens when the command finishes.

For a `go test ./...` run that takes thirty seconds, the user sees nothing until it's done.

The next fix is to thread a streaming callback through the runtime so output lines become events as they arrive. The callback carries the `execID` so the observer can verify which run produced a given line:

```go
type StreamFn func(execID string, line string)

type Call struct {
    Tool      Tool
    Workspace string
    Command   string
    Path      string
    Symbol    string
    Limit     int
    Stream    StreamFn
}
```

The agent passes a `StreamFn` that converts each line into a progress event:

```go
case ActionRunCommand:
    events <- newEvent(EventAction, step, "Running", cmd)
    call := runtime.Call{
        Tool:      runtime.ToolShell,
        Workspace: input.Workspace,
        Command:   cmd,
        Stream: func(execID, line string) {
            events <- newEvent(EventObserved, step, execID, line)
        },
    }
    result, err := a.runtime.Execute(ctx, call)
```

With that change, the UI can render test output, build progress, and compiler errors line by line. A failing test becomes visible the moment it fails, not after the full suite finishes.

This mirrors Codex's `exec_command_output_delta` event model: execution is a lifecycle, not a call that returns a value. The UI subscribes to events; the policy layer subscribes to events; the session logger subscribes to events. They all see the same stream.

---

## 5. Writing and Editing Files

Reading is not enough. An agent that can only observe cannot fix anything. The next capability after running commands is writing files.

### Atomic Write

`ToolWriteFile` does not open the target path and write into it. It writes content to a temp file in the same directory, then calls `os.Rename` to move it into place. `Rename` is atomic on the same filesystem: no reader ever sees a partial file — the path either points to the old content or the new content, never to a truncated or partially written version. Directories along the path are created automatically with `os.MkdirAll`. A trailing newline is enforced before the write: text files without one cause spurious diff noise in `git diff` and `git show`. `safePath` validation rejects any target path that resolves outside the workspace root, so the tool cannot escape its sandbox via `../` traversal.

### 3-Tier Edit Matching

`ToolEditFile` performs in-place edits. The caller supplies `old_string` and `new_string`; the tool locates `old_string` in the file and replaces it. The challenge is that models frequently generate `old_string` with slightly wrong indentation — close enough for a human to understand, wrong enough for a literal string match to fail.

Claws uses Gemini's 3-tier fallback strategy:

```
old_string, new_string
        │
        ▼
Tier 1 — Exact match
  strings.Count(content, oldStr) == 1?
  ├─ yes → replace, done
  └─ no (0 or 2+ occurrences) → fall through
        │
        ▼
Tier 2 — Indent-aware match
  strip leading whitespace from each line of old_string
  scan file for a block where stripped lines match
  re-apply actual file indentation to new_string
  ├─ unique match found → replace, done
  └─ no unique match → fall through
        │
        ▼
Tier 3 — Token-flexible match
  tokenize old_string on ()[]{}:=,; and whitespace
  join tokens with [\s\S]*? to form a regex
  compile and search
  ├─ unique match → replace, done
  └─ no unique match → error: cannot locate edit target
```

**Tier 1 (Exact):** `strings.Count(content, oldStr) == 1`. CRLF is normalized to LF before comparison. Fails if the string appears zero times (wrong file or wrong content) or two or more times (ambiguous — the model must supply more context).

**Tier 2 (Indent-aware):** Strip leading whitespace from every line of `old_string`, then scan the file for a contiguous block where the stripped lines match. When found, measure the indentation of the first matched line in the file and re-apply that indentation to every line of `new_string`. This handles the common case where the model copies a code block from context but the context had different indentation than what is actually on disk.

**Tier 3 (Token-flexible):** Tokenize `old_string` by splitting on punctuation and whitespace — `()[]{}:=,;` and any whitespace character. Join the tokens with `[\s\S]*?` and compile as a regex. This tolerates whitespace inconsistencies inside identifiers and expressions (e.g., `foo ( x )` matching `foo(x)`). Used as a last resort because it is the most permissive and therefore the most likely to produce a false match in a large file.

### SHA256 Conflict Detection

The caller can pass an `ExpectedHash` — the SHA256 of the file content it read before deciding what to change. Before applying the edit, `ToolEditFile` hashes the current file content and compares:

```go
if call.ExpectedHash != "" {
    current, err := os.ReadFile(call.Path)
    if err != nil {
        return Result{}, fmt.Errorf("read for hash check: %w", err)
    }
    actual := fmt.Sprintf("%x", sha256.Sum256(current))
    if actual != call.ExpectedHash {
        return Result{}, fmt.Errorf("conflict: file changed since last read (expected %s, got %s)", call.ExpectedHash, actual)
    }
}
```

If the file changed between the read and the edit — because another tool call or goroutine modified it — the edit is rejected with a conflict error rather than silently overwriting the intervening change. The model can then re-read the file and retry with an updated `old_string`.

### Integration with the Planner

`ModelPlanner` exposes `write_file` and `edit_file` as Gemini function-calling tool definitions with typed parameters. The model chooses between them based on intent: `write_file` for new files or full rewrites where the old content doesn't matter; `edit_file` with the shortest `old_string` that uniquely identifies the target location for surgical changes. After either tool, the planner's next generated step is `run_command` — typically `go build ./...` or the relevant test command — to validate that the change compiles and passes.

`DefaultPlanner` (the rule-based fallback) does not initiate writes. It has no model to generate `new_string` content, so initiating a write would be meaningless. It correctly handles the steps that follow a write — running validation commands, recording results to memory — when a model-driven planner initiates the edit and `DefaultPlanner` takes over for the remaining steps.

---

## 6. Concurrent Jobs

The agent loop runs one action at a time. That is intentional: the planner can't reason about two things simultaneously, and most tasks are sequential. But builds and test suites are not.

When the planner issues `go test ./...`, it shouldn't block the agent from issuing `go vet ./...` while the tests run. These two are independent.

Go's concurrency model handles this naturally. One reasonable next step is for the runtime to expose an `ExecuteAsync` method that returns immediately with a handle:

```go
type JobHandle struct {
    Done   <-chan struct{}
    Result func() (Result, error)
}

func (r *Runtime) ExecuteAsync(ctx context.Context, call Call) *JobHandle {
    ch := make(chan struct{})
    var result Result
    var err error

    go func() {
        defer close(ch)
        result, err = r.Execute(ctx, call)
    }()

    return &JobHandle{
        Done:   ch,
        Result: func() (Result, error) { return result, err },
    }
}
```

The agent can fire multiple jobs, continue planning, and `select` on their `Done` channels when it needs results:

```go
select {
case <-testJob.Done:
    testResult, _ := testJob.Result()
case <-vetJob.Done:
    vetResult, _ := vetJob.Result()
case <-ctx.Done():
    return
}
```

Bubble Tea's `tea.Cmd` pattern is designed for this — multiple background goroutines can push events into the channel; the renderer displays them as they arrive.

---

## 7. Target Runtime Flow

The current prototype still uses buffered `exec.CommandContext`. The flow below is the target runtime shape once PTY execution, streaming, and richer policy checks are implemented. The planner decides to run `go test ./...`:

```
Step 4 — ActionRunCommand
  ↓
  checkPolicy("go test ./...")
    → splitCommands: ["go test ./..."]  (one segment, no chain)
    → hasRedirection? no
    → matches destructive fragment? no
    → in AllowedCommands? yes (auto mode)
    → proceed
  ↓
  sterilizeEnv: strip KITTY_API_KEY, internal vars
  append: KITTY_SHELL=1, TERM_PROGRAM=projectkitty
  SysProcAttr{Setpgid: true} → new process group
  ↓
  execID = "7f3a2b..."
  pty.Start(bash -lc "go test ./...")
    → process sees a real terminal
    → TERM_PROGRAM tells test runner to suppress animations
    → output arrives incrementally
    → outputArrived ← struct{}{} on each line (non-blocking)
    → Stream("7f3a2b...", line) → EventObserved events
    → inactivity timer resets on each outputArrived
  ↓
  cmd.Wait()
    → exit code 0: tests passed
    → exit code 1: tests failed; output contains the failure detail
  ↓
  Result{ExecID: "7f3a2b...", ExitCode: 0, Output: "ok  github.com/..."}
  ↓
  EventObserved: "Command finished. ExecID: 7f3a2b..."
```

### Where It Still Gets It Wrong

**No sandbox.** This is the largest gap. Claude has `sandbox-exec` on macOS. Codex has bubblewrap on Linux. Gemini has five sandbox options — including gVisor, which runs the container inside a user-space Go kernel that intercepts every syscall before it reaches the real kernel. Claws runs on the host. The policy gate is defense-in-depth, not isolation. Until we add bubblewrap support, a sufficiently crafted repository file could trick the agent into running something outside the workspace. The blocklist is not a sandbox.

**No policy persistence.** "Always allow `go test`" resets on restart. Gemini writes rules to `~/.gemini/policies/` in JSON or TOML, with `argsPattern` regex matching so "always allow `git commit`" doesn't also allow `git push`. Codex stores `approval_mode` per thread in SQLite with self-healing upsert logic. Claws rebuilds from config on each run. This is the next step in this layer's evolution.

**Fragment matching is naive.** `python script.py` passes the blocklist even if `script.py` deletes files. Defaulting to `manual` mode contains this: nothing runs without explicit allowlist entry. But it is not the same as Codex's bubblewrap or Gemini's gVisor, which enforce restrictions at the OS level regardless of what the agent decides.

**No `write_stdin`.** Codex can respond to interactive prompts mid-execution by writing to the running process's stdin. Claws can't. A command waiting for a yes/no response will hit the inactivity timeout and be killed rather than answered. The PTY gives us the infrastructure for this — the master fd is writable — but the planner has no mechanism yet to decide what to write.

**No conflict-free parallel edits.** `ExpectedHash` detects conflicts after the fact, but two model turns that both read the same file before either writes will each see a clean hash. The second write overwrites the first silently unless the caller explicitly checks the returned conflict error and retries. Preventing this class of bug requires either serializing all edits through a single lock or adopting an optimistic-concurrency protocol at the session level — neither of which is implemented yet.

---

## What's Next

ProjectKitty can now read a codebase, run commands against it safely, and write or edit files in place. File writing and editing are live: atomic writes via temp-file rename, 3-tier edit matching for model-generated patches, and SHA256 conflict detection to catch concurrent modifications. A session is becoming real — a planner that finds a bug, edits the source, and validates the fix without human intervention.

The remaining gaps are policy persistence and sandbox isolation. "Always allow `go test`" resets on restart today. A bubblewrap or gVisor sandbox would let the agent operate with OS-level containment rather than a blocklist.

Article 4 will build the memory layer: session logs in JSONL, durable project facts in a structured store, and the compaction step that converts a long session into a short summary the next run can start from. Policy persistence lands there too — the `approval_mode` and allowlist entries that today live only in config.

---

*Article 3 is live. Implementation in progress: [github.com/w1ne/ProjectKitty](https://github.com/w1ne/ProjectKitty) Follow Entropora, Inc for Article 4.*
