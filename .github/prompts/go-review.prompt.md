---
description: "httpc: Run Go code review on recent changes. Checks for idiomatic Go, security, error handling, concurrency, and httpc conventions."
agent: "go-reviewer"
---
Review the current Go code changes thoroughly. You MUST complete ALL phases below.

## Phase 1: Automated checks

1. Run `go vet ./...` and `task lint`
2. Run `git diff --stat HEAD -- '*.go'` and `git status --short`
3. Read the full diff for all changed files and full contents of new files
4. Run `go test -coverprofile` on every changed package
5. Run `go test -race` on changed packages

## Phase 2: Systematic review

For each changed/new file, check:

### Security
- SSRF bypass potential
- Race conditions on shared state
- Hardcoded secrets or credentials
- Insecure TLS settings

### Error handling
- Ignored errors, missing error wrapping, panics for recoverable errors

### Concurrency
- Goroutine leaks, race conditions, deadlock potential

### Code quality
- Functions over 60 lines, nesting over 4 levels, non-idiomatic patterns

### httpc conventions
- Metrics through interface, logr.Logger for logging, no hardcoded app names
- Constants from defaults.go, configurable cache keys and dirs

## Phase 3: Coverage analysis

1. Run coverage on changed packages
2. Flag any changed function with coverage below 70%
3. Flag any NEW file with overall coverage below 70%

## Phase 4: Self-review

Re-read the full diff and ask "what did I miss?"

## Output format

Use severity levels: CRITICAL > HIGH > MEDIUM > LOW > INFO
End with: `Review: APPROVE/WARNING/BLOCK | Critical: N | High: N | Medium: N`
