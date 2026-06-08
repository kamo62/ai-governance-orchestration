import { Buffer } from 'buffer';
import * as vscode from 'vscode';

import { GovernanceClient } from './bridgeGovernanceClient';
import { PatchEnvelope, patchID, workspacePathParts } from './bridgeWorkflow';

export class PatchContentProvider implements vscode.TextDocumentContentProvider {
    private readonly _contents = new Map<string, string>();
    private readonly _onDidChange = new vscode.EventEmitter<vscode.Uri>();
    public readonly onDidChange = this._onDidChange.event;

    setContent(uri: vscode.Uri, content: string): void {
        this._contents.set(uri.toString(), content);
        this._onDidChange.fire(uri);
    }

    provideTextDocumentContent(uri: vscode.Uri): string {
        return this._contents.get(uri.toString()) || '';
    }
}

export async function hydratePatches(
    client: GovernanceClient,
    sessionId: string,
    patches: PatchEnvelope[],
    outputChannel: vscode.OutputChannel
): Promise<PatchEnvelope[]> {
    const hydrated: PatchEnvelope[] = [];
    for (const patch of patches) {
        const id = patchID(patch);
        outputChannel.appendLine(`[patch] Fetching buffered patch ${id}`);
        hydrated.push(await client.fetchPatch(sessionId, id));
    }
    return hydrated;
}

export async function showPatchDiffs(
    patches: PatchEnvelope[],
    patchProvider: PatchContentProvider,
    outputChannel: vscode.OutputChannel
): Promise<void> {
    for (const patch of patches) {
        for (const file of patch.files || []) {
            const filePath = workspacePathParts(file.path || file.filename || 'unknown').join('/');
            const action = file.action || 'modify';
            const newContent = file.newContent ?? file.new_content ?? '';

            outputChannel.appendLine(`[diff] Opening diff for ${filePath} (${action})`);

            const patchUri = vscode.Uri.parse(
                `aiagentbridge-patch:${encodeURIComponent(filePath)}?patch=${encodeURIComponent(patchID(patch))}`
            );
            patchProvider.setContent(patchUri, newContent);

            const existingUri = await existingWorkspaceFileUri(filePath);

            let leftUri: vscode.Uri;
            let title: string;

            if (existingUri && action !== 'create') {
                leftUri = existingUri;
                title = `${filePath} (proposed)`;
            } else {
                const emptyUri = vscode.Uri.parse(`aiagentbridge-patch:${encodeURIComponent(filePath)}?empty=1`);
                patchProvider.setContent(emptyUri, '');
                leftUri = emptyUri;
                title = `${filePath} (new)`;
            }

            await vscode.commands.executeCommand('vscode.diff', leftUri, patchUri, title);
        }
    }
}

export async function applyPatches(
    patches: PatchEnvelope[],
    outputChannel: vscode.OutputChannel
): Promise<void> {
    const workspaceFolder = vscode.workspace.workspaceFolders?.[0];
    if (!workspaceFolder) {
        throw new Error('No workspace folder is open.');
    }

    for (const patch of patches) {
        for (const file of patch.files || []) {
            const filePath = file.path || file.filename || '';
            const action = file.action || 'modify';
            const uri = workspaceFileUri(workspaceFolder.uri, filePath);

            if (action === 'delete') {
                await vscode.workspace.fs.delete(uri, { useTrash: false });
                outputChannel.appendLine(`[apply] Deleted ${filePath}`);
                continue;
            }

            const content = file.newContent ?? file.new_content;
            if (typeof content !== 'string') {
                throw new Error(`Patch file ${filePath} is missing new content.`);
            }

            await vscode.workspace.fs.writeFile(uri, Buffer.from(content, 'utf8'));
            outputChannel.appendLine(`[apply] Wrote ${filePath}`);
        }
    }
}

export async function applySelectedPatchFiles(
    patches: PatchEnvelope[],
    selectedPaths: Set<string>,
    outputChannel: vscode.OutputChannel
): Promise<PatchEnvelope[]> {
    const filtered: PatchEnvelope[] = [];
    for (const patch of patches) {
        const files = (patch.files || []).filter((file) => {
            const path = file.path || file.filename || '';
            return selectedPaths.has(path);
        });
        if (files.length === 0) {
            continue;
        }
        filtered.push({ ...patch, files });
    }
    if (filtered.length === 0) {
        throw new Error('No files selected for apply.');
    }
    await applyPatches(filtered, outputChannel);
    return filtered;
}

export async function submitPatchDecisions(
    client: GovernanceClient,
    sessionId: string,
    patches: PatchEnvelope[],
    decision: string,
    reason?: string
): Promise<void> {
    for (const patch of patches) {
        const id = patchID(patch);
        if (!id || id === 'unknown') {
            throw new Error('Patch decision requires a patch ID.');
        }
        await client.submitPatchDecision(sessionId, id, decision, reason);
    }
}

async function existingWorkspaceFileUri(filePath: string): Promise<vscode.Uri | undefined> {
    const workspaceFolder = vscode.workspace.workspaceFolders?.[0];
    if (!workspaceFolder) {
        return undefined;
    }

    const uri = workspaceFileUri(workspaceFolder.uri, filePath);
    try {
        await vscode.workspace.fs.stat(uri);
        return uri;
    } catch {
        return undefined;
    }
}

function workspaceFileUri(root: vscode.Uri, filePath: string): vscode.Uri {
    return vscode.Uri.joinPath(root, ...workspacePathParts(filePath));
}