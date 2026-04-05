package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"bitroot/internal/ai"
	"bitroot/internal/scanner"
	"bitroot/internal/storage"

	"github.com/joho/godotenv"
)

type telemetry struct {
	filesScanned          int64
	filesAnalyzed         int64
	filesSkippedCache     int64
	filesFailed           int64
	embeddingsGenerated   int64
	embeddingsFailed      int64
	totalAIAttempts       int64
	authErrors            int64
	transportErrors       int64
	apiLogicErrors        int64
	totalPromptTokensHint int64
}

type citationSource struct {
	Index int
	Path  string
	Refs  []string
	Score float64
}

func main() {
	startTime := time.Now()

	path := flag.String("path", ".", "Directory path to scan")
	ask := flag.String("ask", "", "Natural language query over the vector store")
	topK := flag.Int("topk", 5, "Maximum number of semantic matches returned by --ask")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	baseCtx := context.Background()
	scanCtx, stopScan := context.WithCancel(baseCtx)
	defer stopScan()

	interruptCh := make(chan os.Signal, 1)
	signal.Notify(interruptCh, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(interruptCh)

	wasInterrupted := make(chan struct{}, 1)
	go func() {
		<-interruptCh
		stopScan()
		select {
		case wasInterrupted <- struct{}{}:
		default:
		}
	}()

	if err := godotenv.Load(); err != nil {
		logger.Warn("failed to load .env", "error", err)
	}

	aiClient, err := ai.NewClient(
		os.Getenv("AI_BASE_URL"),
		os.Getenv("AI_API_KEY"),
		os.Getenv("AI_MODEL"),
	)
	if err != nil {
		logger.Error("failed to initialize ai client", "error", err)
		os.Exit(1)
	}

	aiClient.ConfigureEmbeddings(
		os.Getenv("AI_EMBEDDING_PROVIDER"),
		os.Getenv("EMBEDDING_MODEL"),
	)

	rootDir := *path
	rootInfo, err := os.Stat(rootDir)
	if err != nil {
		logger.Error("invalid --path", "path", rootDir, "error", err)
		os.Exit(1)
	}

	if !rootInfo.IsDir() {
		logger.Info("processing single file input", "path", rootDir)
	}

	indexRoot := rootDir
	if !rootInfo.IsDir() {
		indexRoot = filepath.Dir(rootDir)
	}

	if strings.TrimSpace(*ask) != "" {
		if err := runAskMode(baseCtx, logger, aiClient, indexRoot, *ask, *topK); err != nil {
			logger.Error("ask command failed", "error", err)
			os.Exit(1)
		}
		return
	}

	if err := aiClient.Ping(baseCtx); err != nil {
		log.Fatal(err)
	}

	projectIndex, indexPath := loadProjectIndex(logger, indexRoot)

	workerCount := 4
	logger.Info("starting scanner", "path", rootDir, "workers", workerCount)

	projectTree, err := buildProjectTree(rootDir, 300)
	if err != nil {
		logger.Warn("failed to build project context", "error", err)
	}

	s := scanner.NewScanner(workerCount, logger)
	results, err := s.Scan(scanCtx, rootDir)
	if err != nil {
		logger.Error("failed to start scanner", "error", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup
	var indexMu sync.Mutex
	sem := make(chan struct{}, 2)
	inFlight := make(map[string]struct{})
	tel := &telemetry{}

	for metadata := range results {
		atomic.AddInt64(&tel.filesScanned, 1)

		if metadata.Error != nil {
			atomic.AddInt64(&tel.filesFailed, 1)
			logger.Warn("scan error", "path", metadata.Path, "error", metadata.Error)
			continue
		}

		if metadata.Language == "" {
			logger.Debug("skipping non-text file", "path", metadata.Path)
			continue
		}

		cached, ok := projectIndex.Get(metadata.Path)
		hasEmbedding := len(cached.Embedding) > 0
		unchanged := ok && cached.Hash == metadata.Hash && hasEmbedding

		indexMu.Lock()
		_, processing := inFlight[metadata.Path]
		if !unchanged && !processing {
			inFlight[metadata.Path] = struct{}{}
		}
		indexMu.Unlock()
		if unchanged {
			atomic.AddInt64(&tel.filesSkippedCache, 1)
			logger.Info("skipping unchanged file", "path", metadata.Path)
			continue
		}
		if processing {
			logger.Debug("skipping in-flight file", "path", metadata.Path)
			continue
		}

		md := metadata
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				indexMu.Lock()
				delete(inFlight, md.Path)
				indexMu.Unlock()
			}()
			sem <- struct{}{}
			defer func() { <-sem }()

			logger.Info("processing file", "path", md.Path)

			code, err := readFilePrefix(baseCtx, md.Path, md.Size)
			if err != nil {
				atomic.AddInt64(&tel.filesFailed, 1)
				atomic.AddInt64(&tel.transportErrors, 1)
				logger.Warn("failed to read file", "path", md.Path, "error", err)
				return
			}

			chunkOptions := scanner.DefaultChunkOptions()
			chunks := scanner.ChunkSource(md.Path, md.Language, string(code), chunkOptions)
			if len(chunks) == 0 {
				atomic.AddInt64(&tel.filesFailed, 1)
				logger.Warn("chunking produced no content", "path", md.Path)
				return
			}

			chunkSummaries := make([]string, 0, len(chunks))
			for _, chunk := range chunks {
				atomic.AddInt64(&tel.totalPromptTokensHint, int64(chunk.TokenEstimate))

				result, err := aiClient.AnalyzeCodeWithContextDetailed(baseCtx, projectTree, chunk.Location(), chunk.Content)
				atomic.AddInt64(&tel.totalAIAttempts, int64(result.Attempts))
				if err != nil {
					atomic.AddInt64(&tel.filesFailed, 1)
					trackAIError(tel, err)
					logger.Warn("ai chunk analysis failed", "path", md.Path, "chunk", chunk.Location(), "error", err)
					return
				}

				chunkSummaries = append(chunkSummaries, chunk.Location()+": "+result.Summary)
			}

			fileSummary := strings.Join(chunkSummaries, " ")
			if len(chunkSummaries) > 1 {
				aggregatedPrompt := "Synthesize these chunk summaries into one short, professional file summary:\n\n- " + strings.Join(chunkSummaries, "\n- ")
				atomic.AddInt64(&tel.totalPromptTokensHint, int64(scanner.EstimateTokens(aggregatedPrompt)))

				aggregatedResult, err := aiClient.AnalyzeCodeWithContextDetailed(baseCtx, projectTree, md.Path, aggregatedPrompt)
				atomic.AddInt64(&tel.totalAIAttempts, int64(aggregatedResult.Attempts))
				if err != nil {
					atomic.AddInt64(&tel.filesFailed, 1)
					trackAIError(tel, err)
					logger.Warn("ai summary synthesis failed", "path", md.Path, "error", err)
					return
				}

				fileSummary = aggregatedResult.Summary
			}

			embedding, err := aiClient.EmbedText(baseCtx, fileSummary)
			if err != nil {
				atomic.AddInt64(&tel.embeddingsFailed, 1)
				logger.Warn("embedding generation failed", "path", md.Path, "error", err)
			} else {
				atomic.AddInt64(&tel.embeddingsGenerated, 1)
			}

			upsertErr := projectIndex.Upsert(storage.FileEntry{
				Path:      md.Path,
				Hash:      md.Hash,
				Language:  md.Language,
				Summary:   fileSummary,
				Refs:      chunkRefs(chunks, 5),
				Embedding: embedding,
				UpdatedAt: time.Now().UTC(),
			})
			if upsertErr != nil {
				atomic.AddInt64(&tel.filesFailed, 1)
				logger.Warn("vector upsert failed", "path", md.Path, "error", upsertErr)
				return
			}

			if len(embedding) > 0 {
				similar, searchErr := projectIndex.SearchSimilar(embedding, 3, md.Path)
				if searchErr != nil {
					logger.Warn("vector search failed", "path", md.Path, "error", searchErr)
				} else if len(similar) > 0 {
					logger.Info("semantic neighbors", "path", md.Path, "neighbor", similar[0].Path, "score", similar[0].Score)
				}
			}

			atomic.AddInt64(&tel.filesAnalyzed, 1)

			logger.Info("ai analysis", "file", md.Path, "lang", md.Language, "chunks", len(chunks), "summary", fileSummary)
		}()
	}

	wg.Wait()
	if err := projectIndex.Save(indexPath); err != nil {
		logger.Warn("failed to save index", "path", indexPath, "error", err)
	} else {
		logger.Info("index saved", "path", indexPath)
	}

	select {
	case <-wasInterrupted:
		logger.Info("graceful shutdown complete", "index_path", indexPath)
	default:
	}

	logger.Info(
		"scan telemetry",
		"duration", time.Since(startTime).String(),
		"files_scanned", atomic.LoadInt64(&tel.filesScanned),
		"files_analyzed", atomic.LoadInt64(&tel.filesAnalyzed),
		"files_skipped_cache", atomic.LoadInt64(&tel.filesSkippedCache),
		"files_failed", atomic.LoadInt64(&tel.filesFailed),
		"embeddings_generated", atomic.LoadInt64(&tel.embeddingsGenerated),
		"embeddings_failed", atomic.LoadInt64(&tel.embeddingsFailed),
		"total_ai_attempts", atomic.LoadInt64(&tel.totalAIAttempts),
		"auth_errors", atomic.LoadInt64(&tel.authErrors),
		"transport_errors", atomic.LoadInt64(&tel.transportErrors),
		"api_logic_errors", atomic.LoadInt64(&tel.apiLogicErrors),
	)

	if atomic.LoadInt64(&tel.filesFailed) > 0 {
		logger.Info("error breakdown", "401 Unauthorized", atomic.LoadInt64(&tel.authErrors), "transport", atomic.LoadInt64(&tel.transportErrors), "api_logic", atomic.LoadInt64(&tel.apiLogicErrors))
	}

	logger.Info("scan completed")
}

func readFilePrefix(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	if maxBytes <= 0 {
		return []byte{}, nil
	}

	var out bytes.Buffer
	chunk := make([]byte, 1024)
	remaining := maxBytes

	for remaining > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		toRead := int64(len(chunk))
		if remaining < toRead {
			toRead = remaining
		}

		n, readErr := file.Read(chunk[:toRead])
		if n > 0 {
			if _, writeErr := out.Write(chunk[:n]); writeErr != nil {
				return nil, writeErr
			}
			remaining -= int64(n)
		}

		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, readErr
		}
	}

	return out.Bytes(), nil
}

