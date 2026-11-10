package bitbucket

import (
	"html"
	"strings"
)

// apiUser is the Bitbucket user schema.
type apiUser struct {
	UUID        string   `json:"uuid"`
	AccountID   string   `json:"account_id"`
	DisplayName string   `json:"display_name"`
	Nickname    string   `json:"nickname"`
	Links       apiLinks `json:"links"`
}

func (u apiUser) toUser() User {
	return User{
		UUID:        u.UUID,
		AccountID:   u.AccountID,
		DisplayName: u.DisplayName,
		Nickname:    u.Nickname,
		AvatarURL:   u.Links.Avatar.Href,
	}
}

// apiLinks holds the subset of Bitbucket link objects we use.
type apiLinks struct {
	Avatar apiHref `json:"avatar"`
	HTML   apiHref `json:"html"`
}

type apiHref struct {
	Href string `json:"href"`
}

type apiParticipant struct {
	User     apiUser `json:"user"`
	Role     string  `json:"role"`
	State    string  `json:"state"`
	Approved bool    `json:"approved"`
}

type apiBranchRef struct {
	Branch struct {
		Name string `json:"name"`
	} `json:"branch"`
	Commit struct {
		Hash string `json:"hash"`
	} `json:"commit"`
}

// apiPR is the Bitbucket pull request schema (only the fields we read).
type apiPR struct {
	ID           int              `json:"id"`
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	State        string           `json:"state"`
	CreatedOn    string           `json:"created_on"`
	UpdatedOn    string           `json:"updated_on"`
	CommentCount int              `json:"comment_count"`
	TaskCount    int              `json:"task_count"`
	Author       apiUser          `json:"author"`
	Participants []apiParticipant `json:"participants"`
	Source       apiBranchRef     `json:"source"`
	Destination  apiBranchRef     `json:"destination"`
	Links        apiLinks         `json:"links"`
	Rendered     struct {
		Description struct {
			HTML string `json:"html"`
		} `json:"description"`
	} `json:"rendered"`
}

// reviewStateOf maps one participant to a ReviewState.
func reviewStateOf(p apiParticipant) ReviewState {
	if p.Approved {
		return ReviewApproved
	}
	if p.State == string(ReviewChangesRequested) {
		return ReviewChangesRequested
	}
	return ReviewNone
}

// reviewSummary returns the current user's state and the approval count.
func reviewSummary(participants []apiParticipant, meUUID string) (ReviewState, int) {
	myState := ReviewNone
	approvals := 0
	for _, p := range participants {
		if p.Approved {
			approvals++
		}
		if p.User.UUID == meUUID && p.User.UUID != "" {
			myState = reviewStateOf(p)
		}
	}
	return myState, approvals
}

func participantsOf(participants []apiParticipant) []Participant {
	out := make([]Participant, 0, len(participants))
	for _, p := range participants {
		out = append(out, Participant{
			User:  p.User.toUser(),
			Role:  p.Role,
			State: reviewStateOf(p),
		})
	}
	return out
}

func (a apiPR) toSummary(repo Repo, meUUID string) PRSummary {
	myState, approvals := reviewSummary(a.Participants, meUUID)
	return PRSummary{
		Workspace:    repo.Workspace,
		Repo:         repo.Repo,
		ID:           a.ID,
		Title:        a.Title,
		Author:       a.Author.toUser(),
		State:        a.State,
		SourceBranch: a.Source.Branch.Name,
		DestBranch:   a.Destination.Branch.Name,
		CreatedOn:    a.CreatedOn,
		UpdatedOn:    a.UpdatedOn,
		CommentCount: a.CommentCount,
		TaskCount:    a.TaskCount,
		MyState:      myState,
		Approvals:    approvals,
		BuildState:   BuildNone,
		URL:          a.Links.HTML.Href,
	}
}

func (a apiPR) toDetail(repo Repo, meUUID string) PRDetail {
	summary := a.toSummary(repo, meUUID)
	return PRDetail{
		PRSummary:       summary,
		DescriptionRaw:  a.Description,
		DescriptionHTML: descriptionHTML(a),
		Participants:    participantsOf(a.Participants),
		SourceCommit:    a.Source.Commit.Hash,
		DestCommit:      a.Destination.Commit.Hash,
		MergeBase:       a.Destination.Commit.Hash,
		Builds:          []BuildStatus{},
	}
}

