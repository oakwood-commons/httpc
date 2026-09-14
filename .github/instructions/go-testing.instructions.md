---
description: "Go testing conventions for httpc: table-driven tests, testify/assert, benchmarks, race detection, and coverage. Use when writing or editing Go test files."
applyTo: "**/*_test.go"
---

# Go Testing Conventions

## Framework

- Use standard `go test` with **table-driven tests**
- Use `testify`: `require` when a failure makes the rest of the test
  meaningless, `assert` when it does not
- Test files are in `package httpc` (white-box), so unexported helpers are
  available to tests

## Running the suite

`task test` runs the unit tests with `-shuffle=on`, so an order-dependent test
fails intermittently rather than consistently. A test that passes alone and
fails in the suite is a bug in the test, not flakiness to retry past.

`task test:e2e` is a misleading name: it runs `vet`, `lint`, and the coverage
profile. There is no separate end-to-end suite. `task ci` is the full local
gate.

## Race Detection

Always run with the `-race` flag:

```bash
go test -race ./...
```

## Coverage

```bash
go test -cover ./...
```

### Coverage thresholds

Codecov enforces **70% project** coverage (1% threshold) and **50% patch**
coverage (5% threshold). Those numbers live in `codecov.yml`;
`task coverage:check` applies the same 70% figure locally.

Treat them as floors, not targets. Security-critical code (`ssrf.go`,
`circuitbreaker.go`) is held far higher in practice, and a new file with no
tests will not survive review whatever the percentage says.

- Write the tests in the same change as the code.
- **A test earns its place by failing.** Break the code it covers and confirm
  it goes red. An assertion that holds equally against the bug and the fix
  documents intent but catches nothing.

## Benchmarks

Add benchmark tests for any new features:

```go
func BenchmarkMyFeature(b *testing.B) {
    b.ReportAllocs()
    b.ResetTimer()

    for b.Loop() {
        // benchmark code
    }
}
```
