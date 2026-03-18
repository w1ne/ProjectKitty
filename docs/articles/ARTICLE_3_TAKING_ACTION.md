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

That gap — from buffered `os/exec` to PTY-backed execution — is what this article closes. We are building **Claws**, the execution layer for ProjectKitty. Its job is to run commands safely, observe output correctly, and not let one long-running job freeze everything else.

But before we write code, it's worth looking at what the three most mature CLI agents already learned the hard way.

---

## 1. What Claude Code, Codex, and Gemini Do

The execution layer is where the biggest differences between agents show up. Here is what each one actually implements.

### Claude Code — PTY + UUID Correlation + Environment Sterilization

Claude Code does not call `exec`. It uses `node-pty` to spawn every command inside a pseudoterminal, for the same reason we're building Claws. What makes its approach notable is the extra layers on top of the PTY:

**Environment sterilization.** Before the child process starts, Claude snapshots its environment and strips internal variables — API keys, internal credential paths, agent control variables. The subprocess gets a clean environment that can't accidentally leak or misuse the parent's credentials.

**`TERM_PROGRAM=claude-code`.** Claude sets this in every subprocess. Tools that check it can suppress heavy animations, disable interactive prompts, or change their output format to be more machine-readable. It's a signal: *you are running under supervision, behave accordingly*.

**UUID-tagged output.** Every command execution gets a `latestBashOutputUUID` — a random identifier that is injected into the output stream and matched against the model's expected response. This prevents a subtle class of bugs where the model confuses output from a previous turn with the current one. When a command takes thirty seconds and the model's next turn arrives before it finishes, the UUID ensures the observation is correctly associated.

**`isBashSecurityCheckForMisparsing`.** Before execution, Claude scans the generated command string for patterns that suggest injection — multi-command chains where one segment looks like it came from external input, variable expansions into sub-shells (`$()`), and similar constructions. The model is capable; it can still be tricked by a sufficiently crafted repository file. This check runs regardless.

### Codex — Streaming Lifecycle + Bubblewrap Sandbox

Codex's clearest architectural signal is visible in its event names: `exec_command_begin`, `exec_command_output_delta`, `exec_command_end`. Command execution is not a call that returns a result — it's a lifecycle with typed state transitions. Every observer (the UI, the logger, the policy layer) can subscribe to these events independently.

The second notable piece is bubblewrap. On Linux, Codex runs commands inside `bwrap` — the same userspace container tool used by Flatpak and Chromium's sandbox. The command gets a restricted filesystem view and network policy. The agent can read and write within the workspace and run builds, but cannot touch paths it shouldn't. If the model is tricked into running `cat /etc/shadow`, the OS enforces the restriction; the agent reports what happened; no separate blocklist is needed.

Codex also maintains an `exec_command_failed: ` and `write_stdin failed: ` error surface. The `write_stdin` capability in particular means Codex can respond to interactive prompts — it knows when a command is waiting for input and can write to its stdin. Buffered `os/exec` cannot do this at all.

### Gemini — Process Group Tracking + Inactivity Timeout + Command Splitting

Gemini's `ShellTool` wraps every command before it runs:

```bash
{ <user-command> }; __code=$?; pgrep -g 0 >${tempFile} 2>&1; exit $__code;
```

After the user's command exits, this snippet writes every PID in the process group to a temp file. Gemini reads that file and can kill all of them — not just the immediate child. This solves the process group leak that `os/exec` leaves behind when `bash -lc "make all"` spawns a compiler that ignores SIGTERM.

**Inactivity timeout.** Gemini tracks the last time each command produced output. If no bytes arrive for a configurable duration, it kills the process and returns a timeout error. A hanging `npm install` waiting on a registry that never responds won't keep the agent loop frozen forever.

**Command splitting for policy.** The string `git add . && git commit -m "msg"` contains two distinct operations: a safe read-only file staging and a write that modifies repository history. Gemini's `splitCommands()` breaks the chain on `&&`, `||`, and `;` and evaluates each segment independently against the policy engine. A single "allow git" rule should not implicitly allow `git push --force`.

**Redirection detection.** Any command containing `>` or `>>` is automatically downgraded to require user confirmation in every mode except `YOLO`. Output redirection can clobber files; that is a write operation even if the command itself is read-only.

---

## 2. The PTY Execution Path

We add PTY support using `github.com/creack/pty`. The execution path for shell commands changes from "buffer everything, return at exit" to "stream everything, return at exit."

