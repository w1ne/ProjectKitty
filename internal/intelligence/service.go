package intelligence

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

type Service interface {
	Scan(context.Context, Request) (ContextSnapshot, error)
}

type Request struct {
	Task      string
	Workspace string
}

type ContextSnapshot struct {
	CandidateFiles []string
	Symbols        map[string][]string
	Summary        string
	HasGoModule    bool
}

type LocalService struct{}

func New() *LocalService {
	return &LocalService{}
}

var symbolPattern = regexp.MustCompile(`(?m)^\s*(?:func|type)\s+([A-Za-z_][A-Za-z0-9_]*)`)

func (s *LocalService) Scan(ctx context.Context, req Request) (ContextSnapshot, error) {
	tokens := taskTokens(req.Task)
	scored := make([]scoredFile, 0, 16)
	hasGoModule := false

	err := filepath.WalkDir(req.Workspace, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == ".projectkitty" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}

		info, statErr := d.Info()
		if statErr != nil || info.Size() > 256*1024 {
			return nil
		}

		rel, relErr := filepath.Rel(req.Workspace, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "go.mod" {
			hasGoModule = true
		}

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		score := scoreFile(rel, string(content), tokens)
		if score == 0 && filepath.Base(rel) != "go.mod" {
			return nil
		}

		scored = append(scored, scoredFile{
			Path:    rel,
			Score:   score,
			Symbols: extractSymbols(string(content)),
		})
		return nil
	})
	if err != nil {
		return ContextSnapshot{}, err
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Path < scored[j].Path
		}
		return scored[i].Score > scored[j].Score
	})

	limit := min(5, len(scored))
	files := make([]string, 0, limit)
	symbols := make(map[string][]string, limit)
	for _, candidate := range scored[:limit] {
		files = append(files, candidate.Path)
		if len(candidate.Symbols) > 0 {
			symbols[candidate.Path] = candidate.Symbols
		}
	}

	summary := "No relevant files found."
	if len(files) > 0 {
		summary = fmt.Sprintf("Focused context narrowed to %d files: %s", len(files), strings.Join(files, ", "))
	}

	return ContextSnapshot{
		CandidateFiles: files,
		Symbols:        symbols,
		Summary:        summary,
		HasGoModule:    hasGoModule,
	}, nil
}

type scoredFile struct {
	Path    string
	Score   int
	Symbols []string
}

func taskTokens(task string) []string {
	fields := strings.Fields(strings.ToLower(task))
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, ".,:;!?\"'`()[]{}")
		if len(field) < 3 {
			continue
		}
		tokens = append(tokens, field)
	}
	slices.Sort(tokens)
	return slices.Compact(tokens)
}

func scoreFile(path, content string, tokens []string) int {
	lowerPath := strings.ToLower(path)
	lowerContent := strings.ToLower(content)
	score := 0
	for _, token := range tokens {
		if strings.Contains(lowerPath, token) {
			score += 3
		}
		if strings.Contains(lowerContent, token) {
			score++
		}
	}
	return score
}

func extractSymbols(content string) []string {
	matches := symbolPattern.FindAllStringSubmatch(content, 6)
	if len(matches) == 0 {
		return nil
	}
	symbols := make([]string, 0, len(matches))
	for _, match := range matches {
		symbols = append(symbols, match[1])
	}
	return symbols
}
