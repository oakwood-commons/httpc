---
name: httpc-dev-loop
description: "Use when building, testing, linting, or verifying a change in the httpc repository -- before running any Go or task command here. Covers the pinned toolchain, which task target to reach for at which point, what CI actually gates on, and the commit requirements that block a merge. Not for understanding what the code does (AGENTS.md covers that)."
---

# Working in httpc

`taskfile.yaml` is the source of truth for every command. `task --list` shows
them all; this skill covers which one to reach for, and the traps.

## Use the task targets, not bare `go` commands

The taskfile pins its own tools into `./.tool` (golangci-lint, gofumpt,
goimports, govulncheck, benchstat) and sets `CGO_ENABLED=0`, `GOWORK=off`, and
a private lint cache. A bare `golangci-lint` off your `PATH` is a different
version than CI runs and will disagree with it.

Go **1.27.1** or newer is required (`go.mod`). The taskfile derives
`GOTOOLCHAIN` from that, so `task` targets get the right compiler even if your
default `go` is older -- another reason to prefer them.

## The loop

While iterating, run the narrowest thing that answers your question:

```bash
task build                   # go build ./...
task test                    # go test -shuffle=on -timeout 10m ./...
go test -run TestName .      # a single test, fastest signal
```

Tests run with `-shuffle=on`, so **order-dependent tests fail randomly rather
than consistently**. A test that passes alone and fails in the suite is a real
bug in the test, not flakiness to retry past.

Before proposing a change as finished:

```bash
task ci
```

That runs, in order: `mod`, `build`, `vet`, `lint`, `fmt:check`, `test:race`,
`coverage:check`, `vulncheck`. It is the same ground CI covers, so a green
`task ci` is the strongest local signal available.

If `task ci` is too slow for the moment, the two that catch the most are:

```bash
task lint                    # golangci-lint, ~30 linters, no issue allowances
task test:race               # the race detector, CGO_ENABLED=1
```

Run `task fmt` to fix formatting (goimports, then gofumpt with extra rules)
rather than hand-correcting what `fmt:check` reports.

## What actually gates a merge

Four GitHub workflows run on a pull request. Two of them reject work outright:

- **DCO** -- every commit must carry a `Signed-off-by:` trailer. The check
  scans every commit in the branch, so one missing trailer fails the whole
  pull request.
- **CI** -- `go vet`, golangci-lint, a `go mod tidy` diff check, govulncheck,
  and `go test -race` with coverage uploaded to Codecov.

CodeQL and a benchmark-comparison job also run; the benchmark job posts a
comparison against `main` as a pull request comment.

The `go mod tidy` diff check means an untidy `go.mod` fails CI even when the
code is fine. `task mod` tidies; `task mod:check` tells you without editing.

## Coverage: the real numbers

Codecov enforces **70% project** coverage (1% threshold) and **50% patch**
coverage (5% threshold) -- see `codecov.yml`. `task coverage:check` uses the
same 70% project figure locally.

Those are floors, not targets. The security-critical code (`ssrf.go`,
`circuitbreaker.go`) is held far higher in practice, and a new file arriving
with no tests will not survive review regardless of what the percentage says.
Write tests in the same change as the code.

## Tests

Table-driven, with `testify`: `require` for a failure that makes the rest of
the test meaningless, `assert` for one that does not. Test files are in
`package httpc` (white-box), so unexported helpers are fair game.

```go
tests := []struct {
    name    string
    wantErr bool
}{...}

for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) { ... })
}
```

**A test earns its place by failing.** Before considering one done, break the
code it covers and confirm it goes red. Assertions that hold equally against
the bug and the fix are the main way regressions slip through here -- several
have, in review.

Add a benchmark for performance-sensitive code, using `b.Loop()` and
`b.ReportAllocs()`; the benchmark workflow compares it against `main`
automatically.

## Commits

Conventional commits, and **every commit must be both signed and signed off**:

```bash
git commit -s -S -m "fix(ssrf): reject scoped IPv6 literals"
```

`-s` adds the DCO trailer the DCO workflow requires. `-S` signs the commit.
Recognized types: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `chore`,
`ci`.

The changelog is generated from these commits by `git-cliff` at release time
(`cliff.toml`), so the message is the permanent public record of the change.
Two consequences:

- A breaking change needs a `BREAKING CHANGE:` trailer in the body, or it
  disappears from the release notes. This library is pre-1.0, so breaking
  changes are permitted -- silent ones are not.
- Commits whose body mentions security are grouped into a Security section.
