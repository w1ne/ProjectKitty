package intelligence

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_bash "github.com/tree-sitter/tree-sitter-bash/bindings/go"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_ruby "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

type Service interface {
	Search(context.Context, Request) (SearchResult, error)
	Outline(context.Context, OutlineRequest) (OutlineResult, error)
}

type Request struct {
	Task      string
	Workspace string
}

type ContextSnapshot struct {
	CandidateFiles []string
	Symbols        map[string][]string
	FocusedSymbol  *SymbolMatch
	RelatedFiles   []string
	Summary        string
	HasGoModule    bool
}

type SearchResult struct {
	CandidateFiles []string
	Summary        string
	HasGoModule    bool
	Provider       string
	Passes         []SearchPass
}

type SearchPass struct {
	Name           string
	Tokens         []string
	Provider       string
	CandidateCount int
}

type OutlineRequest struct {
	Task      string
	Workspace string
	Files     []string
}

type OutlineResult struct {
	Symbols       map[string][]string
	FocusedSymbol *SymbolMatch
	RelatedFiles  []string
	Summary       string
}

type SymbolMatch struct {
	Path       string
	Name       string
	Kind       string
	StartLine  int
	EndLine    int
	Snippet    string
	Confidence int
}

var symbolPattern = regexp.MustCompile(`(?m)^(\s*)(?:func\s+(?:\([^)]+\)\s*)?([A-Za-z_][A-Za-z0-9_]*)|type\s+([A-Za-z_][A-Za-z0-9_]*))`)
var receiverTypePattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
var importPattern = regexp.MustCompile(`(?m)^(?:\s*import\s+(?:\w+\s+)?"([^"]+)"|\s*import\s+\((?:\s*(?:\w+\s+)?"([^"]+)")*\s*\))`)

type languageSpec struct {
	lang       *tree_sitter.Language
	extensions []string
}

var languageSpecs = []languageSpec{
	{lang: tree_sitter.NewLanguage(tree_sitter_go.Language()), extensions: []string{".go"}},
	{lang: tree_sitter.NewLanguage(tree_sitter_java.Language()), extensions: []string{".java"}},
	{lang: tree_sitter.NewLanguage(tree_sitter_javascript.Language()), extensions: []string{".js", ".mjs", ".cjs", ".jsx"}},
	{lang: tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()), extensions: []string{".ts"}},
	{lang: tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTSX()), extensions: []string{".tsx"}},
	{lang: tree_sitter.NewLanguage(tree_sitter_python.Language()), extensions: []string{".py"}},
	{lang: tree_sitter.NewLanguage(tree_sitter_rust.Language()), extensions: []string{".rs"}},
	{lang: tree_sitter.NewLanguage(tree_sitter_ruby.Language()), extensions: []string{".rb"}},
	{lang: tree_sitter.NewLanguage(tree_sitter_bash.Language()), extensions: []string{".sh", ".bash"}},
}

type SearchTool struct{}

func (t *SearchTool) Search(ctx context.Context, s *LocalService, req Request) (SearchResult, error) {
	tokens := taskTokens(req.Task)
	scored, hasGoModule, provider, passes, err := s.searchMultiPass(ctx, req.Workspace, tokens)
	if err != nil {
		return SearchResult{}, err
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Kind != scored[j].Kind {
			return scored[i].Kind < scored[j].Kind
		}
		if scored[i].Score == scored[j].Score {
			return scored[i].Path < scored[j].Path
		}
		return scored[i].Score > scored[j].Score
	})

	limit := min(5, len(scored))
	files := make([]string, 0, limit)
	for _, candidate := range scored[:limit] {
		files = append(files, candidate.Path)
	}

	summary := "No relevant files found."
	if len(files) > 0 {
		summary = fmt.Sprintf("Focused context narrowed to %d files via %s (%d passes): %s", len(files), provider, len(passes), strings.Join(files, ", "))
	} else if provider != "" {
		summary = fmt.Sprintf("No relevant files found via %s (%d passes).", provider, len(passes))
	}
	return SearchResult{
		CandidateFiles: files,
		Summary:        summary,
		HasGoModule:    hasGoModule,
		Provider:       provider,
		Passes:         passes,
	}, nil
}

type OutlineTool struct{}