func buildProjectTree(rootDir string, maxEntries int) (string, error) {
	if maxEntries < 1 {
		maxEntries = 1
	}

	var lines []string
	var truncated bool
	stopWalk := errors.New("stop walk")

	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		if d.IsDir() {
			if scanner.IsIgnoredDirectory(d.Name()) {
				return filepath.SkipDir
			}
		}

		relPath, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil
		}

		if relPath == "." {
			return nil
		}

		if len(lines) >= maxEntries {
			truncated = true
			return stopWalk
		}

		if d.IsDir() {
			lines = append(lines, relPath+"/")
		} else {
			lines = append(lines, relPath)
		}

		return nil
	})
	if err != nil && !errors.Is(err, stopWalk) {
		return "", err
	}

	if len(lines) == 0 {
		return "(empty project)", nil
	}

	if truncated {
		lines = append(lines, "... (truncated)")
	}

	return strings.Join(lines, "\n"), nil
}

func trackAIError(tel *telemetry, err error) {
	var authErr ai.AuthError
	var transportErr ai.TransportError
	var logicErr ai.APILogicError

	switch {
	case errors.As(err, &authErr):
		atomic.AddInt64(&tel.authErrors, 1)
	case errors.As(err, &transportErr):
		atomic.AddInt64(&tel.transportErrors, 1)
	case errors.As(err, &logicErr):
		atomic.AddInt64(&tel.apiLogicErrors, 1)
	default:
		atomic.AddInt64(&tel.transportErrors, 1)
	}
}

