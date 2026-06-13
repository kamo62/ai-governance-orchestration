import { Buffer } from 'buffer';
import * as vscode from 'vscode';

import {
    AttachedFileContext,
    DEFAULT_CONTEXT_CHAR_LIMIT,
    SearchHitContext,
    WorkspacePromptContext,
    parseBranchWorkItem,
    parseGitBranch,
    parseGitRemote,
} from './bridgeWorkspace';
import { ContextAttachment, attachmentsFromContext } from './bridgeAgentTypes';

export async function collectWorkspacePromptContext(
    outputChannel?: vscode.OutputChannel
): Promise<WorkspacePromptContext> {
    const workspaceFolder = vscode.workspace.workspaceFolders?.[0];
    const editor = vscode.window.activeTextEditor;
    const context: WorkspacePromptContext = {};

    if (workspaceFolder) {
        context.workspaceName = workspaceFolder.name;
        const gitMetadata = await readGitMetadata(workspaceFolder.uri, outputChannel);
        context.branch = gitMetadata.branch;
        context.repoUrl = gitMetadata.repoUrl;
        const workItem = parseBranchWorkItem(gitMetadata.branch || '');
        context.workItemId = workItem.workItemId;
        context.workItemType = workItem.workItemType;
        context.sourceSystem = workItem.sourceSystem;
    }

    if (editor && (!workspaceFolder || editor.document.uri.scheme === 'file')) {
        const relative = workspaceFolder
            ? vscode.workspace.asRelativePath(editor.document.uri, false)
            : editor.document.uri.fsPath.split(/[\\/]+/).pop();
        context.activeFile = relative || undefined;
        context.languageId = editor.document.languageId;

        if (!editor.selection.isEmpty) {
            context.selectedText = editor.document.getText(editor.selection);
            context.selectedRange = `${editor.selection.start.line + 1}-${editor.selection.end.line + 1}`;
        } else {
            context.activeFileExcerpt = editor.document.getText().slice(0, DEFAULT_CONTEXT_CHAR_LIMIT);
        }
    }

    return context;
}

export function workspaceLabelFromContext(context: WorkspacePromptContext): string {
    return context.workspaceName || 'No workspace folder';
}

export function contextAttachments(context: WorkspacePromptContext): ContextAttachment[] {
    return attachmentsFromContext(context);
}

export async function attachSelectionToContext(
    base: WorkspacePromptContext
): Promise<WorkspacePromptContext> {
    const editor = vscode.window.activeTextEditor;
    if (!editor || editor.selection.isEmpty) {
        throw new Error('Select text in the editor before attaching a selection.');
    }
    return {
        ...base,
        activeFile: vscode.workspace.workspaceFolders?.[0]
            ? vscode.workspace.asRelativePath(editor.document.uri, false)
            : editor.document.uri.fsPath,
        languageId: editor.document.languageId,
        selectedText: editor.document.getText(editor.selection),
        selectedRange: `${editor.selection.start.line + 1}-${editor.selection.end.line + 1}`,
        activeFileExcerpt: undefined,
    };
}

export async function attachFilesToContext(
    base: WorkspacePromptContext,
    paths: string[]
): Promise<WorkspacePromptContext> {
    const existing = [...(base.attachedFiles || [])];
    for (const relativePath of paths) {
        const excerpt = await readRelativeFileExcerpt(relativePath);
        const idx = existing.findIndex((f) => f.path === relativePath);
        const entry: AttachedFileContext = { path: relativePath, excerpt: excerpt.text, truncated: excerpt.truncated };
        if (idx >= 0) {
            existing[idx] = entry;
        } else {
            existing.push(entry);
        }
    }
    return { ...base, attachedFiles: existing };
}

export async function attachTerminalToContext(base: WorkspacePromptContext): Promise<WorkspacePromptContext> {
    const active = vscode.window.activeTerminal;
    if (!active) {
        throw new Error('No active terminal. Run a command first or focus a terminal.');
    }
    const name = active.name;
    return {
        ...base,
        terminalExcerpt: `Terminal "${name}" is active. Paste output into chat or use a task output file for full logs.`,
    };
}

export async function attachSearchToContext(
    base: WorkspacePromptContext,
    query: string
): Promise<WorkspacePromptContext> {
    const symbols = await vscode.commands.executeCommand<vscode.SymbolInformation[]>(
        'vscode.executeWorkspaceSymbolProvider',
        query
    );
    if (!symbols?.length) {
        throw new Error(`No workspace symbols matched "${query}".`);
    }
    const hits: SearchHitContext[] = symbols.slice(0, 15).map((sym) => ({
        path: vscode.workspace.asRelativePath(sym.location.uri, false),
        line: sym.location.range.start.line + 1,
        preview: `${sym.kind} ${sym.name}`,
    }));
    return { ...base, searchHits: hits };
}

export async function attachActiveFileToContext(
    base: WorkspacePromptContext
): Promise<WorkspacePromptContext> {
    const editor = vscode.window.activeTextEditor;
    if (!editor) {
        throw new Error('Open a file in the editor before attaching it.');
    }
    const relative = vscode.workspace.workspaceFolders?.[0]
        ? vscode.workspace.asRelativePath(editor.document.uri, false)
        : editor.document.uri.fsPath;
    return {
        ...base,
        activeFile: relative,
        languageId: editor.document.languageId,
        selectedText: undefined,
        selectedRange: undefined,
        activeFileExcerpt: editor.document.getText().slice(0, DEFAULT_CONTEXT_CHAR_LIMIT),
    };
}

async function readGitMetadata(
    workspaceUri: vscode.Uri,
    outputChannel?: vscode.OutputChannel
): Promise<{ branch?: string; repoUrl?: string }> {
    const result: { branch?: string; repoUrl?: string } = {};
    try {
        const head = await readWorkspaceText(vscode.Uri.joinPath(workspaceUri, '.git', 'HEAD'));
        result.branch = parseGitBranch(head) || undefined;
    } catch {
        outputChannel?.appendLine('[context] No readable .git/HEAD found for workspace.');
    }

    try {
        const config = await readWorkspaceText(vscode.Uri.joinPath(workspaceUri, '.git', 'config'));
        result.repoUrl = parseGitRemote(config) || undefined;
    } catch {
        outputChannel?.appendLine('[context] No readable .git/config found for workspace.');
    }
    return result;
}

async function readWorkspaceText(uri: vscode.Uri): Promise<string> {
    return Buffer.from(await vscode.workspace.fs.readFile(uri)).toString('utf8');
}

async function readRelativeFileExcerpt(relativePath: string): Promise<{ text: string; truncated: boolean }> {
    const root = vscode.workspace.workspaceFolders?.[0];
    if (!root) {
        throw new Error('No workspace folder is open.');
    }
    const parts = relativePath.split(/[\\/]+/).filter(Boolean);
    const uri = vscode.Uri.joinPath(root.uri, ...parts);
    const content = Buffer.from(await vscode.workspace.fs.readFile(uri)).toString('utf8');
    if (content.length <= DEFAULT_CONTEXT_CHAR_LIMIT) {
        return { text: content, truncated: false };
    }
    return { text: content.slice(0, DEFAULT_CONTEXT_CHAR_LIMIT), truncated: true };
}