package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
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

func main() {
	startTime := time.Now()

	path := flag.String("path", ".", "Directory path to scan")
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
		os.Getenv("AI_EMBEDDING_MODEL"),
	)

	if err := aiClient.Ping(baseCtx); err != nil {
		log.Fatal(err)
	}

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

	indexPath := filepath.Join(indexRoot, ".bitroot_index.json")
	projectIndex := &storage.ProjectIndex{}
	if err := projectIndex.Load(indexPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			projectIndex = &storage.ProjectIndex{
				ProjectRoot: indexRoot,
				Files:       make(map[string]storage.FileEntry),
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

		indexMu.Lock()
		cached, ok := projectIndex.Files[metadata.Path]
		hasEmbedding := len(cached.Embedding) > 0
		unchanged := ok && cached.Hash == metadata.Hash && hasEmbedding
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

			indexMu.Lock()
			projectIndex.Files[md.Path] = storage.FileEntry{
				Path:      md.Path,
				Hash:      md.Hash,
				Language:  md.Language,
				Summary:   fileSummary,
				Embedding: embedding,
				UpdatedAt: time.Now().UTC(),
			}
			indexMu.Unlock()
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
