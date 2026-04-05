# BitRoot: Operational Plan

**Current Phase**: Phase 3 — Knowledge Base & Cache
**Status**: [ACTIVE]

---

## Phase 1 — AI Integration [COMPLETED]

- [x] 1.1 Create `scripts/check.sh` (fmt, vet, test) and update `AGENTS.md`.
- [x] 1.2 Setup `internal/ai` package and API client.
- [x] 1.3 Implement `.env` support for API credentials.

---

## Phase 2 — Context & Metadata [COMPLETED]

- [x] 2.1 Implement file type filtering (ignore `node_modules`, `.git`, binary files).
- [x] 2.2 Add Language Detection to metadata (Go, TS, JS, etc.).
- [x] 2.3 Implement project-level context (system prompt with project tree).

---

## Phase 3 — Knowledge Base & Cache

- [x] 3.1 Implement SHA-256 hashing for file contents in `internal/scanner`.
- [x] 3.2 Create `internal/storage` for index persistence (JSON-based).
- [x] 3.3 Add logic in `main.go` to skip AI processing if file hash matches stored index.

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
- AI package: `internal/ai`
- Storage package: `internal/storage`

## Next Atomic Step

Phase 3 baseline complete. Define next cache refinement tasks.
