export const DEFAULT_CONTEXT_CHAR_LIMIT = 12000;

export interface AttachedFileContext {
    path: string;
    excerpt?: string;
    truncated?: boolean;
}

export interface SearchHitContext {
    path: string;
    line?: number;
    preview: string;
}

export interface WorkspacePromptContext {
    workspaceName?: string;
    repoUrl?: string;
    branch?: string;
    workItemId?: string;
    workItemType?: string;
    sourceSystem?: string;
    activeFile?: string;
    languageId?: string;
    selectedText?: string;
    selectedRange?: string;
    activeFileExcerpt?: string;
    attachedFiles?: AttachedFileContext[];
    terminalExcerpt?: string;
    searchHits?: SearchHitContext[];
    priorSessionId?: string;
    localToolNotes?: string[];
}

export function preserveExplicitContextAttachments(
    fresh: WorkspacePromptContext,
    previous: WorkspacePromptContext
): WorkspacePromptContext {
    return {
        ...fresh,
        attachedFiles: previous.attachedFiles?.length ? previous.attachedFiles : fresh.attachedFiles,
        terminalExcerpt: previous.terminalExcerpt || fresh.terminalExcerpt,
        searchHits: previous.searchHits?.length ? previous.searchHits : fresh.searchHits,
        priorSessionId: previous.priorSessionId || fresh.priorSessionId,
        localToolNotes: previous.localToolNotes?.length ? previous.localToolNotes : fresh.localToolNotes,
    };
}

export function buildContextualPrompt(userPrompt: string, context: WorkspacePromptContext, limit = DEFAULT_CONTEXT_CHAR_LIMIT): string {
    const lines: string[] = [
        'User request:',
        userPrompt,
    ];

    const metadata = contextMetadataLines(context);
    const textContext = context.selectedText || context.activeFileExcerpt || '';
    if (metadata.length === 0 && !textContext.trim()) {
        return userPrompt;
    }

    lines.push('', 'Bounded workspace context:');
    lines.push(...metadata);

    if (textContext.trim()) {
        const label = context.selectedText ? 'selected_text' : 'active_file_excerpt';
        const { text, truncated } = trimContextText(textContext, limit);
        if (truncated) {
            lines.push(`${label}_truncated: true`);
        }
        lines.push(`${label}:`);
        lines.push('```');
        lines.push(text);
        lines.push('```');
    }

    if (context.terminalExcerpt?.trim()) {
        const { text, truncated } = trimContextText(context.terminalExcerpt, limit);
        if (truncated) {
            lines.push('terminal_excerpt_truncated: true');
        }
        lines.push('terminal_excerpt:');
        lines.push('```');
        lines.push(text);
        lines.push('```');
    }

    for (const file of context.attachedFiles || []) {
        if (!file.excerpt?.trim()) {
            lines.push(`attached_file: ${file.path}`);
            continue;
        }
        const { text, truncated } = trimContextText(file.excerpt, limit);
        lines.push(`attached_file: ${file.path}${truncated ? ' (truncated)' : ''}`);
        lines.push('```');
        lines.push(text);
        lines.push('```');
    }

    for (const hit of context.searchHits || []) {
        const loc = hit.line ? `${hit.path}:${hit.line}` : hit.path;
        lines.push(`search_hit: ${loc}`);
        lines.push(hit.preview.slice(0, 500));
    }

    if (context.priorSessionId) {
        lines.push(`prior_governed_session: ${context.priorSessionId}`);
    }

    if (context.localToolNotes?.length) {
        lines.push('', 'Local tool results (user-approved):');
        for (const note of context.localToolNotes) {
            lines.push(note.slice(0, 2000));
        }
    }

    lines.push('', 'Use only this bounded context plus the user request. Do not assume unseen files unless you ask for them.');
    return lines.join('\n');
}

export function contextSummary(context: WorkspacePromptContext): string {
    const parts: string[] = [];
    if (context.workspaceName) {
        parts.push(`workspace=${context.workspaceName}`);
    }
    if (context.activeFile) {
        parts.push(`active_file=${context.activeFile}`);
    }
    if (context.selectedText?.trim()) {
        parts.push('context=selection');
    } else if (context.activeFileExcerpt?.trim()) {
        parts.push('context=active_file_excerpt');
    } else {
        parts.push('context=metadata_only');
    }
    return parts.join(', ');
}