The upgraded shell runner:

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

    go func() {
        scanner := bufio.NewScanner(pt)
        for scanner.Scan() {
            line := scanner.Text()
            buf.WriteString(line + "\n")
            if call.Stream != nil {
                call.Stream(execID, line)
            }
        }
        done <- scanner.Err()
    }()

    inactivity := time.NewTimer(r.policy.InactivityTimeout)
    defer inactivity.Stop()

    for {
        select {
        case <-ctx.Done():
            _ = cmd.Process.Kill()
            return Result{}, ctx.Err()
        case <-inactivity.C:
            _ = cmd.Process.Kill()
            return Result{}, fmt.Errorf("command timed out after %s with no output", r.policy.InactivityTimeout)
        case err := <-done:
            _ = err // EOF is normal
            goto wait
        case <-outputArrived: // reset timer on each line
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

Five things changed from the buffered version:

1. **PTY allocation.** `pty.Start` opens the pseudoterminal. The process believes it has a terminal.
2. **Environment sterilization.** `sterilizeEnv` strips internal agent variables before the child process starts. `KITTY_SHELL=1` tells tools they're running under supervision.
3. **UUID tagging.** `execID` is generated per execution and travels with the `Result`. The model's observation is always tied to a specific run, not to whatever the most recent output happened to be.
4. **Inactivity timeout.** The timer resets on every output line. A process that goes silent for longer than `InactivityTimeout` is killed, not waited on forever.
5. **Context cancellation.** `ctx.Done()` kills the process mid-run. Long builds become cancellable.

---

## 3. The Permission Gate

Before any command runs, it passes through `checkPolicy`. This is the boundary between the agent and the system. Getting it wrong in either direction is expensive: too loose and the agent deletes something; too tight and it can't do useful work.

The current `Policy` struct:

```go
type Policy struct {
    ApprovalMode      string
    AllowedCommands   []string
    AllowDestructive  bool
    InactivityTimeout time.Duration
}
```

`checkPolicy` enforces three layers, informed by what we learned from the production tools:

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

**Layer 2: Redirection detection.** Any segment containing `>` or `>>` is blocked in non-`yolo` modes unless explicitly approved. A command that writes to disk is a write operation regardless of what runs before the redirect.

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

Three modes cover the common cases:

| Mode | Shell | Redirection | Destructive |
|------|-------|-------------|-------------|
| `manual` | Allowlist only | Blocked | Blocked |
| `auto` | Read-only commands free | Blocked | Blocked |
| `yolo` | Everything | Allowed | Allowed |

The gap between Claws and the production tools is worth naming: Gemini persists "always allow" rules to `~/.gemini/policies/` so they survive session restart. Codex stores `approval_mode` per thread in SQLite. Claws currently rebuilds policy from config on each run. Policy persistence is the next step in this layer's evolution.

---

## 4. Streaming Output and the Event Channel

The agent loop already communicates through a channel of typed `Event` values:

```go
func (a *Agent) Run(ctx context.Context, input RunInput) <-chan Event
```

The UI reads from this channel and renders each event as it arrives. But the original `ActionRunCommand` handler emits a single `EventObserved` after the runtime returns — which only happens when the command finishes.

For a `go test ./...` run that takes thirty seconds, the user sees nothing until it's done.

The fix threads a streaming callback through the runtime so output lines become events as they arrive. The callback carries the `execID` so the observer can verify which run produced a given line:

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

Now the UI renders test output, build progress, and compiler errors line by line. A failing test is visible the moment it fails, not after the full suite finishes.

This mirrors Codex's `exec_command_output_delta` event model: execution is a lifecycle, not a call that returns a value. The UI subscribes to events; the policy layer subscribes to events; the session logger subscribes to events. They all see the same stream.

---

## 5. Concurrent Jobs

The agent loop runs one action at a time. That is intentional: the planner can't reason about two things simultaneously, and most tasks are sequential. But builds and test suites are not.

When the planner issues `go test ./...`, it shouldn't block the agent from issuing `go vet ./...` while the tests run. These two are independent.

Go's concurrency model handles this naturally. The runtime exposes an `ExecuteAsync` method that returns immediately with a handle:

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

## 6. What the Runtime Looks Like Now

Let's trace a complete execution. The planner decides to run `go test ./...`:

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
  ↓
  execID = "7f3a2b..."
  pty.Start(bash -lc "go test ./...")
    → process sees a real terminal
    → TERM_PROGRAM tells test runner to suppress animations
    → output arrives incrementally
    → Stream("7f3a2b...", line) → EventObserved events
    → inactivity timer resets on each line
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

**Process group leaks.** Gemini wraps every command with `pgrep -g 0` to collect all spawned PIDs. Claws doesn't do this yet. If `bash -lc "make all"` spawns a compiler that spawns subprocesses, killing the bash process leaves those subprocesses running. The fix is to send `SIGKILL` to the entire process group (`-cmd.Process.Pid`), not just the parent — a one-line change that is missing from the current implementation.

**No sandbox.** Claude has `sandbox-exec` on macOS. Codex has bubblewrap on Linux. Claws runs on the host. The policy gate is defense-in-depth, not isolation. Until we add bubblewrap support, a sufficiently crafted repository file could trick the agent into running something outside the workspace. The blocklist is not a sandbox.

**Policy doesn't persist.** "Always allow `go test`" resets on restart. Gemini writes rules to `~/.gemini/policies/`; Codex stores `approval_mode` per thread in SQLite. Claws rebuilds from config on each run. This means users will be prompted for the same approvals every session until we add a persistence layer — which is part of the Article 4 work anyway.

**Fragment matching is naive.** `python script.py` passes the blocklist even if `script.py` deletes files. Defaulting to `manual` mode contains this: nothing runs without explicit allowlist entry. But it is not the same as Codex's bubblewrap, which enforces the restriction at the OS level regardless of what the agent decides.

---

## What's Next

ProjectKitty can now read a codebase and run commands against it safely. A session is becoming possible — a real back-and-forth between the planner, the code reader, and the execution layer.

But each session starts fresh. There is no record of what the agent found last time, no way to resume a task that was interrupted, and no way to accumulate project-specific facts across runs.

Article 4 will build the memory layer: session logs in JSONL, durable project facts in a structured store, and the compaction step that converts a long session into a short summary the next run can start from. That is also where policy persistence lands — the `approval_mode` and allowlist entries that today live only in config.

---

*Article 3 is live. Implementation in progress: [github.com/w1ne/ProjectKitty](https://github.com/w1ne/ProjectKitty) Follow Entropora, Inc for Article 4.*
