# BitRoot: Operational Plan

**Current Phase**: Phase 2.1 — RAG & Semantic Context
**Status**: [ACTIVE]

---

## Phase 1 — AI Integration [COMPLETED]

- [x] 1.1 Create `scripts/check.sh` (fmt, vet, test) and update `AGENTS.md`.
- [x] 1.2 Setup `internal/ai` package and API client.
- [x] 1.3 Implement `.env` support for API credentials.

---

## Phase 1.1 — Reliability & Observability [COMPLETED]

- [x] 1.1.1 Resilient AI Client: Implement middleware for internal/ai with support for:
      Exponential Backoff: Automatic retries for 429 (Rate Limit) and 5xx (Server) errors.
      Hard Timeout: Prevent the CLI from hanging indefinitely on a single file.

- [x] 1.1.2 Token & Cost Tracking: Implement a response interceptor in internal/ai to extract and log prompt_tokens and completion_tokens.

- [x] 1.1.3 Structured Output Enforcement: Transition the system prompt to JSON Mode to ensure internal/scanner receives schema-validated data instead of raw text.

- [x] 1.1.4 Telemetry & Reporting: Display a summary dashboard after scanning (Execution time, total tokens used, and files skipped via cache).

---

## Phase 2 — Context & Metadata [COMPLETED]

- [x] 2.1 Implement file type filtering (ignore `node_modules`, `.git`, binary files).
- [x] 2.2 Add Language Detection to metadata (Go, TS, JS, etc.).
- [x] 2.3 Implement project-level context (system prompt with project tree).

---

## Phase 2.4 — Knowledge Base & Cache [COMPLETED]

- [x] 3.1 Implement SHA-256 hashing for file contents in `internal/scanner`.
- [x] 3.2 Create `internal/storage` for index persistence (JSON-based).
- [x] 3.3 Add logic in `main.go` to skip AI processing if file hash matches stored index.

---

## Phase 2.1 — RAG & Semantic Context (Next)

- [ ] 2.1.1 Chunking Strategy: Develop logic to split large source files into logical blocks (functions, classes, or blocks) to fit model context limits.

- [ ] 2.1.2 Embeddings Integration: Integrate embedding models (via Ollama or OpenAI) to represent code semantically.

- [ ] 2.1.3 Vector Store Implementation: Replace storage/index.json with a local vector database or an embedded semantic search engine (e.g., ChromaDB or a native Go implementation).

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

Implement task #2.1.1: chunking strategy for large source files.
