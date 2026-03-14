package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Store struct {
	root string
	mu   sync.Mutex
}

type SessionMeta struct {
	ID        string    `json:"id"`
	Task      string    `json:"task"`
	Workspace string    `json:"workspace"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
}

type SessionEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Kind      string    `json:"kind"`
	Summary   string    `json:"summary"`
}

type Fact struct {
	Timestamp time.Time `json:"timestamp"`
	Category  string    `json:"category"`
	Summary   string    `json:"summary"`
}

func NewStore(workspace string) (*Store, error) {
	root := filepath.Join(workspace, ".projectkitty")
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

func (s *Store) StartSession(task, workspace string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := time.Now().UTC().Format("20060102T150405Z")
	meta := SessionMeta{
		ID:        id,
		Task:      task,
		Workspace: workspace,
		StartedAt: time.Now().UTC(),
	}
	return id, s.writeJSON(s.sessionMetaPath(id), meta)
}

func (s *Store) EndSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta := SessionMeta{}
	if err := s.readJSON(s.sessionMetaPath(id), &meta); err != nil {
		return err
	}
	meta.EndedAt = time.Now().UTC()
	return s.writeJSON(s.sessionMetaPath(id), meta)
}

func (s *Store) RecordSessionEvent(id, kind, summary string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	event := SessionEvent{
		Timestamp: time.Now().UTC(),
		Kind:      kind,
		Summary:   summary,
	}
	line, err := json.Marshal(event)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(s.sessionLogPath(id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := fmt.Fprintln(f, string(line)); err != nil {
		return err
	}
	return nil
}

func (s *Store) SaveFact(fact Fact) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if fact.Timestamp.IsZero() {
		fact.Timestamp = time.Now().UTC()
	}

	facts := make([]Fact, 0, 8)
	_ = s.readJSON(s.factsPath(), &facts)
	facts = append(facts, fact)
	return s.writeJSON(s.factsPath(), facts)
}

func (s *Store) RecentFacts(limit int) ([]Fact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var facts []Fact
	if err := s.readJSON(s.factsPath(), &facts); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if limit <= 0 || len(facts) <= limit {
		return facts, nil
	}
	return facts[len(facts)-limit:], nil
}

func (s *Store) sessionMetaPath(id string) string {
	return filepath.Join(s.root, "sessions", id+".json")
}

func (s *Store) sessionLogPath(id string) string {
	return filepath.Join(s.root, "sessions", id+".jsonl")
}

func (s *Store) factsPath() string {
	return filepath.Join(s.root, "project-memory.json")
}

func (s *Store) writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (s *Store) readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
