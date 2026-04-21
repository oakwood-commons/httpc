---
description: "httpc: Fetch and triage PR review comments for the current branch. Presents findings for approval before handing off to go-fixer."
agent: "pr-reviewer"
argument-hint: "Optional: PR number or leave blank to use current branch"
---
Triage unresolved PR review comments. Use `gh` CLI and the **GitHub GraphQL API (v4)** to fetch review threads.

Follow these phases **in order**:

1. **Fetch**: Fetch all review threads via GraphQL; skip resolved comments. Include outdated but unresolved threads
2. **Pipeline check**: Run `gh pr checks <PR_NUMBER>` to see CI status
3. **Coverage check**: Assess patch coverage from Codecov
4. **Early exit**: If zero unresolved threads, all checks passing, and coverage >= 70%, report and stop
5. **Triage**: For each unresolved comment, assess and present the triage summary

Include thread IDs in the triage output so the fixer agent can respond after applying fixes.
