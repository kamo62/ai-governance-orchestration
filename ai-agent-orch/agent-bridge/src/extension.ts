import { Buffer } from 'buffer';
import * as http from 'http';
import * as https from 'https';
import * as vscode from 'vscode';

const GOVERNANCE_URL = vscode.workspace.getConfiguration('aiAgentBridge').get<string>('governanceUrl') || 'http://127.0.0.1:8080';
const DEV_TOKEN = process.env.AI_ORCH_DEV_TOKEN || '';

interface AgentSession {
    session_id: string;
    status: string;
}

interface RouteResult {
    specialist: string;
    reason: string;
}

interface PatchFile {
    path?: string;
    filename?: string;
    action?: string;
    originalContentHash?: string;
    proposedContentHash?: string;
    newContent?: string;
    new_content?: string;
}

interface PatchEnvelope {
    patchId?: string;
    patch_id?: string;
    bufferId?: string;
    buffer_id?: string;
    files?: PatchFile[];
}

class PatchContentProvider implements vscode.TextDocumentContentProvider {
    private _contents = new Map<string, string>();
    private _onDidChange = new vscode.EventEmitter<vscode.Uri>();
    public readonly onDidChange = this._onDidChange.event;

    setContent(uri: vscode.Uri, content: string) {
        this._contents.set(uri.toString(), content);
        this._onDidChange.fire(uri);
    }

    provideTextDocumentContent(uri: vscode.Uri): string {
        return this._contents.get(uri.toString()) || '';
    }
}

const patchProvider = new PatchContentProvider();

export function activate(context: vscode.ExtensionContext) {
    const outputChannel = vscode.window.createOutputChannel('AI Agent Bridge');

    context.subscriptions.push(
        vscode.workspace.registerTextDocumentContentProvider('aiagentbridge-patch', patchProvider)
    );

    const invokeCommand = vscode.commands.registerCommand('aiAgentBridge.invokeAgent', async () => {
        const prompt = await vscode.window.showInputBox({
            prompt: 'What would you like the agent to do?',
            placeHolder: 'e.g., write Playwright tests for this component'
        });

        if (!prompt) {
            return;
        }

        outputChannel.appendLine(`Invoking agent with prompt length: ${prompt.length} chars`);

        try {
            const session = await createSession(prompt);
            outputChannel.appendLine(`Session created: ${session.session_id}`);

            const routeResult = await sendMessage(session.session_id, prompt);
            outputChannel.appendLine(`Recommended specialist: ${routeResult.specialist} (${routeResult.reason})`);

            const confirm = await vscode.window.showInformationMessage(
                `Recommended agent: ${routeResult.specialist}. Reason: ${routeResult.reason}`,
                'Confirm',
                'Cancel'
            );

            if (confirm !== 'Confirm') {
                outputChannel.appendLine('User cancelled specialist selection.');
                return;
            }

            outputChannel.appendLine('Connecting to event stream...');
            const collectedPatches: PatchEnvelope[] = [];
            const eventStream = connectToEvents(session.session_id, outputChannel, collectedPatches);

            const confirmed = await confirmSession(session.session_id, routeResult.specialist);
            outputChannel.appendLine(`Session confirmed: ${confirmed.status}`);

            await eventStream;

            if (collectedPatches.length > 0) {
                await reviewPatchFlow(session.session_id, collectedPatches, outputChannel);
                return;
            }

            const patchDecision = await vscode.window.showInformationMessage(
                'Execution complete. No patches proposed.',
                'Show Audit',
                'Dismiss'
            );

            if (patchDecision === 'Show Audit') {
                vscode.commands.executeCommand('aiAgentBridge.showAudit', session.session_id);
            }
        } catch (err: any) {
            vscode.window.showErrorMessage(`Agent invocation failed: ${err.message}`);
            outputChannel.appendLine(`Error: ${err.message}`);
        }
    });

    const showAuditCommand = vscode.commands.registerCommand('aiAgentBridge.showAudit', async (sessionId?: string) => {
        const id = sessionId || await vscode.window.showInputBox({
            prompt: 'Enter session ID to view audit',
            placeHolder: 'sess_...'
        });

        if (!id) {
            return;
        }

        try {
            const audit = await lookupAudit(id);
            const panel = vscode.window.createWebviewPanel(
                'aiAgentAudit',
                `Audit: ${id}`,
                vscode.ViewColumn.One,
                {}
            );
            panel.webview.html = `<pre>${escapeHtml(JSON.stringify(audit, null, 2))}</pre>`;
        } catch (err: any) {
            vscode.window.showErrorMessage(`Audit lookup failed: ${err.message}`);
        }
    });

    context.subscriptions.push(invokeCommand, showAuditCommand, outputChannel);
}