func (t *OutlineTool) Outline(ctx context.Context, s *LocalService, req OutlineRequest) (OutlineResult, error) {
	tokens := taskTokens(req.Task)
	symbols := make(map[string][]string, len(req.Files))
	var focused *SymbolMatch
	focusedScore := -1
	relatedCache := make(map[string][]string)

	for _, rel := range req.Files {
		select {
		case <-ctx.Done():
			return OutlineResult{}, ctx.Err()
		default:
		}

		content, err := os.ReadFile(filepath.Join(req.Workspace, rel))
		if err != nil {
			continue
		}
		outline := extractSymbols(string(content), rel)
		if len(outline) == 0 {
			continue
		}

		names := make([]string, 0, len(outline))
		baseScore := scoreFile(rel, string(content), tokens)
		for _, symbol := range outline {
			names = append(names, symbol.Name)
			cacheKey := rel + "::" + symbol.Name
			if _, ok := relatedCache[cacheKey]; !ok {
				relatedFiles, _ := s.findRelatedFiles(ctx, req.Workspace, symbol)
				relatedCache[cacheKey] = relatedFiles
			}
			score := scoreSymbol(symbol, tokens, baseScore, relatedCache[cacheKey])
			if score > focusedScore {
				s := symbol
				s.Confidence = score
				focused = &s
				focusedScore = score
			}
		}
		symbols[rel] = names
	}

	summary := "No strong symbol match yet."
	if len(symbols) > 0 {
		summary = fmt.Sprintf("Outlined %d candidate files.", len(symbols))
	}
	if focused != nil && (!hasStructuralTokenMatch(*focused, tokens) || focused.Confidence < minimumFocusedConfidence(tokens)) {
		focused = nil
	}
	if focused != nil {
		relatedFiles := relatedCache[focused.Path+"::"+focused.Name]
		if len(relatedFiles) > 0 {
			summary += fmt.Sprintf(" Related files: %s.", strings.Join(relatedFiles, ", "))
		}
		summary += fmt.Sprintf(" Best symbol match: %s in %s.", focused.Name, focused.Path)
		return OutlineResult{
			Symbols:       symbols,
			FocusedSymbol: focused,
			RelatedFiles:  relatedFiles,
			Summary:       summary,
		}, nil
	} else if len(symbols) > 0 {
		summary += " No strong symbol match yet."
	}

	return OutlineResult{
		Symbols:       symbols,
		FocusedSymbol: focused,
		RelatedFiles:  nil,
		Summary:       summary,
	}, nil
}

type LocalService struct {
	searchTool  *SearchTool
	outlineTool *OutlineTool
}

func New() *LocalService {
	return &LocalService{
		searchTool:  &SearchTool{},
		outlineTool: &OutlineTool{},
	}
}

func (s *LocalService) Search(ctx context.Context, req Request) (SearchResult, error) {
	return s.searchTool.Search(ctx, s, req)
}

func (s *LocalService) Outline(ctx context.Context, req OutlineRequest) (OutlineResult, error) {
	return s.outlineTool.Outline(ctx, s, req)
}

func (s *LocalService) Scan(ctx context.Context, req Request) (ContextSnapshot, error) {
	search, err := s.Search(ctx, req)
	if err != nil {
		return ContextSnapshot{}, err
	}
	outline, err := s.Outline(ctx, OutlineRequest{
		Task:      req.Task,
		Workspace: req.Workspace,
		Files:     search.CandidateFiles,
	})
	if err != nil {
		return ContextSnapshot{}, err
	}
	summary := search.Summary
	if outline.Summary != "" {
		summary = search.Summary + " " + outline.Summary
	}
	return ContextSnapshot{
		CandidateFiles: search.CandidateFiles,
		Symbols:        outline.Symbols,
		FocusedSymbol:  outline.FocusedSymbol,
		RelatedFiles:   outline.RelatedFiles,
		Summary:        strings.TrimSpace(summary),
		HasGoModule:    search.HasGoModule,
	}, nil
}

type scoredFile struct {
	Path    string
	Score   int
	Symbols []SymbolMatch
	Kind    fileKind
}

type fileKind int

const (
	fileKindSource fileKind = iota
	fileKindTest
	fileKindDoc
)

