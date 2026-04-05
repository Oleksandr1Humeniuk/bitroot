package scanner

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	goBoundaryPattern         = regexp.MustCompile(`^(func|type|const|var|import|package)\b`)
	jsLikeBoundaryPattern     = regexp.MustCompile(`^(export\s+)?(async\s+)?function\b|^class\b|^(export\s+)?(const|let|var)\b`)
	markdownBoundaryPattern   = regexp.MustCompile(`^#{1,6}\s+`)
	pythonBoundaryPattern     = regexp.MustCompile(`^(class|def)\s+`)
	fallbackBoundarySeparator = regexp.MustCompile(`^\s*$`)
)

// CodeChunk represents one semantically grouped chunk of file content.
type CodeChunk struct {
	FilePath      string
	StartLine     int
	EndLine       int
	Content       string
	TokenEstimate int
}

func (c CodeChunk) Location() string {
	return fmt.Sprintf("%s:%d-%d", c.FilePath, c.StartLine, c.EndLine)
}

// ChunkOptions controls how source content is split.
type ChunkOptions struct {
	TargetTokens int
	MaxTokens    int
	MinLines     int
}

func DefaultChunkOptions() ChunkOptions {
	return ChunkOptions{
		TargetTokens: 900,
		MaxTokens:    1200,
		MinLines:     20,
	}
}

// ChunkSource splits source code into logical blocks and packs them into chunks.
func ChunkSource(filePath, language, source string, opts ChunkOptions) []CodeChunk {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	if strings.TrimSpace(source) == "" {
		return nil
	}

	opts = normalizeChunkOptions(opts)
	lines := strings.Split(source, "\n")
	boundaries := detectLogicalBoundaries(language, lines)
	if len(boundaries) == 0 || boundaries[0] != 0 {
		boundaries = append([]int{0}, boundaries...)
	}
	if boundaries[len(boundaries)-1] != len(lines) {
		boundaries = append(boundaries, len(lines))
	}

	type block struct {
		start int
		end   int
	}

	blocks := make([]block, 0, len(boundaries)-1)
	for i := 0; i < len(boundaries)-1; i++ {
		start := boundaries[i]
		end := boundaries[i+1]
		if start >= end {
			continue
		}
		blocks = append(blocks, block{start: start, end: end})
	}

	if len(blocks) == 0 {
		return nil
	}

	chunks := make([]CodeChunk, 0, len(blocks))
	curStart := -1
	curEnd := -1
	curTokens := 0

	flushCurrent := func() {
		if curStart == -1 || curEnd <= curStart {
			return
		}
		chunks = append(chunks, makeChunk(filePath, lines, curStart, curEnd))
		curStart = -1
		curEnd = -1
		curTokens = 0
	}

	for _, b := range blocks {
		blockTokens := estimateLineTokens(lines[b.start:b.end])
		if blockTokens > opts.MaxTokens {
			flushCurrent()
			split := splitOversizedBlock(filePath, lines, b.start, b.end, opts)
			chunks = append(chunks, split...)
			continue
		}

		if curStart == -1 {
			curStart = b.start
			curEnd = b.end
			curTokens = blockTokens
			continue
		}

		if curTokens+blockTokens > opts.TargetTokens {
			flushCurrent()
			curStart = b.start
			curEnd = b.end
			curTokens = blockTokens
			continue
		}

		curEnd = b.end
		curTokens += blockTokens
	}

	flushCurrent()
	return chunks
}

func normalizeChunkOptions(opts ChunkOptions) ChunkOptions {
	if opts.TargetTokens < 1 {
		opts.TargetTokens = 900
	}
	if opts.MaxTokens < opts.TargetTokens {
		opts.MaxTokens = opts.TargetTokens
	}
	if opts.MinLines < 1 {
		opts.MinLines = 1
	}

	return opts
}

func detectLogicalBoundaries(language string, lines []string) []int {
	var pattern *regexp.Regexp

	switch strings.ToLower(language) {
	case "go":
		pattern = goBoundaryPattern
	case "typescript", "javascript", "tsx", "jsx":
		pattern = jsLikeBoundaryPattern
	case "markdown":
		pattern = markdownBoundaryPattern
	case "python":
		pattern = pythonBoundaryPattern
	}

	boundaries := make([]int, 0, len(lines)/8)
	blankRun := 0

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if i == 0 {
			if fallbackBoundarySeparator.MatchString(trimmed) {
				blankRun = 1
			}
			continue
		}

		if pattern != nil && pattern.MatchString(trimmed) {
			boundaries = append(boundaries, i)
			blankRun = 0
			continue
		}

		if fallbackBoundarySeparator.MatchString(trimmed) {
			blankRun++
			if blankRun >= 2 {
				boundaries = append(boundaries, i)
				blankRun = 0
			}
			continue
		}

		blankRun = 0
	}

	return dedupeSorted(boundaries)
}

func splitOversizedBlock(filePath string, lines []string, start, end int, opts ChunkOptions) []CodeChunk {
	if start >= end {
		return nil
	}

	chunks := make([]CodeChunk, 0, 4)
	cursor := start

	for cursor < end {
		next := cursor
		tokens := 0
		lastBlank := -1

		for next < end {
			lineTokens := EstimateTokens(lines[next])
			if lineTokens < 1 {
				lineTokens = 1
			}

			if tokens+lineTokens > opts.MaxTokens && next > cursor {
				break
			}

			tokens += lineTokens
			if strings.TrimSpace(lines[next]) == "" {
				lastBlank = next
			}
			next++
		}

		if next < end && lastBlank >= cursor+opts.MinLines {
			next = lastBlank + 1
		}

		if next <= cursor {
			next = cursor + 1
		}

		chunks = append(chunks, makeChunk(filePath, lines, cursor, next))
		cursor = next
	}

	return chunks
}

func estimateLineTokens(lines []string) int {
	if len(lines) == 0 {
		return 0
	}

	total := 0
	for _, line := range lines {
		t := EstimateTokens(line)
		if t < 1 {
			t = 1
		}
		total += t
	}

	return total
}

func makeChunk(filePath string, lines []string, start, end int) CodeChunk {
	if start < 0 {
		start = 0
	}
	if end > len(lines) {
		end = len(lines)
	}
	if end < start {
		end = start
	}

	content := strings.Join(lines[start:end], "\n")
	return CodeChunk{
		FilePath:      filePath,
		StartLine:     start + 1,
		EndLine:       end,
		Content:       content,
		TokenEstimate: EstimateTokens(content),
	}
}

func dedupeSorted(input []int) []int {
	if len(input) == 0 {
		return input
	}

	out := make([]int, 0, len(input))
	last := -1
	for _, value := range input {
		if value == last {
			continue
		}
		out = append(out, value)
		last = value
	}

	return out
}
