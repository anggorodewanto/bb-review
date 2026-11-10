// Package bitbucket is a small client for the Bitbucket Cloud REST 2.0 API.
//
// It also holds the normalized types from docs/API.md so that the store and the
// HTTP server can share them without an extra package.
package bitbucket

import "fmt"

// ReviewState is a reviewer's state on a pull request.
type ReviewState string

const (
	ReviewApproved         ReviewState = "approved"
	ReviewChangesRequested ReviewState = "changes_requested"
	ReviewNone             ReviewState = "none"
)

// BuildState is the aggregate state of the commit statuses of a PR head commit.
type BuildState string

const (
	BuildSuccessful BuildState = "SUCCESSFUL"
	BuildFailed     BuildState = "FAILED"
	BuildInProgress BuildState = "INPROGRESS"
	BuildStopped    BuildState = "STOPPED"
	BuildNone       BuildState = "NONE"
)

// User is a Bitbucket user as exposed to the frontend.
type User struct {
	UUID        string `json:"uuid"`
	AccountID   string `json:"account_id"`
	DisplayName string `json:"display_name"`
	Nickname    string `json:"nickname"`
	AvatarURL   string `json:"avatar_url"`
}

// Repo identifies a repository. Workspace is the workspace slug, Repo the repo slug.
type Repo struct {
	Workspace string
	Repo      string
}

// String returns "workspace/repo".
func (r Repo) String() string { return r.Workspace + "/" + r.Repo }

// PRKey is the store key for a pull request: "workspace/repo/id".
func (r Repo) PRKey(id int) string { return fmt.Sprintf("%s/%s/%d", r.Workspace, r.Repo, id) }

// InlineAnchor points a comment at a line of the diff. Exactly one of From/To is
// set for line comments: To for added/context lines, From for removed lines.
type InlineAnchor struct {
	Path string `json:"path"`
	From *int   `json:"from"`
	To   *int   `json:"to"`
}

// Clone returns a deep copy so callers cannot mutate shared state.
func (a *InlineAnchor) Clone() *InlineAnchor {
	if a == nil {
		return nil
	}
	out := &InlineAnchor{Path: a.Path}
	if a.From != nil {
		v := *a.From
		out.From = &v
	}
	if a.To != nil {
		v := *a.To
		out.To = &v
	}
	return out
}

// Line returns the anchored line number, or 0 when the anchor has no line.
func (a *InlineAnchor) Line() int {
	if a == nil {
		return 0
	}
	if a.To != nil {
		return *a.To
	}
	if a.From != nil {
		return *a.From
	}
	return 0
}

// PRSummary is one row of the pull request list.
type PRSummary struct {
	Workspace    string      `json:"workspace"`
	Repo         string      `json:"repo"`
	ID           int         `json:"id"`
	Title        string      `json:"title"`
	Author       User        `json:"author"`
	State        string      `json:"state"`
	SourceBranch string      `json:"source_branch"`
	DestBranch   string      `json:"dest_branch"`
	CreatedOn    string      `json:"created_on"`
	UpdatedOn    string      `json:"updated_on"`
	CommentCount int         `json:"comment_count"`
	TaskCount    int         `json:"task_count"`
	MyState      ReviewState `json:"my_state"`
	Approvals    int         `json:"approvals"`
	BuildState   BuildState  `json:"build_state"`
	URL          string      `json:"url"`
}

// Participant is one reviewer on a pull request.
type Participant struct {
	User  User        `json:"user"`
	Role  string      `json:"role"`
	State ReviewState `json:"state"`
}

// BuildStatus is one CI status attached to a commit.
type BuildStatus struct {
	Key   string     `json:"key"`
	Name  string     `json:"name"`
	State BuildState `json:"state"`
	URL   string     `json:"url"`
}

// PRDetail is the full pull request view.
type PRDetail struct {
	PRSummary
	DescriptionRaw  string        `json:"description_raw"`
	DescriptionHTML string        `json:"description_html"`
	Participants    []Participant `json:"participants"`
	SourceCommit    string        `json:"source_commit"`
	DestCommit      string        `json:"dest_commit"`
	MergeBase       string        `json:"merge_base"`
	Builds          []BuildStatus `json:"builds"`
}

// Comment is a Bitbucket comment, including inline anchors.
type Comment struct {
	ID        int           `json:"id"`
	ParentID  *int          `json:"parent_id"`
	Author    User          `json:"author"`
	Raw       string        `json:"raw"`
	HTML      string        `json:"html"`
	CreatedOn string        `json:"created_on"`
	UpdatedOn string        `json:"updated_on"`
	Inline    *InlineAnchor `json:"inline"`
	Deleted   bool          `json:"deleted"`
	Outdated  bool          `json:"outdated"`
}

// Draft is a locally stored, not yet posted comment.
type Draft struct {
	ID        string        `json:"id"`
	Raw       string        `json:"raw"`
	Inline    *InlineAnchor `json:"inline"`
	ParentID  *int          `json:"parent_id"`
	CreatedOn string        `json:"created_on"`
}

// Clone returns a deep copy of the draft.
func (d Draft) Clone() Draft {
	out := d
	out.Inline = d.Inline.Clone()
	if d.ParentID != nil {
		v := *d.ParentID
		out.ParentID = &v
	}
	return out
}

// ViewedState maps a file path to the hash of its diff text when it was marked viewed.
type ViewedState struct {
	Files map[string]string `json:"files"`
}

// Clone returns a copy of the viewed state with its own map.
func (v ViewedState) Clone() ViewedState {
	out := ViewedState{Files: make(map[string]string, len(v.Files))}
	for k, val := range v.Files {
		out.Files[k] = val
	}
	return out
}
