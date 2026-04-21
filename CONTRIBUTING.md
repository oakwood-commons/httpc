# Contributing to httpc

Thank you for your interest in contributing!

## Development

### Prerequisites

- Go 1.26+
- [Task](https://taskfile.dev/) runner (`go install github.com/go-task/task/v3/cmd/task@latest`)

### Building

```bash
go build ./...
```

### Testing

```bash
go test ./...
```

### Full CI Pipeline

```bash
task ci
```

### Linting

```bash
task lint
```

### Formatting

```bash
task fmt
```

## Commits

Use [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/#specification).
All commits must be signed (`-S`) and include DCO sign-off (`-s`).

```bash
git commit -s -S -m "feat: add new feature"
```

### Commit Types

- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `test`: Adding or updating tests
- `refactor`: Code refactoring
- `perf`: Performance improvements
- `chore`: Maintenance tasks
- `ci`: CI/CD changes

## Pull Requests

1. Fork the repository
2. Create a feature branch from `main`
3. Make your changes
4. Ensure all tests pass: `task ci`
5. Submit a pull request

## Response Times

- **Issue triage**: 1-2 weeks
- **Pull request review**: 2 weeks
- **Security reports**: 48 hours

## Benchmarks

When adding performance-sensitive code, include benchmarks:

```go
func BenchmarkMyFunction(b *testing.B) {
    for b.Loop() {
        MyFunction()
    }
    b.ReportAllocs()
}
```

Run benchmarks with:

```bash
task bench
```

## Coordination with scafctl

This package is used by [scafctl](https://github.com/oakwood-commons/scafctl) as its HTTP client.
Breaking changes to the public API should be coordinated with the scafctl repository.

## License

By contributing, you agree that your contributions will be licensed under the
Apache License 2.0.
