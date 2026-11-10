// Package store keeps local per-pull-request state (draft comments, viewed files)
// in a single JSON file. Writes are atomic (temp file + rename) and guarded by a
// mutex, so concurrent requests cannot corrupt the file.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
)

// ErrNotFound is returned when a draft id does not exist for a pull request.
var ErrNotFound = errors.New("not found")

// DefaultPath is the state file location: ~/.local/share/bb-review/state.json.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "state.json")
	}
	return filepath.Join(home, ".local", "share", "bb-review", "state.json")
}

// Store is the local state file. It is safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
	data persisted
}

type persisted struct {
	Drafts map[string][]bitbucket.Draft     `json:"drafts"`
	Viewed map[string]bitbucket.ViewedState `json:"viewed"`
}

// Open loads (or creates in memory) the state file at path.
func Open(path string) (*Store, error) {
	s := &Store{
		path: path,
		data: persisted{
			Drafts: make(map[string][]bitbucket.Draft),
			Viewed: make(map[string]bitbucket.ViewedState),
		},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Path returns the state file path.
func (s *Store) Path() string { return s.path }

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read state %s: %w", s.path, err)
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return fmt.Errorf("parse state %s: %w", s.path, err)
	}
	if s.data.Drafts == nil {
		s.data.Drafts = make(map[string][]bitbucket.Draft)
	}
	if s.data.Viewed == nil {
		s.data.Viewed = make(map[string]bitbucket.ViewedState)
	}
	return nil
}

// Drafts returns a copy of the drafts for a pull request, in creation order.
func (s *Store) Drafts(prKey string) []bitbucket.Draft {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.data.Drafts[prKey]
	out := make([]bitbucket.Draft, 0, len(stored))
	for _, d := range stored {
		out = append(out, d.Clone())
	}
	return out
}

// AddDraft appends a new draft and returns it.
func (s *Store) AddDraft(prKey, raw string, inline *bitbucket.InlineAnchor, parentID *int) (bitbucket.Draft, error) {
	id, err := newID()
	if err != nil {
		return bitbucket.Draft{}, err
	}
	draft := bitbucket.Draft{
		ID:        id,
		Raw:       raw,
		Inline:    inline.Clone(),
		CreatedOn: time.Now().UTC().Format(time.RFC3339),
	}
	if parentID != nil {
		parent := *parentID
		draft.ParentID = &parent
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Drafts[prKey] = append(s.data.Drafts[prKey], draft)
	if err := s.persist(); err != nil {
		return bitbucket.Draft{}, err
	}
	return draft.Clone(), nil
}

// UpdateDraft replaces the raw text of an existing draft.
func (s *Store) UpdateDraft(prKey, id, raw string) (bitbucket.Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	drafts := s.data.Drafts[prKey]
	for i := range drafts {
		if drafts[i].ID != id {
			continue
		}
		drafts[i].Raw = raw
		if err := s.persist(); err != nil {
			return bitbucket.Draft{}, err
		}
		return drafts[i].Clone(), nil
	}
	return bitbucket.Draft{}, ErrNotFound
}

// DeleteDraft removes a draft.
func (s *Store) DeleteDraft(prKey, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	drafts := s.data.Drafts[prKey]
	for i := range drafts {
		if drafts[i].ID != id {
			continue
		}
		remaining := append(drafts[:i:i], drafts[i+1:]...)
		if len(remaining) == 0 {
			delete(s.data.Drafts, prKey)
		} else {
			s.data.Drafts[prKey] = remaining
		}
		return s.persist()
	}
	return ErrNotFound
}

// Viewed returns the viewed-file state for a pull request (never nil map).
func (s *Store) Viewed(prKey string) bitbucket.ViewedState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.data.Viewed[prKey]
	if !ok {
		return bitbucket.ViewedState{Files: map[string]string{}}
	}
	return state.Clone()
}

// SetViewed replaces the viewed-file state for a pull request.
func (s *Store) SetViewed(prKey string, state bitbucket.ViewedState) (bitbucket.ViewedState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := state.Clone()
	if len(stored.Files) == 0 {
		delete(s.data.Viewed, prKey)
	} else {
		s.data.Viewed[prKey] = stored
	}
	if err := s.persist(); err != nil {
		return bitbucket.ViewedState{}, err
	}
	return stored.Clone(), nil
}

// persist writes the state file atomically. Callers must hold s.mu.
func (s *Store) persist() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir %s: %w", dir, err)
	}

	encoded, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	encoded = append(encoded, '\n')

	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(encoded); err != nil {
		cleanup()
		return fmt.Errorf("write temp state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync temp state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp state file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("chmod temp state file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace state file: %w", err)
	}
	return nil
}

// newID returns a random 32 character hex id (16 random bytes).
func newID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate draft id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}