func runAskMode(ctx context.Context, logger *slog.Logger, aiClient *ai.Client, indexRoot, query string, topK int) error {
	if strings.TrimSpace(query) == "" {
		return errors.New("ask query is required")
	}

	if topK < 1 {
		topK = 5
	}

	projectIndex, _ := loadProjectIndex(logger, indexRoot)
	if len(projectIndex.Files) == 0 {
		return errors.New("vector store is empty; run scan first")
	}

	queryEmbedding, err := aiClient.EmbedText(ctx, query)
	if err != nil {
		return err
	}

	results, err := projectIndex.SearchSimilar(queryEmbedding, topK, "")
	if err != nil {
		return err
	}

	if len(results) == 0 {
		logger.Info("no semantic matches found", "query", query)
		return nil
	}

	semanticContext, sources := buildRAGContext(results)
	answer, err := aiClient.AnswerQuestionWithContext(ctx, query, semanticContext)
	if err != nil {
		return err
	}

	fmt.Printf("Query: %s\n", query)
	fmt.Printf("Top %d semantic matches:\n", len(results))
	for i, result := range results {
		fmt.Printf("%d. %s (score=%.4f)\n", i+1, result.Path, result.Score)
		if result.Summary != "" {
			fmt.Printf("   %s\n", result.Summary)
		}
	}
	fmt.Printf("\nAnswer:\n%s\n", answer)

	if len(sources) > 0 {
		fmt.Printf("\nSources:\n")
		for _, source := range sources {
			if len(source.Refs) > 0 {
				fmt.Printf("[%d] %s (%s)\n", source.Index, source.Path, strings.Join(source.Refs, ", "))
			} else {
				fmt.Printf("[%d] %s\n", source.Index, source.Path)
			}
		}
	}

	return nil
}

