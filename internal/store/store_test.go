package store

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, path
}

func intPtr(v int) *int { return &v }

func TestOpenMissingFileStartsEmpty(t *testing.T) {
	s, path := newStore(t)
	if drafts := s.Drafts("ws/repo/1"); len(drafts) != 0 {
		t.Fatalf("drafts = %v, want empty", drafts)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state file should not exist yet: %v", err)
	}
	viewed := s.Viewed("ws/repo/1")
	if viewed.Files == nil || len(viewed.Files) != 0 {
		t.Fatalf("viewed = %+v, want empty map", viewed)
	}
}

func TestRoundTrip(t *testing.T) {
	s, path := newStore(t)
	key := "ws/repo/42"

	first, err := s.AddDraft(key, "first draft", &bitbucket.InlineAnchor{Path: "src/a.go", To: intPtr(12)}, nil)
	if err != nil {
		t.Fatalf("AddDraft: %v", err)
	}
	if len(first.ID) != 32 {
		t.Fatalf("draft id = %q, want 32 hex chars", first.ID)
	}
	parent := 7
	if _, err := s.AddDraft(key, "a reply", nil, &parent); err != nil {
		t.Fatalf("AddDraft reply: %v", err)
	}
	if _, err := s.UpdateDraft(key, first.ID, "edited draft"); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	if _, err := s.SetViewed(key, bitbucket.ViewedState{Files: map[string]string{"src/a.go": "hash-1"}}); err != nil {
		t.Fatalf("SetViewed: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	drafts := reopened.Drafts(key)
	if len(drafts) != 2 {
		t.Fatalf("drafts = %d, want 2", len(drafts))
	}
	if drafts[0].ID != first.ID || drafts[0].Raw != "edited draft" {
		t.Fatalf("first draft = %+v", drafts[0])
	}
	if drafts[0].Inline == nil || drafts[0].Inline.Line() != 12 {
		t.Fatalf("inline anchor lost: %+v", drafts[0].Inline)
	}
	if drafts[1].ParentID == nil || *drafts[1].ParentID != 7 {
		t.Fatalf("parent id lost: %+v", drafts[1])
	}
	if drafts[1].CreatedOn == "" {
		t.Fatal("created_on not set")
	}

	viewed := reopened.Viewed(key)
	if viewed.Files["src/a.go"] != "hash-1" {
		t.Fatalf("viewed = %+v", viewed)
	}

	if err := reopened.DeleteDraft(key, first.ID); err != nil {
		t.Fatalf("DeleteDraft: %v", err)
	}
	after, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after delete: %v", err)
	}
	if got := after.Drafts(key); len(got) != 1 || got[0].ID == first.ID {
		t.Fatalf("drafts after delete = %+v", got)
	}
}

func TestDraftsAreScopedPerPullRequest(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.AddDraft("ws/repo/1", "one", nil, nil); err != nil {
		t.Fatalf("AddDraft: %v", err)
	}
	if _, err := s.AddDraft("ws/repo/2", "two", nil, nil); err != nil {
		t.Fatalf("AddDraft: %v", err)
	}
	if got := len(s.Drafts("ws/repo/1")); got != 1 {
		t.Fatalf("pr 1 drafts = %d", got)
	}
	if got := len(s.Drafts("ws/repo/2")); got != 1 {
		t.Fatalf("pr 2 drafts = %d", got)
	}
}

func TestMissingDraftReturnsErrNotFound(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.UpdateDraft("ws/repo/1", "nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateDraft err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteDraft("ws/repo/1", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteDraft err = %v, want ErrNotFound", err)
	}
}

func TestConcurrentWrites(t *testing.T) {
	s, path := newStore(t)
	const (
		writers   = 20
		perWriter = 5
	)
	key := "ws/repo/9"

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				if _, err := s.AddDraft(key, "draft", nil, nil); err != nil {
					t.Errorf("AddDraft: %v", err)
					return
				}
				if _, err := s.SetViewed(key, bitbucket.ViewedState{Files: map[string]string{"f.go": "h"}}); err != nil {
					t.Errorf("SetViewed: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	want := writers * perWriter
	if got := len(s.Drafts(key)); got != want {
		t.Fatalf("in-memory drafts = %d, want %d", got, want)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := len(reopened.Drafts(key)); got != want {
		t.Fatalf("persisted drafts = %d, want %d", got, want)
	}
	ids := make(map[string]struct{}, want)
	for _, d := range reopened.Drafts(key) {
		if _, dup := ids[d.ID]; dup {
			t.Fatalf("duplicate draft id %q", d.ID)
		}
		ids[d.ID] = struct{}{}
	}
}

func TestStateFilePermissions(t *testing.T) {
	s, path := newStore(t)
	if _, err := s.AddDraft("ws/repo/1", "x", nil, nil); err != nil {
		t.Fatalf("AddDraft: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("state file mode = %o, want 600", perm)
	}
}

func TestCorruptStateFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("expected an error for a corrupt state file")
	}
}
