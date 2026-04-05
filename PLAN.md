BitRoot: Operational Plan
Current Status: Phase 2.3 — Semantic Retrieval & QA [COMPLETED]
Tech Stack: Go 1.26+, OpenRouter (Qwen + OpenAI Embeddings), JSON Vector Store

Phase 1: Core AI Infrastructure [COMPLETED]
1.1 Foundation & Connectivity
[x] Initial repository setup (Go modules, .env, .gitignore).

[x] Project structure skeleton (cmd/, internal/).

[x] OpenAI-compatible HTTP client for internal/ai.

[x] Smart scanner with file filtering (node_modules, binary, etc.).

1.2 Production-Grade Reliability
[x] Resilience: Exponential Backoff & Retry logic for 429/5xx errors.

[x] Timeouts: Hard context deadlines for AI requests.

[x] Structured Output: JSON Mode enforcement for schema-validated responses.

[x] Observability: Token usage tracking and cost telemetry dashboard.

Phase 2: Context & Semantic Intelligence [COMPLETED]
2.1 File Analysis & Caching [COMPLETED]
[x] Language detection and SHA-256 content hashing.

[x] Persistence layer: .bitroot_index.json for skipping unchanged files.

[x] Project-level context (building the project tree for system prompts).

2.2 RAG Foundation (Knowledge Base) [COMPLETED]
[x] Smart Chunking: Logical splitting of large files with line-preservation.

[x] Embeddings: Integration with dedicated embedding models (Ollama/OpenAI).

[x] Vector Storage: Implementation of vector persistence (1536-dim vectors in JSON).

2.3 Retrieval & Query Workflow [ACTIVE]
[x] Similarity Engine: Implement Cosine Similarity for vector comparison.

[x] CLI "ask" Command: New sub-command for natural language queries.

[x] Context Injection: RAG logic to feed top-K relevant chunks into the prompt.

[x] Source Citations: AI responses with file paths and line references.

Phase 3: Advanced Agentic Features [BACKLOG]
[ ] Auto-Fixer: Agentic loop for suggesting and applying code refactoring.

[ ] Cross-File Mapping: Understanding dependencies across the whole codebase.

[ ] Multi-Model Support: Dynamic model selection based on task complexity.

Technical Context
Entrypoint: cmd/bitroot/main.go

Scanner: internal/scanner (Handling chunks & hashing)

AI: internal/ai (Handling chat & embeddings)

Storage: internal/storage (Managing JSON vector database)

Next Atomic Step
Implement Task 3.1: Start Auto-Fixer agentic loop design.
