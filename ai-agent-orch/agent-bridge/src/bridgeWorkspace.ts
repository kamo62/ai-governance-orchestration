export const DEFAULT_CONTEXT_CHAR_LIMIT = 12000;

export interface WorkspacePromptContext {
    workspaceName?: string;
    repoUrl?: string;
    branch?: string;
    activeFile?: string;
    languageId?: string;
    selectedText?: string;
    selectedRange?: string;
    activeFileExcerpt?: string;
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