func buildRAGContext(results []storage.SearchResult) (string, []citationSource) {
	if len(results) == 0 {
		return "(no semantic context)", nil
	}

	var b strings.Builder
	sources := make([]citationSource, 0, len(results))
	for i, result := range results {
		b.WriteString(fmt.Sprintf("[%d] Path: %s\n", i+1, result.Path))
		b.WriteString(fmt.Sprintf("Score: %.4f\n", result.Score))
		if len(result.Refs) > 0 {
			b.WriteString("Line references: ")
			b.WriteString(strings.Join(result.Refs, ", "))
			b.WriteString("\n")
		}
		if strings.TrimSpace(result.Summary) != "" {
			b.WriteString("Summary: ")
			b.WriteString(strings.TrimSpace(result.Summary))
			b.WriteString("\n")
		}
		b.WriteString("\n")

		sources = append(sources, citationSource{
			Index: i + 1,
			Path:  result.Path,
			Refs:  append([]string(nil), result.Refs...),
			Score: result.Score,
		})
	}

	return strings.TrimSpace(b.String()), sources
}

func chunkRefs(chunks []scanner.CodeChunk, max int) []string {
	if len(chunks) == 0 || max <= 0 {
		return nil
	}

	limit := max
	if len(chunks) < limit {
		limit = len(chunks)
	}

	refs := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		refs = append(refs, chunks[i].Location())
	}

	return refs
}

func loadProjectIndex(logger *slog.Logger, indexRoot string) (*storage.ProjectIndex, string) {
	indexPath := filepath.Join(indexRoot, ".bitroot_vector_store.json")
	legacyIndexPath := filepath.Join(indexRoot, ".bitroot_index.json")
	projectIndex := &storage.ProjectIndex{}

	if err := projectIndex.Load(indexPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if legacyErr := projectIndex.Load(legacyIndexPath); legacyErr != nil {
				if !errors.Is(legacyErr, os.ErrNotExist) {
					logger.Warn("failed to load legacy index, starting fresh", "path", legacyIndexPath, "error", legacyErr)
				}
				projectIndex = &storage.ProjectIndex{
					ProjectRoot: indexRoot,
					Files:       make(map[string]storage.FileEntry),
				}
			} else {
				logger.Info("migrated legacy index into vector store", "legacy_path", legacyIndexPath)
			}
		} else {
			logger.Warn("failed to load index, starting fresh", "path", indexPath, "error", err)
			projectIndex = &storage.ProjectIndex{
				ProjectRoot: indexRoot,
				Files:       make(map[string]storage.FileEntry),
			}
		}
	}

	if projectIndex.Files == nil {
		projectIndex.Files = make(map[string]storage.FileEntry)
	}
	projectIndex.ProjectRoot = indexRoot

	return projectIndex, indexPath
}
