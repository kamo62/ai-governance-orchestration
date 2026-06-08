import { describe, expect, test } from 'bun:test';

import {
    buildContextualPrompt,
    contextSummary,
    parseBranchWorkItem,
    parseGitBranch,
    parseGitRemote,
    preserveExplicitContextAttachments,
    sanitiseRepositoryURL,
} from '../src/bridgeWorkspace';

describe('Bridge workspace context helpers', () => {
    test('builds a bounded prompt from current workspace context', () => {
        const prompt = buildContextualPrompt('Add tests for this code', {
            workspaceName: 'z83',
            activeFile: 'src/form.ts',
            languageId: 'typescript',
            selectedText: 'export function parseForm() { return true; }',
            selectedRange: '12-14',
            branch: 'main',
            repoUrl: 'https://github.com/example/z83.git',
        });

        expect(prompt).toContain('User request:\nAdd tests for this code');
        expect(prompt).toContain('workspace: z83');
        expect(prompt).toContain('active_file: src/form.ts');
        expect(prompt).toContain('selected_lines: 12-14');
        expect(prompt).toContain('export function parseForm');
        expect(prompt).not.toContain('/Users/');
    });

    test('caps large active-file excerpts before adding them to the prompt', () => {
        const prompt = buildContextualPrompt('Review this file', {
            workspaceName: 'demo',
            activeFile: 'src/large.ts',
            activeFileExcerpt: 'a'.repeat(200),
        }, 40);

        expect(prompt).toContain('active_file_excerpt_truncated: true');
        expect(prompt).toContain('a'.repeat(40));
        expect(prompt).not.toContain('a'.repeat(80));
    });

    test('parses branch and sanitised origin remote from git metadata', () => {
        expect(parseGitBranch('ref: refs/heads/feature/demo\n')).toBe('feature/demo');
        expect(parseGitBranch('1234567890abcdef\n')).toBe('detached:1234567');

        const config = `
[remote "origin"]
    url = https://token@example.com/org/repo.git
[remote "upstream"]
    url = git@github.com:org/upstream.git
`;
        expect(parseGitRemote(config)).toBe('https://example.com/org/repo.git');
    });

    test('parses branch work item metadata', () => {
        expect(parseBranchWorkItem('frontend/APP-123-navigation')).toEqual({
            workItemId: 'APP-123',
            workItemType: 'frontend',
            sourceSystem: 'jira',
        });
        expect(parseBranchWorkItem('backend/APP-124-api')).toEqual({
            workItemId: 'APP-124',
            workItemType: 'backend',
            sourceSystem: 'jira',
        });
        expect(parseBranchWorkItem('users/kamo/ADO-456-fix')).toEqual({
            workItemId: 'ADO-456',
            workItemType: 'feature',
            sourceSystem: 'ado',
        });
        expect(parseBranchWorkItem('bugfix/123-login')).toEqual({
            workItemId: '123',
            workItemType: 'bugfix',
            sourceSystem: 'github',
        });
    });

    test('preserves explicit attachments across fresh workspace scans', () => {
        const merged = preserveExplicitContextAttachments(
            {
                workspaceName: 'demo',
                branch: 'main',
                activeFile: 'src/current.ts',
            },
            {
                attachedFiles: [{ path: 'src/attached.ts', excerpt: 'export const kept = true;' }],
                searchHits: [{ path: 'src/search.ts', line: 12, preview: 'function searchHit' }],
                terminalExcerpt: 'last test output',
                localToolNotes: ['local note'],
            }
        );

        expect(merged.workspaceName).toBe('demo');
        expect(merged.activeFile).toBe('src/current.ts');
        expect(merged.attachedFiles?.[0].path).toBe('src/attached.ts');
        expect(merged.searchHits?.[0].preview).toBe('function searchHit');
        expect(merged.terminalExcerpt).toBe('last test output');
        expect(merged.localToolNotes?.[0]).toBe('local note');
    });

    test('summarises context without leaking selected text', () => {
        expect(sanitiseRepositoryURL('https://user:pass@example.com/org/repo.git')).toBe('https://example.com/org/repo.git');
        expect(contextSummary({
            workspaceName: 'z83',
            activeFile: 'src/form.ts',
            selectedText: 'secret text should not be logged',
        })).toBe('workspace=z83, active_file=src/form.ts, context=selection');
    });
});
