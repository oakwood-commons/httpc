---
description: "Go testing conventions for httpc: table-driven tests, testify/assert, benchmarks, race detection, and coverage. Use when writing or editing Go test files."
applyTo: "**/*_test.go"
---

# Go Testing Conventions

## Framework

- Use standard `go test` with **table-driven tests**
- Use `testify/assert` for assertions
- Place mocks in `mock.go` files

## E2E Tests

E2E tests (`task test:e2e`) are expensive. Follow these rules:

1. Only run when validating a complete set of changes, not for iterative checks
2. Run **once** and capture output: `task test:e2e 2>&1 | tee /tmp/e2e-results.txt`
3. Review the saved file instead of re-running: `grep -E 'FAIL|PASS|ok' /tmp/e2e-results.txt`
4. For iterative development, run targeted unit tests: `go test ./...`

## Race Detection

Always run with the `-race` flag:

```bash
go test -race ./...
```

## Coverage

```bash
go test -cover ./...
```

### Coverage Targets

| Code Type | Package Target | Patch Target |
|-----------|---------------|-------------|
| Core library code | 80%+ | 80%+ |
| Critical logic (SSRF, circuit breaker) | 90%+ | 100% |

### Patch Coverage (CRITICAL)

Every PR must have **70%+ patch coverage** (percentage of new/changed lines covered by tests). This is enforced by Codecov.

- When adding new code, write tests for it in the same PR
- Never submit a new file with 0% coverage; at minimum test the happy path and one error path

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