// descriptionHTML prefers Bitbucket's rendered HTML and falls back to escaped
// markdown in paragraphs when the API does not return a rendered description.
func descriptionHTML(a apiPR) string {
	if rendered := strings.TrimSpace(a.Rendered.Description.HTML); rendered != "" {
		return rendered
	}
	raw := strings.TrimSpace(a.Description)
	if raw == "" {
		return ""
	}
	parts := make([]string, 0, 4)
	for _, para := range strings.Split(raw, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		parts = append(parts, "<p>"+html.EscapeString(para)+"</p>")
	}
	return strings.Join(parts, "")
}

// apiComment is the Bitbucket comment schema.
type apiComment struct {
	ID     int `json:"id"`
	Parent *struct {
		ID int `json:"id"`
	} `json:"parent"`
	User    apiUser `json:"user"`
	Content struct {
		Raw  string `json:"raw"`
		HTML string `json:"html"`
	} `json:"content"`
	CreatedOn string     `json:"created_on"`
	UpdatedOn string     `json:"updated_on"`
	Inline    *apiInline `json:"inline"`
	Deleted   bool       `json:"deleted"`
	Outdated  bool       `json:"outdated"`
}

func (a apiComment) toComment() Comment {
	out := Comment{
		ID:        a.ID,
		Author:    a.User.toUser(),
		Raw:       a.Content.Raw,
		HTML:      a.Content.HTML,
		CreatedOn: a.CreatedOn,
		UpdatedOn: a.UpdatedOn,
		Inline:    a.Inline.toAnchor(),
		Deleted:   a.Deleted,
		Outdated:  a.Outdated,
	}
	if a.Parent != nil {
		parent := a.Parent.ID
		out.ParentID = &parent
	}
	return out
}

// apiInline is the inline anchor schema used both ways.
type apiInline struct {
	Path string `json:"path"`
	From *int   `json:"from,omitempty"`
	To   *int   `json:"to,omitempty"`
}

func (a *apiInline) toAnchor() *InlineAnchor {
	if a == nil {
		return nil
	}
	return &InlineAnchor{Path: a.Path, From: a.From, To: a.To}
}

func inlineFromAnchor(a *InlineAnchor) *apiInline {
	if a == nil {
		return nil
	}
	return &apiInline{Path: a.Path, From: a.From, To: a.To}
}

// apiCommentPost is the POST /comments request body.
type apiCommentPost struct {
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
	Inline *apiInline `json:"inline,omitempty"`
	Parent *apiParent `json:"parent,omitempty"`
}

type apiParent struct {
	ID int `json:"id"`
}

// apiStatus is one commit status.
type apiStatus struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	State string `json:"state"`
	URL   string `json:"url"`
}

func (a apiStatus) toBuildStatus() BuildStatus {
	state := BuildState(a.State)
	switch state {
	case BuildSuccessful, BuildFailed, BuildInProgress, BuildStopped:
	default:
		state = BuildNone
	}
	return BuildStatus{Key: a.Key, Name: a.Name, State: state, URL: a.URL}
}

// AggregateBuildStates folds individual status states into one BuildState.
func AggregateBuildStates(states []string) BuildState {
	if len(states) == 0 {
		return BuildNone
	}
	allSuccessful := true
	anyInProgress := false
	for _, s := range states {
		switch BuildState(s) {
		case BuildFailed:
			return BuildFailed
		case BuildInProgress:
			anyInProgress = true
			allSuccessful = false
		case BuildSuccessful:
		default:
			allSuccessful = false
		}
	}
	if anyInProgress {
		return BuildInProgress
	}
	if allSuccessful {
		return BuildSuccessful
	}
	return BuildStopped
}

func aggregateBuilds(builds []BuildStatus) BuildState {
	states := make([]string, 0, len(builds))
	for _, b := range builds {
		states = append(states, string(b.State))
	}
	return AggregateBuildStates(states)
}