async function createSession(prompt: string): Promise<AgentSession> {
    const response = await fetch(`${GOVERNANCE_URL}/v1/sessions`, {
        method: 'POST',
        headers: authHeaders({
            'Content-Type': 'application/json'
        }),
        body: JSON.stringify({
            agent: 'test-generation',
            classification: 'internal',
            prompt
        })
    });

    if (!response.ok) {
        const text = await response.text();
        throw new Error(`Create session failed: ${response.status} ${text}`);
    }

    return response.json() as Promise<AgentSession>;
}

async function sendMessage(sessionId: string, prompt: string): Promise<RouteResult> {
    const response = await fetch(`${GOVERNANCE_URL}/v1/sessions/${sessionId}/messages`, {
        method: 'POST',
        headers: authHeaders({
            'Content-Type': 'application/json'
        }),
        body: JSON.stringify({ prompt })
    });

    if (!response.ok) {
        const text = await response.text();
        throw new Error(`Send message failed: ${response.status} ${text}`);
    }

    return response.json() as Promise<RouteResult>;
}

async function confirmSession(sessionId: string, agent: string): Promise<AgentSession> {
    const response = await fetch(`${GOVERNANCE_URL}/v1/sessions/${sessionId}/confirm`, {
        method: 'POST',
        headers: authHeaders({
            'Content-Type': 'application/json'
        }),
        body: JSON.stringify({ agent })
    });

    if (!response.ok) {
        const text = await response.text();
        throw new Error(`Confirm session failed: ${response.status} ${text}`);
    }

    return response.json() as Promise<AgentSession>;
}

async function connectToEvents(sessionId: string, outputChannel: vscode.OutputChannel, patches: PatchEnvelope[]): Promise<void> {
    return new Promise((resolve, reject) => {
        const url = `${GOVERNANCE_URL}/v1/sessions/${sessionId}/events`;
        const requestGet = url.startsWith('https') ? https.get : http.get;
        let settled = false;
        const requestState: { req?: http.ClientRequest } = {};

        const finish = () => {
            if (settled) {
                return;
            }
            settled = true;
            clearTimeout(timeout);
            resolve();
        };

        const fail = (err: Error) => {
            if (settled) {
                return;
            }
            settled = true;
            clearTimeout(timeout);
            reject(err);
        };

        const timeout = setTimeout(() => {
            requestState.req?.destroy();
            fail(new Error('SSE stream timed out'));
        }, 60000);

        const req = requestGet(url, {
            headers: authHeaders({
                'Accept': 'text/event-stream'
            })
        }, (res: http.IncomingMessage) => {
            if (res.statusCode !== 200) {
                fail(new Error(`SSE failed: HTTP ${res.statusCode}`));
                return;
            }

            let buffer = '';
            res.on('data', (chunk: Buffer) => {
                buffer += chunk.toString();
                const lines = buffer.split('\n');
                buffer = lines.pop() || '';

                for (const line of lines) {
                    if (!line.startsWith('data: ')) {
                        continue;
                    }

                    try {
                        const data = JSON.parse(line.substring(6));
                        outputChannel.appendLine(`[${data.type}] ${data.payload}`);

                        if (data.type === 'patch') {
                            const patch = parsePatchPayload(data.payload);
                            patches.push(patch);
                            outputChannel.appendLine(`[patch] Collected patch ${patchID(patch)} with ${patch.files?.length || 0} file(s)`);
                        }

                        if (data.type === 'error') {
                            fail(new Error(data.payload || 'runtime emitted an error'));
                            return;
                        }

                        if (data.type === 'done') {
                            finish();
                            return;
                        }
                    } catch (err: any) {
                        fail(new Error(`Failed to parse SSE payload: ${err.message}`));
                        return;
                    }
                }
            });

            res.on('end', finish);
            res.on('error', fail);
        });

        requestState.req = req;
        req.on('error', fail);
    });
}

async function reviewPatchFlow(sessionId: string, patches: PatchEnvelope[], outputChannel: vscode.OutputChannel): Promise<void> {
    const fullPatches = await hydratePatches(sessionId, patches, outputChannel);
    const totalFiles = fullPatches.reduce((sum, patch) => sum + (patch.files?.length || 0), 0);
    const review = await vscode.window.showInformationMessage(
        `Execution complete. ${totalFiles} file(s) proposed in ${fullPatches.length} patch(es).`,
        'Review Diff',
        'Show Audit',
        'Dismiss'
    );

    if (review === 'Show Audit') {
        vscode.commands.executeCommand('aiAgentBridge.showAudit', sessionId);
        return;
    }

    if (review !== 'Review Diff') {
        return;
    }

    await showPatchDiffs(fullPatches, outputChannel);

    const decision = await vscode.window.showWarningMessage(
        'Record a patch decision for this session.',
        { modal: true },
        'Apply',
        'Mark Partially Applied',
        'Reject'
    );

    if (!decision) {
        return;
    }

    if (decision === 'Apply') {
        await applyPatches(fullPatches, outputChannel);
        await submitPatchDecisions(sessionId, fullPatches, 'applied');
        vscode.window.showInformationMessage('Patch applied and audit decision recorded.');
        return;
    }

    if (decision === 'Mark Partially Applied') {
        await submitPatchDecisions(sessionId, fullPatches, 'partially_applied', 'User marked the patch as partially applied in VS Code.');
        vscode.window.showInformationMessage('Partial patch decision recorded.');
        return;
    }

    await submitPatchDecisions(sessionId, fullPatches, 'rejected');
    vscode.window.showInformationMessage('Patch rejected and audit decision recorded.');
}

