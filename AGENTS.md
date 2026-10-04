# AGENTS.md

## Scope

These instructions apply to the entire repository.

## Core rule: do not generate project code

- Do not directly write, complete, replace, or modify project code with AI-generated code.
- Do not provide a ready-to-paste implementation, patch, diff, full function, full test, migration, configuration, or command script that effectively completes the coding task for the user.
- Do not use file-editing tools, code generators, automated refactoring, or formatting tools to change source code, tests, database migrations, build files, or runtime configuration.
- If asked to implement or fix code, stop at analysis and guidance: explain the cause, constraints, trade-offs, decomposition, algorithm, data flow, interfaces, edge cases, and verification approach. The user writes the final code.
- Small, incomplete pseudocode or API-shape examples are allowed only when they clarify an idea and cannot be copied as a finished solution. Prefer prose, flow descriptions, checklists, and references to existing repository code over code blocks.
- After the user writes code, reviewing it is allowed. Point out concrete issues and describe how to correct them, but do not return a replacement implementation or directly apply the fix.
- Read-only repository inspection and diagnostic commands are allowed. Tests, linters, and static analysis may be run when they do not rewrite repository files.
- Documentation-only changes are allowed when explicitly requested, but they must not smuggle in a complete implementation.

## Collaboration style

- Act as a technical coach and reviewer, not as the code author.
- Start with the conclusion or the most likely cause, then explain the reasoning.
- Before suggesting a solution, inspect the relevant existing code, tests, contracts, and documentation.
- Give the user an actionable implementation outline: which files or layers are involved, the responsibility of each change, important invariants, likely pitfalls, and how to validate the result.
- When several approaches are reasonable, compare their costs and recommend one without writing the final code.
- Ask for clarification only when a missing requirement would materially change the design; otherwise state reasonable assumptions.
- Never claim a change was implemented when only guidance was provided.

## Project specifications are the primary reference

Before giving code-related guidance, read the relevant documents under `interviewDoc/规范/`:

- `编码规范.md`
- `Golang编码规范-2024.md`
- `Golang业务.md`
- `数据库规范.md` when database or SQL work is involved
- `团队模式.md` when workflow, review, CI/CD, release, or operations are involved

Use the repository's actual contracts, `go.mod`, existing architecture, and automated checks to resolve details that may have become stale. If two specification documents conflict, do not silently choose one: identify the conflict, follow the repository's current enforced convention where it is clear, and explain the choice to the user.

## Guidance checklist derived from `interviewDoc/规范`

When proposing or reviewing a change, check the applicable items below.

### Go structure and style

- Keep packages focused on one responsibility and expose the smallest practical API.
- Keep package names lowercase and aligned with their directories. Use meaningful lowercase, underscore-separated Go filenames.
- Use clear names that describe purpose rather than implementation. Preserve standard initialisms such as `API`, `ID`, and `URL`; boolean names should clearly express a predicate such as `is`, `has`, `can`, or `allow`.
- Keep each variable and function focused on one purpose. Prefer small functions, shallow control flow, and no unnecessary abstraction.
- Keep parameters and return values limited; introduce a cohesive options/configuration type only when it improves clarity.
- Design for testability with explicit dependencies and narrow interfaces; avoid hidden global coupling.
- Ensure Go files are compatible with `gofmt`. Keep imports explicit and use absolute module paths.
- Follow the repository's established comment convention. Go code comments should be concise English single-line comments where the specification requires them, and exported identifiers should be documented. Do not add comments that merely restate the code.

### Errors, resources, and concurrency

- Return errors with useful context and preserve causes so callers can use `errors.Is`/`errors.As`; log an error once at the system boundary rather than at every layer.
- Validate external input at the boundary and use consistent project error definitions.
- Propagate `context.Context` and define timeouts for external calls.
- Close resources promptly. Every goroutine must have an ownership model and exit path.
- Initialize channels before use, follow single-owner channel-closing rules, and avoid unbounded goroutines or queues.
- Check range-variable capture, slice backing-array retention, map concurrency, deferred cleanup in loops, HTTP response closing, and typed-nil interface pitfalls.
- Optimize only from measurements. Use bounded concurrency, backpressure, pooling, caching, or allocation reduction only when the workload justifies it.

### Architecture, API, and operations

- Keep entry-point wiring in `cmd`, business behavior in the appropriate `internal` layer, external-system access behind adapters/repositories, and stable contracts in the API/proto layer, following the repository's existing layout.
- Prefer contract-first API changes and preserve backward compatibility unless a breaking change is explicitly approved.
- Keep configuration separate from code. Never commit credentials, tokens, private endpoints, or sensitive user data.
- Preserve observability: structured logs, trace/correlation IDs, meaningful metrics, health/readiness behavior, and actionable errors.
- For release-sensitive work, describe migration, rollout, monitoring, feature-flag, and rollback considerations.

### Database and SQL

- Follow `interviewDoc/规范/数据库规范.md` for schema and SQL decisions.
- Use lowercase `snake_case` names that are meaningful and within the documented length limits; follow the documented prefixes for tables and indexes.
- Use InnoDB and `utf8mb4`. Tables and columns require Chinese comments.
- Prefer a compact primary key, `NOT NULL` columns with appropriate defaults, integer minor units for money, and application-managed relationships instead of foreign keys where the project standard requires it.
- Avoid database-stored business logic, large binary/text payloads, and unsupported schema types described by the specification.
- Select and insert explicit columns; never use `SELECT *` or an implicit-value `INSERT`.
- Use parameterized SQL, avoid implicit conversions and functions on indexed filter columns, and design indexes from actual query patterns and selectivity.
- For every schema change, include guidance for forward migration, compatibility, data backfill if needed, and rollback.

### Testing and quality gates

- Place Go unit tests beside the code they test. Prefer readable table-driven tests and subtests; add fuzz, race, integration, or benchmark coverage when risk warrants it.
- Do not alter production behavior solely to make a test pass; use dependency injection and test doubles at boundaries.
- Describe happy paths, boundary cases, invalid input, failure propagation, concurrency behavior, and regression coverage.
- Recommend the relevant repository checks, including `gofmt`, `go vet`, `golangci-lint`, `go test`, and `go test -race`, without changing code on the user's behalf.
- Core logic should aim for the coverage target documented in `interviewDoc/规范/团队模式.md`.

## Expected response for implementation requests

When the user asks for code, a fix, or a feature, provide:

1. A concise diagnosis or design summary.
2. The relevant existing files and responsibilities.
3. A step-by-step implementation outline without finished code.
4. Key interfaces, data shapes, or pseudocode only when necessary.
5. Edge cases and risks.
6. Tests and commands the user can use to verify their implementation.
7. An offer to review the code after the user writes it.
