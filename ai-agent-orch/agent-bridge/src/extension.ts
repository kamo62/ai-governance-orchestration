import { Buffer } from 'buffer';
import * as http from 'http';
import * as https from 'https';
import * as vscode from 'vscode';

import {
    DEFAULT_GOVERNANCE_URL,
    DEFAULT_IDENTITY,
    DEV_TOKEN_SECRET_KEY,
    BridgeSettings,
    bridgeConnectionStatus,
    hasUsableDevToken,
    normalizeGovernanceUrl,
    resolveBridgeSettings,
    validateGovernanceReadyResponse,
} from './bridgeConfig';
import {
    PatchEnvelope,
    authHeadersForBridge,
    parsePatchPayload,
    parseSessionEventLine,
    patchID,
    workspacePathParts,
} from './bridgeWorkflow';
import {
    DEFAULT_CONTEXT_CHAR_LIMIT,
    WorkspacePromptContext,
    buildContextualPrompt,
    contextSummary,
    parseGitBranch,
    parseGitRemote,
} from './bridgeWorkspace';

let governanceUrl = DEFAULT_GOVERNANCE_URL;
let devToken = process.env.AI_ORCH_DEV_TOKEN || '';
let identity = DEFAULT_IDENTITY;

interface AgentSession {
    session_id: string;
    status: string;
}