async function hydratePatches(sessionId: string, patches: PatchEnvelope[], outputChannel: vscode.OutputChannel): Promise<PatchEnvelope[]> {
    const hydrated: PatchEnvelope[] = [];
    for (const patch of patches) {
        const id = patchID(patch);
        outputChannel.appendLine(`[patch] Fetching buffered patch ${id}`);
        hydrated.push(await fetchPatch(sessionId, id));
    }
    return hydrated;
}

async function fetchPatch(sessionId: string, id: string): Promise<PatchEnvelope> {
    if (!id || id === 'unknown') {
        throw new Error('Patch fetch requires a patch ID.');
    }
    const response = await fetch(`${GOVERNANCE_URL}/v1/sessions/${encodeURIComponent(sessionId)}/patches/${encodeURIComponent(id)}`, {
        method: 'GET',
        headers: authHeaders()
    });

    if (!response.ok) {
        const text = await response.text();
        throw new Error(`Patch fetch failed: ${response.status} ${text}`);
    }

    return response.json() as Promise<PatchEnvelope>;
}

async function showPatchDiffs(patches: PatchEnvelope[], outputChannel: vscode.OutputChannel): Promise<void> {
    for (const patch of patches) {
        const files = patch.files || [];
        for (const file of files) {
            const filePath = file.path || file.filename || 'unknown';
            const action = file.action || 'modify';
            const newContent = file.newContent ?? file.new_content ?? '';

            outputChannel.appendLine(`[diff] Opening diff for ${filePath} (${action})`);

            const patchUri = vscode.Uri.parse(`aiagentbridge-patch:${encodeURIComponent(filePath)}?patch=${encodeURIComponent(patchID(patch))}`);
            patchProvider.setContent(patchUri, newContent);

            const workspaceFiles = await vscode.workspace.findFiles(filePath, null, 1);
            const existingUri = workspaceFiles.length > 0 ? workspaceFiles[0] : undefined;

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

async function applyPatches(patches: PatchEnvelope[], outputChannel: vscode.OutputChannel): Promise<void> {
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

async function submitPatchDecisions(sessionId: string, patches: PatchEnvelope[], decision: string, reason?: string): Promise<void> {
    for (const patch of patches) {
        const id = patchID(patch);
        if (!id || id === 'unknown') {
            throw new Error('Patch decision requires a patch ID.');
        }
        await submitPatchDecision(sessionId, id, decision, reason);
    }
}

async function submitPatchDecision(sessionId: string, id: string, decision: string, reason?: string): Promise<void> {
    const response = await fetch(`${GOVERNANCE_URL}/v1/sessions/${sessionId}/patch-decision`, {
        method: 'POST',
        headers: authHeaders({
            'Content-Type': 'application/json'
        }),
        body: JSON.stringify({
            patch_id: id,
            decision,
            reason
        })
    });

    if (!response.ok) {
        const text = await response.text();
        throw new Error(`Patch decision failed: ${response.status} ${text}`);
    }
}

async function lookupAudit(sessionId: string): Promise<any> {
    const response = await fetch(`${GOVERNANCE_URL}/v1/audit/sessions/${sessionId}`, {
        method: 'GET',
        headers: authHeaders()
    });

    if (!response.ok) {
        const text = await response.text();
        throw new Error(`Audit lookup failed: ${response.status} ${text}`);
    }

    return response.json();
}

function parsePatchPayload(payload: string): PatchEnvelope {
    const patch = JSON.parse(payload) as PatchEnvelope;
    if (!patchID(patch) || patchID(patch) === 'unknown') {
        throw new Error('patch payload is missing patchId');
    }
    return patch;
}

function patchID(patch: PatchEnvelope): string {
    return patch.patchId || patch.patch_id || 'unknown';
}

function workspaceFileUri(root: vscode.Uri, filePath: string): vscode.Uri {
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

    return vscode.Uri.joinPath(root, ...parts);
}

function authHeaders(extra: Record<string, string> = {}): Record<string, string> {
    const token = DEV_TOKEN.trim();
    if (!token) {
        throw new Error('AI_ORCH_DEV_TOKEN is required for the VS Code Bridge.');
    }
    return {
        'Authorization': `Bearer ${token}`,
        ...extra
    };
}

function escapeHtml(value: string): string {
    return value
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

export function deactivate() {}
