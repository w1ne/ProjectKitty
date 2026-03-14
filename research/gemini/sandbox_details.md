# Gemini CLI Sandbox Details

## Sandbox Types

> **Updated v0.33.0**: gVisor and LXC/LXD support added; Podman support added alongside Docker.

| Type | `GEMINI_SANDBOX` value | Platform | Mechanism |
|---|---|---|---|
| **macOS Seatbelt** | `sandbox-exec` | macOS | Apple `sandbox-exec` + `.sb` profile |
| **Docker** | `docker` | Linux/macOS | Container image (versioned with CLI) |
| **Podman** | `podman` | Linux/macOS | Podman container (drop-in Docker alternative) |
| **gVisor (runsc)** | `runsc` | Linux | User-space Go kernel; intercepts all syscalls; requires Docker + gVisor runtime |
| **LXC/LXD** | `lxc` | Linux (experimental) | Full-system container with systemd/snapd; for tools that require full OS |
| **None (host)** | unset | All | Direct host execution |

**Activation precedence** (highest → lowest):
1. CLI flag `-s` / `--sandbox`
2. Environment variable `GEMINI_SANDBOX`
3. Settings file `settings.json` → `"sandbox"` field

## Sandbox Re-entry Flow

The CLI detects `!process.env['SANDBOX']` on startup. If sandboxing is configured:
1. Auth is completed **before** re-entry (OAuth web redirect breaks inside sandbox).
2. stdin data is injected into args (`--prompt <stdinData>\n\n<original-prompt>`).
3. `start_sandbox()` relaunches the process inside the environment.
4. After sandbox process exits, `runExitCleanup()` finalizes in the outer process.

## macOS Seatbelt

Profile selection via `SEATBELT_PROFILE` env var (default: `permissive-open`).

Built-in profiles live in `src/patches/`:
```
sandbox-macos-permissive-open.sb
sandbox-macos-<other-built-ins>.sb
```

Custom profiles: place `sandbox-macos-<name>.sb` in `.gemini/` of the project.

Injected `sandbox-exec -D` variables:
| Variable | Value |
|---|---|
| `TARGET_DIR` | `realpath(cwd)` |
| `TMP_DIR` | `realpath(os.tmpdir())` |
| `HOME_DIR` | `realpath(homedir())` |
| `CACHE_DIR` | `getconf DARWIN_USER_CACHE_DIR` |
| `INCLUDE_DIR_0..4` | Up to 5 additional workspace directories |

`BUILD_SANDBOX` env var is blocked inside seatbelt (causes `FatalSandboxError`).

## Docker / Podman Sandbox

- Image: `us-docker.pkg.dev/gemini-code-dev/gemini-cli/sandbox:<version>` (versioned with CLI).
- Image version originally `0.27.3`, kept in sync with `config.sandboxImageUri` in `package.json`.
- Network: `SANDBOX_NETWORK_NAME` (isolated Docker network).
- Proxy: `SANDBOX_PROXY_NAME` — internal network proxy routing.
- User: `shouldUseCurrentUserInSandbox()` — may run as the current host user inside the container.
- Entry: `entrypoint` from `sandboxUtils.js`.
- Ports: Managed via `ports` from `sandboxUtils.js`.
- Podman works as a drop-in replacement (same flags/behavior as Docker mode).

## gVisor (runsc) Sandbox *(added v0.31.0+)*

- Strongest isolation: all container syscalls intercepted and handled by a sandboxed Go kernel.
- Requires: Linux + Docker + gVisor runtime (`runsc`) installed.
- Use case: Maximum security for untrusted code execution.
- Overhead: Higher than plain Docker due to syscall interception.

## LXC/LXD Sandbox *(experimental, added v0.31.0+)*

- Full-system Linux containers: runs `systemd`, `snapd`, etc.
- Designed for tools that don't function in standard Docker containers (e.g. Snapcraft, Rockcraft).
- Requires: Linux + LXD installed.
- Status: Experimental — not recommended for general use.

## SandboxConfig Loading

`loadSandboxConfig(settings, argv)` resolves the config from:
1. CLI flags (`--sandbox`)
2. Settings (`settings.security.sandbox`)
3. If null → no sandbox, run on host.

## Sandbox vs Claude Code / Codex
- Claude Code: macOS seatbelt only (`sandbox-exec`).
- Codex: Linux `bubblewrap` with explicit policy definitions.
- Gemini CLI: macOS seatbelt + Docker/Podman + gVisor + LXC — **widest sandbox coverage** of the three, including the strongest isolation option (gVisor).
