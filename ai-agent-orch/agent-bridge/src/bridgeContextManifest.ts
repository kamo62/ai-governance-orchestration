import { createHash } from 'crypto';

import { GovernanceClient } from './bridgeGovernanceClient';
import { WorkspacePromptContext } from './bridgeWorkspace';

export function estimateContextChars(context: WorkspacePromptContext, userPrompt = ''): number {
    let total = userPrompt.length;
    if (context.selectedText) {
        total += context.selectedText.length;
    }
    if (context.activeFileExcerpt) {
        total += context.activeFileExcerpt.length;
    }
    if (context.terminalExcerpt) {
        total += context.terminalExcerpt.length;
    }
    for (const file of context.attachedFiles || []) {
        total += (file.excerpt || '').length;
    }
    for (const hit of context.searchHits || []) {
        total += hit.preview.length;
    }
    return total;
}

export function contextManifestId(sessionId: string, sourceSystem: string, sourceObjectId: string): string {
    const digest = createHash('sha256')
        .update(`${sessionId}:${sourceSystem}:${sourceObjectId}`)
        .digest('hex')
        .slice(0, 16);
    return `cm_${digest}`;
}

export async function registerContextManifest(
    client: GovernanceClient,
    sessionId: string,
    context: WorkspacePromptContext,
    summary: string
): Promise<string | undefined> {
    const sourceObjectId = context.activeFile || context.workspaceName || 'workspace';
    const id = contextManifestId(sessionId, 'vscode-bridge', sourceObjectId);
    try {
        await client.createContextManifest({
            id,
            session_id: sessionId,
            summary: summary.slice(0, 2000),
            source_system: 'vscode-bridge',
            source_object_id: sourceObjectId,
            source_path: context.activeFile,
            classification: 'internal',
            cache_status: 'fresh',
            chunk_hashes: hashChunks(context),
        });
        return id;
    } catch {
        return undefined;
    }
}

function hashChunks(context: WorkspacePromptContext): string[] {
    const chunks: string[] = [];
    if (context.selectedText) {
        chunks.push(sha256(context.selectedText));
    }
    for (const file of context.attachedFiles || []) {
        if (file.excerpt) {
            chunks.push(sha256(file.excerpt));
        }
    }
    return chunks;
}

function sha256(value: string): string {
    return createHash('sha256').update(value).digest('hex');
}