func (s *LocalService) collectCandidates(ctx context.Context, workspace string, tokens []string) ([]scoredFile, bool, string, error) {
	if rg, hasGoModule, used, err := s.scanWithRipgrep(ctx, workspace, tokens); used || err != nil {
		return rg, hasGoModule, "ripgrep", err
	}
	if gitFiles, hasGoModule, used, err := s.scanWithGit(ctx, workspace, tokens); used || err != nil {
		return gitFiles, hasGoModule, "git ls-files", err
	}
	files, hasGoModule, err := s.scanWithWalk(ctx, workspace, tokens)
	return files, hasGoModule, "workspace walk", err
}

func (s *LocalService) searchMultiPass(ctx context.Context, workspace string, tokens []string) ([]scoredFile, bool, string, []SearchPass, error) {
	primary, hasGoModule, provider, err := s.collectCandidates(ctx, workspace, tokens)
	if err != nil {
		return nil, false, "", nil, err
	}

	passes := []SearchPass{{
		Name:           "primary",
		Tokens:         append([]string(nil), tokens...),
		Provider:       provider,
		CandidateCount: len(primary),
	}}

	refinedTokens := deriveRefinedTokens(tokens, primary)
	if len(refinedTokens) == 0 {
		return primary, hasGoModule, provider, passes, nil
	}

	refined, refinedHasGoModule, refinedProvider, err := s.collectCandidates(ctx, workspace, refinedTokens)
	if err != nil {
		return nil, false, "", nil, err
	}
	passes = append(passes, SearchPass{
		Name:           "refine_structural",
		Tokens:         refinedTokens,
		Provider:       refinedProvider,
		CandidateCount: len(refined),
	})

	merged := mergeCandidateScores(primary, refined)
	return merged, hasGoModule || refinedHasGoModule, joinProviders(provider, refinedProvider), passes, nil
}

func (s *LocalService) scanWithRipgrep(ctx context.Context, workspace string, tokens []string) ([]scoredFile, bool, bool, error) {
	if len(tokens) == 0 {
		return nil, false, false, nil
	}
	if _, err := exec.LookPath("rg"); err != nil {
		return nil, false, false, nil
	}

	pattern := strings.Join(tokens, "|")
	cmd := exec.CommandContext(
		ctx,
		"rg",
		"--files-with-matches",
		"--smart-case",
		pattern,
		workspace,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, false, false, nil
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}

	scored := make([]scoredFile, 0, len(lines)+1)
	hasGoModule := false
	for _, line := range lines {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		rel, relErr := filepath.Rel(workspace, path)
		if relErr != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		if rel == "go.mod" {
			hasGoModule = true
		}
		score := scoreFile(rel, string(content), tokens)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredFile{
			Path:    rel,
			Score:   score,
			Symbols: extractSymbols(string(content), rel),
			Kind:    classifyFile(rel),
		})
	}

	if !hasGoModule {
		if content, err := os.ReadFile(filepath.Join(workspace, "go.mod")); err == nil {
			hasGoModule = true
			scored = append(scored, scoredFile{
				Path:    "go.mod",
				Score:   scoreFile("go.mod", string(content), tokens),
				Symbols: nil,
				Kind:    fileKindSource,
			})
		}
	}

	return scored, hasGoModule, true, nil
}

func (s *LocalService) scanWithGit(ctx context.Context, workspace string, tokens []string) ([]scoredFile, bool, bool, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, false, false, nil
	}

	cmd := exec.CommandContext(ctx, "git", "-C", workspace, "ls-files", "-co", "--exclude-standard", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil, false, false, nil
	}

	entries := strings.Split(string(out), "\x00")
	scored := make([]scoredFile, 0, len(entries))
	hasGoModule := false

	for _, rel := range entries {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}

		fullPath := filepath.Join(workspace, filepath.FromSlash(rel))
		info, statErr := os.Stat(fullPath)
		if statErr != nil || info.IsDir() || info.Size() > 256*1024 {
			continue
		}

		content, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			continue
		}

		if rel == "go.mod" {
			hasGoModule = true
		}

		score := scoreFile(rel, string(content), tokens)
		if score == 0 && rel != "go.mod" {
			continue
		}

		scored = append(scored, scoredFile{
			Path:    rel,
			Score:   score,
			Symbols: extractSymbols(string(content), rel),
			Kind:    classifyFile(rel),
		})
	}

	return scored, hasGoModule, true, nil
}