export function parseGitBranch(headContent: string): string {
    const head = headContent.trim();
    const prefix = 'ref: refs/heads/';
    if (head.startsWith(prefix)) {
        return head.substring(prefix.length);
    }
    if (/^[a-f0-9]{7,40}$/i.test(head)) {
        return `detached:${head.substring(0, 7)}`;
    }
    return '';
}

export function parseGitRemote(configContent: string, remoteName = 'origin'): string {
	const lines = configContent.split(/\r?\n/);
	let inRemote = false;
	for (const line of lines) {
        const section = line.match(/^\s*\[remote "(.+)"\]\s*$/);
        if (section) {
            inRemote = section[1] === remoteName;
            continue;
        }
        if (!inRemote) {
            continue;
        }
        const url = line.match(/^\s*url\s*=\s*(.+?)\s*$/);
        if (url) {
            return sanitiseRepositoryURL(url[1]);
        }
	}
	return '';
}

export function parseBranchWorkItem(branch: string): { workItemId?: string; workItemType?: string; sourceSystem?: string } {
    if (!branch) {
        return {};
    }
    const workItemType = inferWorkItemType(branch.split('/')[0] || '');
    const patterns: Array<{ source: string; pattern: RegExp }> = [
        { source: 'ado', pattern: /(?:users\/[^/]+\/)([A-Z]+-\d+)(?:-|$)/i },
        { source: 'jira', pattern: /(?:^|\/)([A-Z]+-\d+)(?:-|$)/i },
        { source: 'github', pattern: /(?:^|\/)(\d+)(?:-|$)/ },
    ];
    for (const candidate of patterns) {
        const match = branch.match(candidate.pattern);
        if (match?.[1]) {
            return {
                workItemId: match[1],
                workItemType: workItemType || 'feature',
                sourceSystem: candidate.source,
            };
        }
    }
    return workItemType ? { workItemType } : {};
}

function inferWorkItemType(prefix: string): string {
    switch (prefix.toLowerCase()) {
        case 'frontend':
        case 'ui':
            return 'frontend';
        case 'backend':
        case 'api':
            return 'backend';
        case 'feature':
        case 'feat':
            return 'feature';
        case 'bugfix':
        case 'fix':
        case 'hotfix':
            return 'bugfix';
        case 'docs':
        case 'doc':
            return 'docs';
        case 'refactor':
            return 'refactor';
        case 'test':
        case 'tests':
            return 'test';
        case 'security':
        case 'sec':
            return 'security';
        default:
            return '';
    }
}

export function sanitiseRepositoryURL(value: string): string {
    const raw = value.trim();
    if (!raw) {
        return '';
    }
    try {
        const url = new URL(raw);
        url.username = '';
        url.password = '';
        return url.toString();
    } catch {
        return raw;
    }
}

function contextMetadataLines(context: WorkspacePromptContext): string[] {
    const lines: string[] = [];
    if (context.workspaceName) {
        lines.push(`workspace: ${context.workspaceName}`);
    }
    if (context.repoUrl) {
        lines.push(`repo_url: ${context.repoUrl}`);
    }
    if (context.branch) {
        lines.push(`branch: ${context.branch}`);
    }
    if (context.workItemId) {
        lines.push(`work_item_id: ${context.workItemId}`);
    }
    if (context.workItemType) {
        lines.push(`work_item_type: ${context.workItemType}`);
    }
    if (context.activeFile) {
        lines.push(`active_file: ${context.activeFile}`);
    }
    if (context.languageId) {
        lines.push(`language: ${context.languageId}`);
    }
    if (context.selectedRange) {
        lines.push(`selected_lines: ${context.selectedRange}`);
    }
    return lines;
}

function trimContextText(value: string, limit: number): { text: string; truncated: boolean } {
    const trimmed = value.trimEnd();
    if (limit <= 0 || trimmed.length <= limit) {
        return { text: trimmed, truncated: false };
    }
    return { text: trimmed.slice(0, limit), truncated: true };
}
