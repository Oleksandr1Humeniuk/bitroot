# BitRoot: Operational Plan

**Current Phase**: Phase 0 — CLI Foundation & Agentic Workflow
**Status**: [ACTIVE]

---

## Todo: Phase 2

## Done

- [x] Initialize repository (.gitignore, .env.example, go.mod).
- [x] Define project structure (`cmd/`, `internal/`).

Phase 0

- [x] 1. Rewrite PLAN.md into this operational format.
- [x] 2. Implement `internal/scanner` skeleton (Struct & Interface).
- [x] 3. Implement directory traversal using `path/filepath`.
- [x] 4. Connect scanner to `cmd/bitroot/main.go`.
- [x] 5. Add CLI flag for `--path` (default: ".").
- [x] 6. Add basic `slog` integration.

---

## Technical Context (Current)

- Entrypoint: `cmd/bitroot/main.go`
- Package: `internal/scanner`
- Using `context.Context` for cancellation.

## Next Atomic Step

Phase 0 complete. Prepare Phase 1 tasks.