func (s *LocalService) scanWithWalk(ctx context.Context, workspace string, tokens []string) ([]scoredFile, bool, error) {
	scored := make([]scoredFile, 0, 16)
	hasGoModule := false

	err := filepath.WalkDir(workspace, func(path string, d fs.DirEntry, err error) error {
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
			if strings.HasPrefix(name, ".") && path != workspace {
				return filepath.SkipDir
			}
			return nil
		}

		info, statErr := d.Info()
		if statErr != nil || info.Size() > 256*1024 {
			return nil
		}

		rel, relErr := filepath.Rel(workspace, path)
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
			Symbols: extractSymbols(string(content), rel),
			Kind:    classifyFile(rel),
		})
		return nil
	})
	if err != nil {
		return nil, false, err
	}

	return scored, hasGoModule, nil
}

func taskTokens(task string) []string {
	fields := strings.Fields(strings.ToLower(task))
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, ".,:;!?\"'`()[]{}")
		if len(field) < 3 {
			continue
		}
		if isWeakTaskToken(field) {
			continue
		}
		tokens = append(tokens, field)
	}
	slices.Sort(tokens)
	return slices.Compact(tokens)
}

func isWeakTaskToken(token string) bool {
	switch token {
	case "a", "an", "and", "are", "for", "from", "how", "into", "its", "not", "that", "the", "their", "them", "then", "there", "these", "this", "those", "validate", "check", "review", "analyze", "analyse", "look", "find", "show", "read", "run", "test", "tests", "verify", "inspect", "with", "without":
		return true
	default:
		return false
	}
}

func deriveRefinedTokens(tokens []string, scored []scoredFile) []string {
	if len(scored) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		seen[token] = struct{}{}
	}

	refined := make([]string, 0, 8)
	limit := min(3, len(scored))
	for _, candidate := range scored[:limit] {
		for _, part := range splitIdentifier(filepath.Base(candidate.Path)) {
			if shouldAddRefinedToken(part, seen) {
				seen[part] = struct{}{}
				refined = append(refined, part)
			}
		}
		for _, symbol := range candidate.Symbols {
			for _, part := range splitIdentifier(symbol.Name) {
				if shouldAddRefinedToken(part, seen) {
					seen[part] = struct{}{}
					refined = append(refined, part)
				}
			}
		}
	}
	if len(refined) > 6 {
		refined = refined[:6]
	}
	return refined
}

func shouldAddRefinedToken(token string, seen map[string]struct{}) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	if len(token) < 3 || isWeakTaskToken(token) {
		return false
	}
	_, exists := seen[token]
	return !exists
}

func splitIdentifier(value string) []string {
	replacer := strings.NewReplacer(".", " ", "_", " ", "-", " ", "/", " ")
	value = replacer.Replace(value)
	var builder strings.Builder
	for i, r := range value {
		if i > 0 && r >= 'A' && r <= 'Z' {
			builder.WriteByte(' ')
		}
		builder.WriteRune(r)
	}
	parts := strings.Fields(strings.ToLower(builder.String()))
	return slices.Compact(parts)
}

func mergeCandidateScores(primary, refined []scoredFile) []scoredFile {
	merged := make(map[string]scoredFile, len(primary)+len(refined))
	for _, candidate := range primary {
		merged[candidate.Path] = candidate
	}
	for _, candidate := range refined {
		existing, ok := merged[candidate.Path]
		if !ok {
			candidate.Score += 2
			merged[candidate.Path] = candidate
			continue
		}
		existing.Score += candidate.Score + 2
		if len(existing.Symbols) == 0 && len(candidate.Symbols) > 0 {
			existing.Symbols = candidate.Symbols
		}
		if existing.Kind > candidate.Kind {
			existing.Kind = candidate.Kind
		}
		merged[candidate.Path] = existing
	}

	result := make([]scoredFile, 0, len(merged))
	for _, candidate := range merged {
		result = append(result, candidate)
	}
	return result
}

func joinProviders(first, second string) string {
	switch {
	case first == "" && second == "":
		return ""
	case first == "":
		return second
	case second == "" || second == first:
		return first
	default:
		return first + " -> " + second
	}
}

