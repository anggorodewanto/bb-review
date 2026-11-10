// Types mirroring docs/API.md exactly. Do not diverge without updating the contract.

export type User = {
    uuid: string;
    account_id: string;
    display_name: string;
    nickname: string;
    avatar_url: string;
};

export type ReviewState = 'approved' | 'changes_requested' | 'none';

export type BuildState = 'SUCCESSFUL' | 'FAILED' | 'INPROGRESS' | 'STOPPED' | 'NONE';

export type PRSummary = {
    workspace: string;
    repo: string;
    id: number;
    title: string;
    author: User;
    state: 'OPEN' | 'MERGED' | 'DECLINED' | 'SUPERSEDED';
    source_branch: string;
    dest_branch: string;
    created_on: string;
    updated_on: string;
    comment_count: number;
    task_count: number;
    my_state: ReviewState;
    approvals: number;
    build_state: BuildState;
    url: string;
};

export type PRRef = {
    workspace: string;
    repo: string;
    id: number;
};

export type Participant = {
    user: User;
    role: 'REVIEWER' | 'PARTICIPANT';
    state: ReviewState;
};

export type BuildStatus = {
    key: string;
    name: string;
    state: BuildState;
    url: string;
};

export type PRDetail = PRSummary & {
    description_raw: string;
    description_html: string;
    participants: Participant[];
    source_commit: string;
    dest_commit: string;
    merge_base: string;
    builds: BuildStatus[];
};

export type InlineAnchor = {
    path: string;
    from: number | null;
    to: number | null;
};

export type Comment = {
    id: number;
    parent_id: number | null;
    author: User;
    raw: string;
    html: string;
    created_on: string;
    updated_on: string;
    inline: InlineAnchor | null;
    deleted: boolean;
    outdated: boolean;
};

export type Draft = {
    id: string;
    raw: string;
    inline: InlineAnchor | null;
    parent_id: number | null;
    created_on: string;
};

export type ViewedState = {
    files: Record<string, string>;
};

export type SubmitResult = {
    posted: Comment[];
    failed: Array<{draft: Draft; error: string}>;
};

export type ApiErrorBody = {
    error: string;
    status: number;
};
