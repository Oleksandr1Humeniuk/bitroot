package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"bitroot/internal/ai"
	"bitroot/internal/scanner"

	"github.com/joho/godotenv"
)

func main() {
	path := flag.String("path", ".", "Directory path to scan")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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

	rootDir := *path
	rootInfo, err := os.Stat(rootDir)
	if err != nil {
		logger.Error("invalid --path", "path", rootDir, "error", err)
		os.Exit(1)
	}

	if !rootInfo.IsDir() {
		logger.Info("processing single file input", "path", rootDir)
	}

	workerCount := 4
	logger.Info("starting scanner", "path", rootDir, "workers", workerCount)

	projectTree, err := buildProjectTree(rootDir, 300)
	if err != nil {
		logger.Warn("failed to build project context", "error", err)
	}

	s := scanner.NewScanner(workerCount, logger)
	results, err := s.Scan(ctx, rootDir)
	if err != nil {
		logger.Error("failed to start scanner", "error", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 2)

	for metadata := range results {
		if metadata.Error != nil {
			logger.Warn("scan error", "path", metadata.Path, "error", metadata.Error)
			continue
		}

		if metadata.Language == "" {
			logger.Debug("skipping non-text file", "path", metadata.Path)
			continue
		}

		md := metadata
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			logger.Info("processing file", "path", md.Path)

			select {
			case <-ctx.Done():
				logger.Warn("context cancelled before file read", "path", md.Path, "error", ctx.Err())
				return
			default:
			}

			code, err := readFilePrefix(ctx, md.Path, 4000)
			if err != nil {
				logger.Warn("failed to read file", "path", md.Path, "error", err)
				return
			}

			summary, err := aiClient.AnalyzeCodeWithContext(ctx, projectTree, md.Path, string(code))
			if err != nil {
				logger.Warn("ai analysis failed", "path", md.Path, "error", err)
				return
			}

			logger.Info("ai analysis", "file", md.Path, "lang", md.Language, "summary", summary)
		}()
	}

	wg.Wait()
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
				return nil, fmt.Errorf("buffer write failed: %w", writeErr)
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

	ignoredDirs := map[string]struct{}{
		".git":         {},
		".next":        {},
		".vercel":      {},
		"build":        {},
		"coverage":     {},
		"node_modules": {},
		"dist":         {},
		"vendor":       {},
	}

	var lines []string
	var truncated bool
	stopWalk := errors.New("stop walk")

	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		if d.IsDir() {
			if _, ok := ignoredDirs[strings.ToLower(d.Name())]; ok {
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
