# Security Audit & Fixes

**Date:** 2026-02-13
**Scope:** Full source review of `dgx-spark-cli` (15 Go source files, 2 shell scripts, 1 CI workflow, dependency manifest)

**Malware assessment:** Clean — no embedded backdoors, data exfiltration, obfuscated code, or suspicious network calls detected.

---

## Findings Summary

| Severity | # | Status |
|----------|---|--------|
| HIGH     | 2 | Fixed  |
| MEDIUM   | 2 | Fixed  |
| LOW      | 2 | Fixed  |

---

## HIGH Severity

### 1. Insecure SSH Host Key Verification Fallback

**File:** `internal/ssh/client.go`

**Problem:** When `~/.ssh/known_hosts` did not exist or could not be read, the SSH client silently fell back to `ssh.InsecureIgnoreHostKey()` with only a stderr warning. This accepted any host key without user consent, enabling man-in-the-middle attacks on every first connection.

```go
// BEFORE — silently accepted any host key
hostKeyCallback, err := knownhosts.New(knownHostsPath)
if err != nil {
    fmt.Fprintf(os.Stderr, "Warning: Using insecure host key verification\n")
    hostKeyCallback = ssh.InsecureIgnoreHostKey()
}
```

**Fix:** Replaced with an interactive trust-on-first-use (TOFU) flow that mirrors standard OpenSSH behavior. When `known_hosts` is missing, the user is prompted to trust the host key. Declining aborts the connection. Accepting scans the key via `ssh-keyscan` and creates `known_hosts`. If the file exists but cannot be parsed, the connection is refused with an error.

```go
// AFTER — requires explicit user consent
if _, statErr := os.Stat(knownHostsPath); os.IsNotExist(statErr) {
    fmt.Fprintf(os.Stderr, "known_hosts file not found at %s\n", knownHostsPath)
    fmt.Fprintf(os.Stderr, "Trust host key for %s and create known_hosts? [Y/n]: ", c.config.Host)
    var response string
    fmt.Scanln(&response)
    if response != "" && strings.ToLower(response) != "y" {
        return fmt.Errorf("connection aborted: host key not trusted")
    }
    if err := c.addHostKey(); err != nil {
        return fmt.Errorf("failed to initialize known_hosts: %w", err)
    }
}
hostKeyCallback, err := knownhosts.New(knownHostsPath)
if err != nil {
    return fmt.Errorf("failed to load known_hosts (%s): %w", knownHostsPath, err)
}
```

---

### 2. Command Injection via Unsanitized User Input

**Files:** `cmd/dgx/main.go`, `internal/playbook/ollama.go`, `internal/playbook/vllm.go`, `internal/playbook/nvfp4.go`

**Problem:** User-supplied values (model names, prompts, file paths) were interpolated directly into shell command strings executed over SSH without any quoting or sanitization. An attacker-controlled value containing shell metacharacters (e.g., `'; rm -rf / #`) could break out of the intended command and execute arbitrary code on the remote DGX.

| Location | Vulnerable Code |
|----------|----------------|
| `ollama.go` — `ollamaRun` | `fmt.Sprintf("ollama run %s '%s'", model, prompt)` |
| `nvfp4.go` — `nvfp4Quantize` | `--model_name %s` inside `bash -c "..."` |
| `vllm.go` — `vllmServe` | `vllm serve %s` inside `docker run` |
| `main.go` — `ensureRemoteDirectory` | `mkdir -p %s` with unquoted path |

**Fix:** Introduced a single exported `ssh.ShellQuote()` function that wraps values in single quotes with proper embedded-quote escaping. Applied it at every injection point:

- **`ollama.go`** — model and prompt are now individually shell-quoted:
  ```go
  cmd := fmt.Sprintf("ollama run %s %s", ssh.ShellQuote(model), ssh.ShellQuote(prompt))
  ```
- **`vllm.go`** — model name is shell-quoted in the docker command:
  ```go
  vllm serve %s \  ...`, ssh.ShellQuote(model))
  ```
- **`nvfp4.go`** — restructured to pass the model name as a Docker environment variable (`-e MODEL_NAME=<quoted>`) and switched `bash -c` from double quotes to single quotes, referencing `"$MODEL_NAME"` inside the container to completely eliminate the injection surface:
  ```go
  -e MODEL_NAME=%s \  ...
  bash -c '... --model_name "$MODEL_NAME" ...'`, ssh.ShellQuote(modelName))
  ```
