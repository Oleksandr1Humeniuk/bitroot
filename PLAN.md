# BitRoot: Operational Plan

**Current Phase**: Phase 1 — AI Integration
**Status**: [ACTIVE]

---

## Todo: Phase 1 — AI Integration

- [x] 1.1 Create `scripts/check.sh` (fmt, vet, test) and update `AGENTS.md`.
- [x] 1.2 Setup `internal/ai` package and API client.
- [x] 1.3 Implement `.env` support for API credentials.

---

## Archived

### Foundation

- [x] Initialize repository (.gitignore, .env.example, go.mod).
- [x] Define project structure (`cmd/`, `internal/`).

### Phase 0 — CLI Foundation & Agentic Workflow

- [x] 1. Rewrite PLAN.md into this operational format.
- [x] 2. Implement `internal/scanner` skeleton (Struct & Interface).
- [x] 3. Implement directory traversal using `path/filepath`.
- [x] 4. Connect scanner to `cmd/bitroot/main.go`.
- [x] 5. Add CLI flag for `--path` (default: ".").
- [x] 6. Add basic `slog` integration.

---

## Technical Context (Current)

- Entrypoint: `cmd/bitroot/main.go`
- Scanner package: `internal/scanner`
- Next package: `internal/ai`

## Next Atomic Step

Phase 1 tasks complete. Define next integration milestone.
