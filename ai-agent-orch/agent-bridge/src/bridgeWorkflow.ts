export interface PatchFile {
    path?: string;
    filename?: string;
    action?: string;
    originalContentHash?: string;
    proposedContentHash?: string;
    newContent?: string;
    new_content?: string;
}

export interface PatchEnvelope {
    patchId?: string;
    patch_id?: string;
    bufferId?: string;
    buffer_id?: string;
    files?: PatchFile[];
}

export interface BridgeAuthSettings {
    devToken: string;
    identity: string;
}

export interface SessionEvent {
    type: string;
    payload?: string;
}

export function patchID(patch: PatchEnvelope): string {
    return patch.patchId || patch.patch_id || 'unknown';
}

export function parsePatchPayload(payload: string): PatchEnvelope {
    const patch = JSON.parse(payload) as PatchEnvelope;
    if (!patchID(patch) || patchID(patch) === 'unknown') {
        throw new Error('patch payload is missing patchId');
    }
    return patch;
}

export function workspacePathParts(filePath: string): string[] {
    if (!filePath || filePath === 'unknown') {
        throw new Error('Patch file path is required.');
    }
    if (filePath.startsWith('/') || /^[A-Za-z]:/.test(filePath)) {
        throw new Error(`Patch file path must be relative: ${filePath}`);
    }

    const parts = filePath.split(/[\\/]+/).filter(Boolean);
    if (parts.some((part) => part === '..' || part === '.')) {
        throw new Error(`Patch file path contains unsafe segments: ${filePath}`);
    }
    if (parts.length === 0) {
        throw new Error('Patch file path is required.');
    }

    return parts;
}

export function authHeadersForBridge(settings: BridgeAuthSettings, extra: Record<string, string> = {}): Record<string, string> {
    const token = settings.devToken.trim();
    if (!token) {
        throw new Error('AI Agent Bridge developer token is required.');
    }
    return {
        Authorization: `Bearer ${token}`,
        'X-AI-Orch-Local-Identity': settings.identity,
        ...extra,
    };
}

export function parseSessionEventLine(line: string): SessionEvent | undefined {
    if (!line.startsWith('data: ')) {
        return undefined;
    }

    try {
        return JSON.parse(line.substring(6)) as SessionEvent;
    } catch (err: any) {
        throw new Error(`Failed to parse SSE payload: ${err.message}`);
    }
}