interface RouteResult {
    specialist: string;
    reason: string;
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

export async function activate(context: vscode.ExtensionContext) {
    const outputChannel = vscode.window.createOutputChannel('AI Agent Bridge');
    await refreshBridgeSettings(context);

    context.subscriptions.push(
        vscode.workspace.registerTextDocumentContentProvider('aiagentbridge-patch', patchProvider)
    );

    const onboardCommand = vscode.commands.registerCommand('aiAgentBridge.onboard', async () => {
        await runOnboarding(context, outputChannel);
    });

    const checkConnectionCommand = vscode.commands.registerCommand('aiAgentBridge.checkConnection', async () => {
        await runConnectionCheck(context, outputChannel, true);
    });

    const invokeCommand = vscode.commands.registerCommand('aiAgentBridge.invokeAgent', async () => {
        if (!await ensureBridgeReady(context, outputChannel)) {
            return;
        }

        const prompt = await vscode.window.showInputBox({
            prompt: 'What would you like the agent to do?',
            placeHolder: 'e.g., write Playwright tests for this component'
        });

        if (!prompt) {
            return;
        }

        const workspaceContext = await collectWorkspacePromptContext(outputChannel);
        const governedPrompt = buildContextualPrompt(prompt, workspaceContext);
        outputChannel.appendLine(`Invoking agent with prompt length: ${prompt.length} chars`);
        outputChannel.appendLine(`[context] ${contextSummary(workspaceContext)}`);

        try {
            const session = await createSession(governedPrompt, workspaceContext, prompt);
            outputChannel.appendLine(`Session created: ${session.session_id}`);

            const routeResult = await sendMessage(session.session_id, governedPrompt);
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
        if (!await ensureBridgeReady(context, outputChannel)) {
            return;
        }

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

    const settings = await refreshBridgeSettings(context);
    if (!hasUsableDevToken(settings)) {
        const action = await vscode.window.showInformationMessage(
            'AI Agent Bridge needs first-run setup before it can call the Governance Shell.',
            'Run Setup',
            'Later'
        );
        if (action === 'Run Setup') {
            await runOnboarding(context, outputChannel);
        }
    }

    context.subscriptions.push(onboardCommand, checkConnectionCommand, invokeCommand, showAuditCommand, outputChannel);
}

async function refreshBridgeSettings(context: vscode.ExtensionContext): Promise<BridgeSettings> {
    const config = vscode.workspace.getConfiguration('aiAgentBridge');
    const settings = resolveBridgeSettings({
        configuredGovernanceUrl: config.get<string>('governanceUrl'),
        configuredDevToken: config.get<string>('devToken'),
        configuredIdentity: config.get<string>('identity'),
        envDevToken: process.env.AI_ORCH_DEV_TOKEN,
        secretDevToken: await context.secrets.get(DEV_TOKEN_SECRET_KEY),
    });

    governanceUrl = settings.governanceUrl;
    devToken = settings.devToken;
    identity = settings.identity;
    return settings;
}

async function runOnboarding(context: vscode.ExtensionContext, outputChannel: vscode.OutputChannel): Promise<boolean> {
    const current = await refreshBridgeSettings(context);
    const config = vscode.workspace.getConfiguration('aiAgentBridge');

    const urlInput = await vscode.window.showInputBox({
        title: 'AI Agent Bridge Setup',
        prompt: 'Governance Shell URL',
        value: current.governanceUrl,
        placeHolder: DEFAULT_GOVERNANCE_URL,
        validateInput: (value) => {
            try {
                normalizeGovernanceUrl(value);
                return undefined;
            } catch (err: any) {
                return err.message;
            }
        },
    });
    if (urlInput === undefined) {
        return false;
    }

    const nextUrl = normalizeGovernanceUrl(urlInput);
    await config.update('governanceUrl', nextUrl, vscode.ConfigurationTarget.Global);

    const identityInput = await vscode.window.showInputBox({
        title: 'AI Agent Bridge Setup',
        prompt: 'Identity label for audit events',
        value: current.identity || DEFAULT_IDENTITY,
        placeHolder: DEFAULT_IDENTITY,
    });
    if (identityInput === undefined) {
        return false;
    }
    await config.update('identity', identityInput.trim() || DEFAULT_IDENTITY, vscode.ConfigurationTarget.Global);

    const existingToken = hasUsableDevToken(current);
    const tokenInput = await vscode.window.showInputBox({
        title: 'AI Agent Bridge Setup',
        prompt: existingToken
            ? 'Developer token. Leave blank to keep the stored token.'
            : 'Developer token. For the local Compose default, use local-dev.',
        placeHolder: 'local-dev',
        password: true,
        validateInput: (value) => {
            if (existingToken || value.trim()) {
                return undefined;
            }
            return 'A developer token is required. Use local-dev for the default local Compose stack.';
        },
    });
    if (tokenInput === undefined) {
        return false;
    }
    if (tokenInput.trim()) {
        await context.secrets.store(DEV_TOKEN_SECRET_KEY, tokenInput.trim());
    }

    outputChannel.appendLine(`Bridge setup saved. Governance URL: ${nextUrl}`);
    const ready = await runConnectionCheck(context, outputChannel, false);
    if (!ready) {
        const action = await vscode.window.showWarningMessage(
            'Setup saved, but the Governance Shell is not reachable yet.',
            'Show Output'
        );
        if (action === 'Show Output') {
            outputChannel.show();
        }
        return false;
    }

    const action = await vscode.window.showInformationMessage(
        'AI Agent Bridge is ready.',
        'Invoke Agent',
        'Show Output'
    );
    if (action === 'Invoke Agent') {
        vscode.commands.executeCommand('aiAgentBridge.invokeAgent');
    } else if (action === 'Show Output') {
        outputChannel.show();
    }
    return true;
}

async function ensureBridgeReady(context: vscode.ExtensionContext, outputChannel: vscode.OutputChannel): Promise<boolean> {
    const settings = await refreshBridgeSettings(context);
    if (!hasUsableDevToken(settings)) {
        const action = await vscode.window.showWarningMessage(
            'AI Agent Bridge needs setup before it can call the Governance Shell.',
            'Run Setup',
            'Cancel'
        );
        if (action === 'Run Setup') {
            return runOnboarding(context, outputChannel);
        }
        return false;
    }

    const ready = await checkGovernanceReady(settings.governanceUrl);
    if (ready.ok) {
        return true;
    }

    outputChannel.appendLine(`[setup] Governance Shell is not reachable at ${settings.governanceUrl}: ${ready.message}`);
    outputChannel.appendLine('[setup] Start it from ai-agent-orch with: docker compose --env-file ../.env.dev up -d governance-shell orchestrator');
    const action = await vscode.window.showWarningMessage(
        `Governance Shell is not reachable at ${settings.governanceUrl}.`,
        'Run Setup',
        'Show Output',
        'Cancel'
    );
    if (action === 'Run Setup') {
        return runOnboarding(context, outputChannel);
    }
    if (action === 'Show Output') {
        outputChannel.show();
    }
    return false;
}

async function runConnectionCheck(context: vscode.ExtensionContext, outputChannel: vscode.OutputChannel, showResult: boolean): Promise<boolean> {
    const settings = await refreshBridgeSettings(context);
    const ready = await checkGovernanceReady(settings.governanceUrl);
    if (!ready.ok) {
        outputChannel.appendLine(`[setup] Governance Shell check failed at ${settings.governanceUrl}: ${ready.message}`);
        outputChannel.appendLine('[setup] Expected local startup command: docker compose --env-file ../.env.dev up -d governance-shell orchestrator');
        if (showResult) {
            const action = await vscode.window.showWarningMessage(
                `Governance Shell check failed: ${ready.message}`,
                'Show Output'
            );
            if (action === 'Show Output') {
                outputChannel.show();
            }
        }
        return false;
    }

    const status = bridgeConnectionStatus({ settings, ready: ready.ok, readyMessage: ready.message });
    outputChannel.appendLine(`[setup] Governance Shell ready at ${settings.governanceUrl}; ${status.tokenMessage}.`);
    if (showResult) {
        if (status.needsSetup) {
            const action = await vscode.window.showWarningMessage(
                status.message,
                'Run Setup',
                'Show Output',
                'Cancel'
            );
            if (action === 'Run Setup') {
                return runOnboarding(context, outputChannel);
            }
            if (action === 'Show Output') {
                outputChannel.show();
            }
        } else {
            vscode.window.showInformationMessage(status.message);
        }
    }
    return status.ok;
}

async function checkGovernanceReady(baseUrl: string): Promise<{ ok: boolean; message: string }> {
    try {
        const response = await fetch(`${baseUrl}/readyz`, { method: 'GET' });
        const body = await response.text();
        return validateGovernanceReadyResponse(response.status, body);
    } catch (err: any) {
        return { ok: false, message: err.message || String(err) };
    }
}

async function createSession(prompt: string, workspaceContext: WorkspacePromptContext, userIntent: string): Promise<AgentSession> {
    const response = await fetch(`${governanceUrl}/v1/sessions`, {
        method: 'POST',
        headers: authHeaders({
            'Content-Type': 'application/json'
        }),
        body: JSON.stringify({
            agent: 'test-generation',
            classification: 'internal',
            prompt,
            repo_url: workspaceContext.repoUrl || undefined,
            branch: workspaceContext.branch || undefined,
            intent: userIntent
        })
    });

    if (!response.ok) {
        const text = await response.text();
        throw new Error(`Create session failed: ${response.status} ${text}`);
    }

    return response.json() as Promise<AgentSession>;
}

async function collectWorkspacePromptContext(outputChannel: vscode.OutputChannel): Promise<WorkspacePromptContext> {
    const workspaceFolder = vscode.workspace.workspaceFolders?.[0];
    const editor = vscode.window.activeTextEditor;
    const context: WorkspacePromptContext = {};

    if (workspaceFolder) {
        context.workspaceName = workspaceFolder.name;
        const gitMetadata = await readGitMetadata(workspaceFolder.uri, outputChannel);
        context.branch = gitMetadata.branch;
        context.repoUrl = gitMetadata.repoUrl;
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

async function readGitMetadata(workspaceUri: vscode.Uri, outputChannel: vscode.OutputChannel): Promise<{ branch?: string; repoUrl?: string }> {
    const result: { branch?: string; repoUrl?: string } = {};
    try {
        const head = await readWorkspaceText(vscode.Uri.joinPath(workspaceUri, '.git', 'HEAD'));
        result.branch = parseGitBranch(head) || undefined;
    } catch {
        outputChannel.appendLine('[context] No readable .git/HEAD found for workspace.');
    }

    try {
        const config = await readWorkspaceText(vscode.Uri.joinPath(workspaceUri, '.git', 'config'));
        result.repoUrl = parseGitRemote(config) || undefined;
    } catch {
        outputChannel.appendLine('[context] No readable .git/config found for workspace.');
    }
    return result;
}

async function readWorkspaceText(uri: vscode.Uri): Promise<string> {
    return Buffer.from(await vscode.workspace.fs.readFile(uri)).toString('utf8');
}

async function sendMessage(sessionId: string, prompt: string): Promise<RouteResult> {
    const response = await fetch(`${governanceUrl}/v1/sessions/${sessionId}/messages`, {
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
    const response = await fetch(`${governanceUrl}/v1/sessions/${sessionId}/confirm`, {
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
        const url = `${governanceUrl}/v1/sessions/${sessionId}/events`;
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
                    try {
                        const data = parseSessionEventLine(line);
                        if (!data) {
                            continue;
                        }
                        outputChannel.appendLine(`[${data.type}] ${data.payload}`);

                        if (data.type === 'patch') {
                            const patch = parsePatchPayload(data.payload || '');
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
    const response = await fetch(`${governanceUrl}/v1/sessions/${encodeURIComponent(sessionId)}/patches/${encodeURIComponent(id)}`, {
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
            const filePath = workspacePathParts(file.path || file.filename || 'unknown').join('/');
            const action = file.action || 'modify';
            const newContent = file.newContent ?? file.new_content ?? '';

            outputChannel.appendLine(`[diff] Opening diff for ${filePath} (${action})`);

            const patchUri = vscode.Uri.parse(`aiagentbridge-patch:${encodeURIComponent(filePath)}?patch=${encodeURIComponent(patchID(patch))}`);
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
    const response = await fetch(`${governanceUrl}/v1/sessions/${sessionId}/patch-decision`, {
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
    const response = await fetch(`${governanceUrl}/v1/audit/sessions/${sessionId}`, {
        method: 'GET',
        headers: authHeaders()
    });

    if (!response.ok) {
        const text = await response.text();
        throw new Error(`Audit lookup failed: ${response.status} ${text}`);
    }

    return response.json();
}

function workspaceFileUri(root: vscode.Uri, filePath: string): vscode.Uri {
    return vscode.Uri.joinPath(root, ...workspacePathParts(filePath));
}

function authHeaders(extra: Record<string, string> = {}): Record<string, string> {
    return authHeadersForBridge({ devToken, identity }, extra);
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