func scoreFile(path, content string, tokens []string) int {
	lowerPath := strings.ToLower(path)
	lowerContent := strings.ToLower(content)
	score := 0
	matched := false
	for _, token := range tokens {
		if strings.Contains(lowerPath, token) {
			score += 5 // Slightly increased from 4
			matched = true
		}
		if strings.Contains(lowerContent, token) {
			score++
			matched = true
		}
	}
	if !matched {
		return 0
	}

	// Boost for structural overlap (Claude/Codex style)
	symbols := extractSymbols(content, path)
	for _, s := range symbols {
		lowerName := strings.ToLower(s.Name)
		for _, token := range tokens {
			if strings.Contains(lowerName, token) {
				score += 2 // Boost for each symbol name match
			}
		}
	}

	score += fileTypeBias(lowerPath)
	return score
}

func fileTypeBias(path string) int {
	switch {
	case strings.HasSuffix(path, "_test.go"):
		return -6
	case strings.HasPrefix(path, "docs/"), strings.HasSuffix(path, ".md"), strings.HasSuffix(path, ".txt"):
		return -8
	case strings.Contains(path, "/vendor/"):
		return -3
	case strings.HasSuffix(path, ".go"):
		return 2
	default:
		return 0
	}
}

func extractSymbols(content, path string) []SymbolMatch {
	if symbols := extractTreeSitterSymbols(content, path); len(symbols) > 0 {
		return symbols
	}
	return extractRegexSymbols(content, path)
}

func FindSymbol(content, path, name string) *SymbolMatch {
	for _, symbol := range extractSymbols(content, path) {
		if symbol.Name == name {
			s := symbol
			return &s
		}
	}
	return nil
}

func extractTreeSitterSymbols(content, path string) []SymbolMatch {
	language := languageForPath(path)
	if language == nil {
		return nil
	}

	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(language); err != nil {
		return nil
	}

	tree := parser.Parse([]byte(content), nil)
	if tree == nil {
		return nil
	}
	defer tree.Close()

	root := tree.RootNode()
	if root == nil {
		return nil
	}

	cursor := tree.Walk()
	defer cursor.Close()
	return collectTreeSitterSymbols(content, path, root, cursor, "")
}

func collectTreeSitterSymbols(content, path string, node *tree_sitter.Node, cursor *tree_sitter.TreeCursor, classContext string) []SymbolMatch {
	result := make([]SymbolMatch, 0)
	if node == nil {
		return result
	}

	nextClassContext := classContext
	if symbol := symbolFromNode(content, path, node, classContext); symbol != nil {
		result = append(result, *symbol)
		if node.Kind() == "class_declaration" || node.Kind() == "class_definition" {
			nextClassContext = symbol.Name
		}
	}

	for _, child := range node.NamedChildren(cursor) {
		result = append(result, collectTreeSitterSymbols(content, path, &child, cursor, nextClassContext)...)
	}
	return result
}

func symbolFromNode(content, path string, node *tree_sitter.Node, classContext string) *SymbolMatch {
	if node == nil {
		return nil
	}

	kind := node.Kind()
	nameNode := node.ChildByFieldName("name")

	switch kind {
	case "type_declaration":
		return nil
	case "type_spec":
		return buildSymbol(content, path, node, "type", textForNode(content, nameNode))
	case "function_declaration", "function_definition", "generator_function_declaration", "function_signature", "function_item", "method":
		return buildSymbol(content, path, node, "func", textForNode(content, nameNode))
	case "method_declaration":
		name := textForNode(content, nameNode)
		if receiver := receiverNameFromTreeSitter(content, node.ChildByFieldName("receiver")); receiver != "" {
			name = receiver + "." + name
		}
		return buildSymbol(content, path, node, "func", name)
	case "method_definition":
		name := textForNode(content, nameNode)
		if classContext != "" {
			name = classContext + "." + name
		}
		return buildSymbol(content, path, node, "func", name)
	case "class_declaration", "class_definition", "interface_declaration", "type_alias_declaration", "enum_declaration", "struct_item", "enum_item", "trait_item", "class", "module":
		return buildSymbol(content, path, node, "type", textForNode(content, nameNode))
	case "decorated_definition":
		return symbolFromNode(content, path, node.ChildByFieldName("definition"), classContext)
	default:
		return nil
	}
}

func buildSymbol(content, path string, node *tree_sitter.Node, kind, name string) *SymbolMatch {
	if node == nil || name == "" {
		return nil
	}
	start := int(node.StartByte())
	end := int(node.EndByte())
	if start < 0 || end > len(content) || start >= end {
		return nil
	}
	return &SymbolMatch{
		Path:      path,
		Name:      name,
		Kind:      kind,
		StartLine: int(node.StartPosition().Row) + 1,
		EndLine:   int(node.EndPosition().Row) + 1,
		Snippet:   strings.TrimSpace(content[start:end]),
	}
}