- **`main.go`** — path is now shell-quoted:
  ```go
  client.Execute(fmt.Sprintf("mkdir -p %s", ssh.ShellQuote(path)))
  ```

---

## MEDIUM Severity

### 3. Unconfirmed Remote Script Execution (curl | sh)

**Files:** `internal/playbook/ollama.go`, `internal/playbook/dmr.go`

**Problem:** Two playbook commands downloaded and piped remote scripts directly into a shell without any warning or user confirmation:

| Command | What it ran on the DGX |
|---------|----------------------|
| `dgx run ollama install` | `curl -fsSL https://ollama.com/install.sh \| sh` |
| `dgx run dmr setup` | `curl -fsSL https://get.docker.com \| sudo sh` |

The DMR variant ran with `sudo`, meaning a compromised upstream script would gain root access on the DGX.

**Fix:** Both commands now display a clear warning about what will be downloaded and require explicit `[Y/n]` confirmation before proceeding. Declining cancels the operation cleanly.

```go
// ollama install
fmt.Println("This will download and execute a script from https://ollama.com/install.sh")
fmt.Print("Continue? [Y/n]: ")

// dmr setup
fmt.Println("Warning: This may download and run scripts from https://get.docker.com with sudo.")
fmt.Print("Continue? [Y/n]: ")
```

---

### 4. Unsanitized Model Name in Docker Commands

**File:** `internal/playbook/vllm.go`

**Problem:** The model argument to `vllm serve` was passed directly into a `docker run` command string without sanitization.

**Fix:** Covered by the `ssh.ShellQuote()` fix described in HIGH #2 above. The model name is now properly quoted before interpolation.

---

## LOW Severity

### 5. Duplicate `shellQuote` Function

**Files:** `cmd/dgx/main.go`, `internal/playbook/dmr.go`

**Problem:** Two identical private `shellQuote` functions existed in separate packages. If one were updated (e.g., to fix an edge case) and the other forgotten, inconsistent quoting could reintroduce injection vulnerabilities.

**Fix:** Both copies removed. A single exported `ssh.ShellQuote()` function now lives in `internal/ssh/client.go` and is used by all callers across the codebase.

---

### 6. Hardcoded `--delete` Flag in rsync

**File:** `cmd/dgx/main.go`

**Problem:** The internal `syncDirectoryToRemote` function always passed `--delete` to rsync, which removes files on the remote that don't exist locally. If pointed at the wrong remote path, this could cause unintended data loss with no way for callers to opt out.

```go
// BEFORE — --delete always applied
cmd := exec.Command("rsync", "-az", "--delete", "-e", sshCmd, local, remote)
```

**Fix:** Changed the function signature to accept a `deleteExtraneous bool` parameter. The flag is only added when explicitly requested. The existing call site (`dgx codex import-config`) passes `true` to preserve its current behavior.

```go
// AFTER — caller decides
func syncDirectoryToRemote(localPath, remotePath string, deleteExtraneous bool) error {
    args := []string{"-az", "-e", sshCmd}
    if deleteExtraneous {
        args = append(args, "--delete")
    }
    args = append(args, local, remote)
    cmd := exec.Command("rsync", args...)
    ...
}
```

---

## Files Modified

| File | Changes |
|------|---------|
| `internal/ssh/client.go` | Fixed host key fallback; added exported `ShellQuote()` |
| `cmd/dgx/main.go` | Removed local `shellQuote`; used `ssh.ShellQuote`; quoted path in `ensureRemoteDirectory`; parameterized `--delete` |
| `internal/playbook/ollama.go` | Added confirmation prompt; shell-quoted model + prompt |
| `internal/playbook/vllm.go` | Shell-quoted model name |
| `internal/playbook/nvfp4.go` | Restructured docker command to use env var for model name |
| `internal/playbook/dmr.go` | Added confirmation prompt; replaced local `shellQuote` with `ssh.ShellQuote`; removed duplicate |
| `README.md` | Added Security section; updated playbook notes; added host key troubleshooting; updated project structure |