func languageForPath(path string) *tree_sitter.Language {
	ext := strings.ToLower(filepath.Ext(path))
	for _, spec := range languageSpecs {
		if slices.Contains(spec.extensions, ext) {
			return spec.lang
		}
	}
	return nil
}

func textForNode(content string, node *tree_sitter.Node) string {
	if node == nil {
		return ""
	}
	start := int(node.StartByte())
	end := int(node.EndByte())
	if start < 0 || end > len(content) || start >= end {
		return ""
	}
	return strings.TrimSpace(content[start:end])
}

func receiverNameFromTreeSitter(content string, node *tree_sitter.Node) string {
	text := textForNode(content, node)
	if text == "" {
		return ""
	}
	parts := receiverTypePattern.FindAllString(text, -1)
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func extractRegexSymbols(content, path string) []SymbolMatch {
	matches := symbolPattern.FindAllStringSubmatch(content, 6)
	indices := symbolPattern.FindAllStringSubmatchIndex(content, 6)
	if len(matches) == 0 {
		return nil
	}
	symbols := make([]SymbolMatch, 0, len(matches))
	for i, match := range matches {
		name := match[2]
		kind := "func"
		if name == "" {
			name = match[3]
			kind = "type"
		}
		start, end := symbolRange(content, indices, i)
		symbols = append(symbols, SymbolMatch{
			Path:      path,
			Name:      name,
			Kind:      kind,
			StartLine: lineNumber(content, start),
			EndLine:   lineNumber(content, end),
			Snippet:   strings.TrimSpace(content[start:end]),
		})
	}
	return symbols
}

func symbolRange(content string, indices [][]int, idx int) (int, int) {
	start := indices[idx][0]
	end := len(content)
	if idx+1 < len(indices) {
		end = indices[idx+1][0]
	}

	blockStart := strings.Index(content[start:end], "{")
	if blockStart >= 0 {
		scanStart := start + blockStart
		if blockEnd := matchingBrace(content, scanStart); blockEnd > scanStart {
			return start, consumeTrailingNewline(content, blockEnd+1)
		}
	}

	return start, trimTrailingWhitespace(content, end)
}

func matchingBrace(content string, open int) int {
	depth := 0
	for i := open; i < len(content); i++ {
		switch content[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func consumeTrailingNewline(content string, idx int) int {
	for idx < len(content) && (content[idx] == '\n' || content[idx] == '\r') {
		idx++
	}
	return idx
}

func trimTrailingWhitespace(content string, idx int) int {
	for idx > 0 {
		switch content[idx-1] {
		case ' ', '\t', '\n', '\r':
			idx--
		default:
			return idx
		}
	}
	return idx
}

func lineNumber(content string, idx int) int {
	if idx <= 0 {
		return 1
	}
	return strings.Count(content[:idx], "\n") + 1
}

func scoreSymbol(symbol SymbolMatch, tokens []string, base int, relatedFiles []string) int {
	score := base
	lowerName := strings.ToLower(symbol.Name)
	lowerSnippet := strings.ToLower(symbol.Snippet)
	lowerPath := strings.ToLower(symbol.Path)
	for _, token := range tokens {
		if strings.Contains(lowerName, token) {
			score += 15 // Heavily increased from 6 to prioritize declarations
		}
		if strings.Contains(lowerSnippet, token) {
			// Snippet matches are now much lower value relative to name
			score += 1
		}
		if strings.Contains(lowerPath, token) {
			score += 2
		}
	}
	if strings.HasSuffix(lowerPath, "_test.go") {
		score -= 4
	}
	// Relationships (Gemini/Claude style) provide a strong boost
	score += len(relatedFiles) * 5 // Increased from 3
	return score
}

func minimumFocusedConfidence(tokens []string) int {
	if len(tokens) == 0 {
		return 8
	}
	return 6 + len(tokens)
}

func hasStructuralTokenMatch(symbol SymbolMatch, tokens []string) bool {
	lowerName := strings.ToLower(symbol.Name)
	lowerPath := strings.ToLower(symbol.Path)
	for _, token := range tokens {
		if strings.Contains(lowerName, token) || strings.Contains(lowerPath, token) {
			return true
		}
	}
	return false
}

func (s *LocalService) findRelatedFiles(ctx context.Context, workspace string, symbol SymbolMatch) ([]string, error) {
	terms := referenceTerms(symbol.Name)

	fullPath := filepath.Join(workspace, filepath.FromSlash(symbol.Path))
	content, err := os.ReadFile(fullPath)
	if err == nil {
		// Add same-package files as candidates
		dir := filepath.Dir(symbol.Path)
		if dir != "." && dir != "" {
			terms = append(terms, dir)
		}

		// Add imported packages as terms (Claude style)
		imports := extractGoImports(string(content))
		for _, imp := range imports {
			if strings.Contains(imp, "/") {
				parts := strings.Split(imp, "/")
				terms = append(terms, parts[len(parts)-1])
			} else {
				terms = append(terms, imp)
			}
		}
	}

	if len(terms) == 0 {
		return nil, nil
	}

	if related, used, err := findRelatedFilesWithRipgrep(ctx, workspace, symbol.Path, terms); used || err != nil {
		return related, err
	}
	return findRelatedFilesWithWalk(ctx, workspace, symbol.Path, terms)
}

func extractGoImports(content string) []string {
	matches := importPattern.FindAllStringSubmatch(content, -1)
	imports := make([]string, 0, len(matches))
	for _, match := range matches {
		if match[1] != "" {
			imports = append(imports, match[1])
		}
		if match[2] != "" {
			imports = append(imports, match[2])
		}
	}
	return imports
}

func referenceTerms(name string) []string {
	parts := strings.Split(name, ".")
	terms := make([]string, 0, len(parts)+1)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) < 3 {
			continue
		}
		terms = append(terms, part)
	}
	if len(terms) == 0 && len(name) >= 3 {
		terms = append(terms, name)
	}
	slices.Sort(terms)
	return slices.Compact(terms)
}

func findRelatedFilesWithRipgrep(ctx context.Context, workspace, origin string, terms []string) ([]string, bool, error) {
	if _, err := exec.LookPath("rg"); err != nil {
		return nil, false, nil
	}

	patterns := make([]string, 0, len(terms))
	for _, term := range terms {
		patterns = append(patterns, `\b`+regexp.QuoteMeta(term)+`\b`)
	}

	cmd := exec.CommandContext(
		ctx,
		"rg",
		"--files-with-matches",
		"--smart-case",
		strings.Join(patterns, "|"),
		workspace,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, false, nil
	}

	return normalizeRelatedFiles(strings.Split(strings.TrimSpace(string(out)), "\n"), workspace, origin), true, nil
}

func findRelatedFilesWithWalk(ctx context.Context, workspace, origin string, terms []string) ([]string, error) {
	related := make([]string, 0, 3)
	err := filepath.WalkDir(workspace, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != workspace {
				return filepath.SkipDir
			}
			return nil
		}

		info, statErr := d.Info()
		if statErr != nil || info.Size() > 256*1024 {
			return nil
		}

		rel, relErr := filepath.Rel(workspace, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == origin {
			return nil
		}

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		lowerContent := strings.ToLower(string(content))
		for _, term := range terms {
			if strings.Contains(lowerContent, strings.ToLower(term)) {
				related = append(related, rel)
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return clampRelatedFiles(related), nil
}

func normalizeRelatedFiles(lines []string, workspace, origin string) []string {
	related := make([]string, 0, len(lines))
	for _, line := range lines {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		rel, err := filepath.Rel(workspace, path)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == origin {
			continue
		}
		related = append(related, rel)
	}
	return clampRelatedFiles(related)
}

func clampRelatedFiles(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	sort.Slice(paths, func(i, j int) bool {
		if fileTypeBias(paths[i]) == fileTypeBias(paths[j]) {
			return paths[i] < paths[j]
		}
		return fileTypeBias(paths[i]) > fileTypeBias(paths[j])
	})
	paths = slices.Compact(paths)
	if len(paths) > 3 {
		paths = paths[:3]
	}
	return paths
}

func classifyFile(path string) fileKind {
	switch {
	case strings.HasSuffix(path, "_test.go"):
		return fileKindTest
	case strings.HasPrefix(path, "docs/"), strings.HasSuffix(path, ".md"), strings.HasSuffix(path, ".txt"):
		return fileKindDoc
	default:
		return fileKindSource
	}
}
