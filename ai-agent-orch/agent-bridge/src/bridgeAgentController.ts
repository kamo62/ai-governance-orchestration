import * as vscode from 'vscode';

import { timelineFromAuditEvents } from './bridgeAuditTimeline';
import { AgentPanelState, initialPanelState } from './bridgeAgentTypes';
import { appendChatMessage, appendTimeline, reduceSessionEvent } from './bridgeAgentReducer';
import {
    attachActiveFileToContext,
    attachFilesToContext,
    attachSearchToContext,
    attachSelectionToContext,
    attachTerminalToContext,
    collectWorkspacePromptContext,
    contextAttachments,
    workspaceLabelFromContext,
} from './bridgeAgentSession';
import { estimateContextChars, registerContextManifest } from './bridgeContextManifest';
import { evaluateToolApproval, normalizeApprovalChoice } from './bridgeToolApproval';
import { BridgeSettings, hasUsableDevToken } from './bridgeConfig';
import { GovernanceClient, SessionUsageSummary } from './bridgeGovernanceClient';
import { BridgeMcpClient, flattenMcpTools } from './bridgeMcpClient';
import {
    applyPatches,
    applySelectedPatchFiles,
    hydratePatches,
    showPatchDiffs,
    submitPatchDecisions,
    PatchContentProvider,
} from './bridgePatchService';
import {
    loadPanelSnapshot,
    loadRecentSessions,
    rememberRecentSession,
    savePanelSnapshot,
} from './bridgeSessionStore';
import {
    WorkspacePromptContext,
    buildContextualPrompt,
    contextSummary,
    preserveExplicitContextAttachments,
} from './bridgeWorkspace';

export type PanelStateListener = (state: AgentPanelState) => void;

export class BridgeAgentController {
    private state: AgentPanelState;
    private workspaceContext: WorkspacePromptContext = {};
    private streamCancel?: () => void;
    private lastCompletedSessionId?: string;

    constructor(
        private readonly getSettings: () => Promise<BridgeSettings>,
        private readonly getAutoConfirm: () => boolean,
        private readonly patchProvider: PatchContentProvider,
        private readonly outputChannel: vscode.OutputChannel,
        private readonly storage: vscode.Memento | undefined,
        private readonly onStateChange: PanelStateListener
    ) {
        this.state = initialPanelState('');
    }

    getState(): AgentPanelState {
        return this.state;
    }

    setActiveTab(tab: 'chat' | 'timeline'): void {
        this.state = { ...this.state, activeTab: tab };
        this.emit();
    }

    setAutoConfirm(enabled: boolean): void {
        this.state = { ...this.state, autoConfirm: enabled };
        this.emit();
    }

    async refreshConnection(): Promise<void> {
        const settings = await this.getSettings();
        this.workspaceContext = await collectWorkspacePromptContext(this.outputChannel);
        const workspaceName = this.workspaceContext.workspaceName || 'default';
        const client = new GovernanceClient(settings);
        const ready = await client.checkReady();
        const connected = ready.ok && hasUsableDevToken(settings);

        let agents = this.state.agents;
        let systemStatus = this.state.systemStatus;
        if (connected) {
            try {
                agents = await client.listAgents();
            } catch {
                agents = this.state.agents;
            }
            try {
                const status = await client.fetchSystemStatus();
                systemStatus = {
                    version: status.version,
                    modelBackend: status.model_backend,
                    modelGatewayAddr: status.model_gateway_addr,
                };
            } catch {
                systemStatus = undefined;
            }
            await this.loadRegistryOptions(client);
            if (this.state.sessionId) {
                await this.refreshSessionUsage(client, this.state.sessionId);
                await this.refreshMcpTools(client, this.state.sessionId);
            }
        }

        let recentSessions = this.state.recentSessions;
        if (this.storage) {
            recentSessions = await loadRecentSessions(this.storage, workspaceName);
            const snapshot = await loadPanelSnapshot(this.storage, workspaceName);
            if (snapshot && this.state.messages.length === 0) {
                this.state = {
                    ...this.state,
                    messages: snapshot.messages,
                    agentHint: snapshot.agentHint || this.state.agentHint,
                    sessionId: snapshot.lastSessionId,
                    runId: snapshot.lastRunId,
                };
                if (snapshot.lastSessionId) {
                    this.lastCompletedSessionId = snapshot.lastSessionId;
                }
            }
        }

        this.state = {
            ...this.state,
            governanceUrl: settings.governanceUrl,
            connected,
            workspaceLabel: workspaceLabelFromContext(this.workspaceContext),
            branch: this.workspaceContext.branch,
            attachments: contextAttachments(this.workspaceContext),
            agents,
            systemStatus,
            recentSessions,
            autoConfirm: this.getAutoConfirm(),
            contextCharEstimate: estimateContextChars(this.workspaceContext),
            status: connected && ['error'].includes(this.state.status) ? 'idle' : this.state.status,
            statusDetail: connected
                ? (this.state.status === 'idle' ? 'Ready' : this.state.statusDetail)
                : hasUsableDevToken(settings)
                    ? `Governance Shell unreachable: ${ready.message}`
                    : 'Run setup to configure developer token',
        };
        this.emit();
    }

    async attachSelection(): Promise<void> {
        await this.mutateContext(() => attachSelectionToContext(this.workspaceContext));
    }

    async attachActiveFile(): Promise<void> {
        await this.mutateContext(() => attachActiveFileToContext(this.workspaceContext));
    }

    async attachFiles(): Promise<void> {
        const picks = await vscode.window.showOpenDialog({
            canSelectMany: true,
            canSelectFolders: false,
            openLabel: 'Attach to agent context',
        });
        if (!picks?.length) {
            return;
        }
        const paths: string[] = [];
        for (const uri of picks) {
            if (!vscode.workspace.getWorkspaceFolder(uri)) {
                vscode.window.showWarningMessage(`Skipped ${uri.fsPath}: only workspace files can be attached.`);
                continue;
            }
            paths.push(vscode.workspace.asRelativePath(uri, false));
        }
        if (paths.length === 0) {
            return;
        }
        await this.mutateContext(() => attachFilesToContext(this.workspaceContext, paths));
    }

    async attachTerminal(): Promise<void> {
        await this.mutateContext(() => attachTerminalToContext(this.workspaceContext));
    }

    async attachSearch(): Promise<void> {
        const query = await vscode.window.showInputBox({
            prompt: 'Search workspace symbols to attach',
            placeHolder: 'e.g. LoginController',
        });
        if (!query) {
            return;
        }
        await this.mutateContext(() => attachSearchToContext(this.workspaceContext, query));
    }

    setUseCaseId(useCaseId: string): void {
        this.state = { ...this.state, useCaseId: useCaseId || 'uc-exploratory' };
        this.emit();
    }

    setWorkflowId(workflowId: string): void {
        this.state = { ...this.state, workflowId: workflowId || 'wf-unit-tests' };
        this.emit();
    }

    async invokeMcpTool(serverId: string, toolName: string, options?: { skipApproval?: boolean }): Promise<void> {
        const sessionId = this.state.sessionId;
        if (!sessionId) {
            vscode.window.showWarningMessage('Start or resume a governed session before calling MCP tools.');
            return;
        }
        const argsInput = await vscode.window.showInputBox({
            prompt: `MCP args JSON for ${serverId}/${toolName}`,
            value: '{}',
        });
        if (argsInput === undefined) {
            return;
        }
        let args: Record<string, unknown> = {};
        try {
            args = JSON.parse(argsInput || '{}') as Record<string, unknown>;
        } catch {
            vscode.window.showErrorMessage('Tool arguments must be valid JSON.');
            return;
        }
        if (!options?.skipApproval) {
            const choice = await vscode.window.showWarningMessage(
                `Call governed MCP tool ${serverId}/${toolName}?`,
                { modal: true },
                'Approve',
                'Deny'
            );
            const approved = evaluateToolApproval(
                { tool: `${serverId}/${toolName}`, risk: 'high' },
                normalizeApprovalChoice(choice)
            );
            if (!approved) {
                this.state = appendTimeline(this.state, 'MCP tool not approved', `${serverId}/${toolName}`);
                this.emit();
                return;
            }
        }
        const settings = await this.getSettings();
        const mcp = new BridgeMcpClient(settings);
        const result = await mcp.invokeTool(sessionId, serverId, toolName, args);
        if (!result.ok) {
            vscode.window.showErrorMessage(`MCP tool failed: ${result.body}`);
            this.state = appendTimeline(this.state, 'MCP denied/failed', `${serverId}/${toolName}`);
            this.emit();
            return;
        }
        this.state = appendTimeline(this.state, 'MCP tool executed', `${serverId}/${toolName}`);
        this.state = appendChatMessage(this.state, 'system', `MCP ${serverId}/${toolName} completed (gateway-enforced).`);
        this.emit();
        const client = new GovernanceClient(settings);
        await this.refreshSessionUsage(client, sessionId);
        vscode.window.showInformationMessage(`MCP tool ${toolName} completed via Governance Shell.`);
    }

    async approvePendingMcpTool(): Promise<void> {
        const pending = this.state.pendingToolRequest;
        if (!pending) {
            return;
        }
        const settings = await this.getSettings();
        const mcp = new BridgeMcpClient(settings);
        const sessionId = this.state.sessionId;
        if (!sessionId) {
            return;
        }
        let catalog: Array<{ serverId: string; toolName: string }> = [];
        try {
            catalog = flattenMcpTools(await mcp.fetchCatalog(sessionId));
        } catch (err: any) {
            vscode.window.showErrorMessage(err.message);
            return;
        }
        const pick = await vscode.window.showQuickPick(
            catalog.map((item) => ({
                label: `${item.serverId}/${item.toolName}`,
                description: 'Governed MCP proxy',
                item,
            })),
            { placeHolder: pending.payload || 'Select MCP tool to satisfy runtime request' }
        );
        if (!pick) {
            return;
        }
        await this.invokeMcpTool(pick.item.serverId, pick.item.toolName, { skipApproval: true });
        this.state = { ...this.state, pendingToolRequest: undefined };
        this.emit();
    }

    dismissPendingTool(): void {
        this.state = { ...this.state, pendingToolRequest: undefined };
        this.state = appendTimeline(this.state, 'Tool request dismissed');
        this.emit();
    }

    async resumeSession(sessionId: string): Promise<void> {
        const record = this.state.recentSessions.find((item) => item.sessionId === sessionId);
        if (!record) {
            return;
        }
        this.lastCompletedSessionId = sessionId;
        this.state = appendChatMessage(
            this.state,
            'system',
            `Resumed context from session ${sessionId} (${record.summary}). Send a follow-up message to start a new governed run.`
        );
        this.state = { ...this.state, sessionId, runId: record.runId, specialist: record.specialist };
        await this.refreshAuditTimeline(sessionId);
        this.emit();
    }

    abortRun(): void {
        this.streamCancel?.();
        this.streamCancel = undefined;
        this.state = {
            ...this.state,
            status: 'idle',
            statusDetail: 'Aborted',
            pendingRun: undefined,
        };
        this.state = appendTimeline(this.state, 'Run aborted');
        this.emit();
    }

    async sendMessage(text: string): Promise<void> {
        const trimmed = text.trim();
        if (!trimmed) {
            return;
        }

        const settings = await this.getSettings();
        if (!hasUsableDevToken(settings)) {
            vscode.commands.executeCommand('aiAgentBridge.onboard');
            return;
        }

        const client = new GovernanceClient(settings);
        const ready = await client.checkReady();
        if (!ready.ok) {
            this.state = { ...this.state, status: 'error', statusDetail: 'Not connected', error: ready.message };
            this.emit();
            return;
        }

        if (['running', 'connecting', 'awaiting_confirm', 'routing'].includes(this.state.status)) {
            vscode.window.showWarningMessage('Wait for the current run to finish or abort it first.');
            return;
        }

        const previousStatus = this.state.status;
        const previousContext = this.workspaceContext;
        this.workspaceContext = preserveExplicitContextAttachments(
            await collectWorkspacePromptContext(this.outputChannel),
            previousContext
        );

        const governedPrompt = buildContextualPrompt(trimmed, this.workspaceContext);
        const charEstimate = estimateContextChars(this.workspaceContext, trimmed);
        this.outputChannel.appendLine(`[agent] ${contextSummary(this.workspaceContext)}; ~${charEstimate} chars`);

        this.state = appendChatMessage(
            {
                ...this.state,
                connected: true,
                workspaceLabel: workspaceLabelFromContext(this.workspaceContext),
                branch: this.workspaceContext.branch,
                attachments: contextAttachments(this.workspaceContext),
                contextCharEstimate: charEstimate,
                status: 'connecting',
                statusDetail: 'Starting governed run…',
                patches: [],
                pendingRun: undefined,
                error: undefined,
            },
            'user',
            trimmed
        );
        this.state = appendTimeline(this.state, 'Run requested', trimmed.slice(0, 120));
        this.emit();

        try {
            const activeSession = this.state.sessionId;
            const canFollowUp =
                !!activeSession &&
                activeSession === this.lastCompletedSessionId &&
                ['idle', 'done', 'patch_ready', 'error'].includes(previousStatus);

            if (canFollowUp) {
                await this.startFollowUpTurn(client, activeSession, trimmed, governedPrompt);
                return;
            }

            const run = await client.startRun({
                prompt: governedPrompt,
                userIntent: trimmed,
                workspaceContext: this.workspaceContext,
                agentHint: this.state.agentHint,
                useCaseId: this.state.useCaseId,
                workflowId: this.state.workflowId,
            });

            await registerContextManifest(
                client,
                run.session_id,
                this.workspaceContext,
                `Bridge context: ${contextSummary(this.workspaceContext)}`
            );
            await this.refreshMcpTools(client, run.session_id);

            const pending = {
                runId: run.run_id,
                sessionId: run.session_id,
                specialist: run.specialist,
                reason: run.reason,
                routingConfidence: run.routing_confidence,
                humanConfirmationRequired: run.human_confirmation_required,
                routingAlternates: run.routing_alternates,
                userIntent: trimmed,
            };

            this.state = {
                ...this.state,
                runId: run.run_id,
                sessionId: run.session_id,
                specialist: run.specialist,
                specialistReason: run.reason,
                pendingRun: pending,
            };
            this.state = appendTimeline(this.state, 'Routed', `${run.specialist}: ${run.reason || 'no reason'}`);
            this.state = appendChatMessage(
                this.state,
                'system',
                `Router recommends **${run.specialist}**${run.reason ? `: ${run.reason}` : ''}.`
            );
            this.emit();

            if (this.state.autoConfirm && !run.human_confirmation_required) {
                await this.confirmPendingRun();
                return;
            }

            this.state = {
                ...this.state,
                status: 'awaiting_confirm',
                statusDetail: run.human_confirmation_required ? `Confirm low-confidence route to ${run.specialist}` : `Confirm ${run.specialist}`,
            };
            this.emit();
        } catch (err: any) {
            this.failRun(err.message || String(err));
        }
    }

    private async startFollowUpTurn(
        client: GovernanceClient,
        sessionId: string,
        trimmed: string,
        governedPrompt: string
    ): Promise<void> {
        const turn = await client.postTurn(sessionId, {
            prompt: governedPrompt,
            agent: this.state.agentHint,
            autoConfirm: this.state.autoConfirm,
            useCaseId: this.state.useCaseId,
            workflowId: this.state.workflowId,
        });

        this.state = {
            ...this.state,
            sessionId: turn.session_id,
            specialist: turn.specialist,
            specialistReason: turn.reason,
            patches: [],
            pendingToolRequest: undefined,
        };
        this.state = appendTimeline(this.state, 'Follow-up turn', trimmed.slice(0, 80));
        this.emit();

        if ((turn.human_confirmation_required || !this.state.autoConfirm) && turn.next_gate === 'confirm') {
            this.state = {
                ...this.state,
                status: 'awaiting_confirm',
                statusDetail: turn.human_confirmation_required ? `Confirm low-confidence route to ${turn.specialist}` : `Confirm ${turn.specialist}`,
                pendingRun: {
                    runId: this.state.runId || '',
                    sessionId: turn.session_id,
                    specialist: turn.specialist || this.state.agentHint,
                    reason: turn.reason || '',
                    routingConfidence: turn.routing_confidence,
                    humanConfirmationRequired: turn.human_confirmation_required,
                    routingAlternates: turn.routing_alternates,
                    userIntent: trimmed,
                },
            };
            this.state = appendChatMessage(
                this.state,
                'system',
                `Follow-up routed to **${turn.specialist}**. Confirm to run.`
            );
            this.emit();
            return;
        }

        await this.streamSessionExecution(client, turn.session_id, turn.specialist || this.state.agentHint, trimmed);
    }

    private async streamSessionExecution(
        client: GovernanceClient,
        sessionId: string,
        specialist: string,
        summary: string
    ): Promise<void> {
        this.state = {
            ...this.state,
            status: 'running',
            statusDetail: 'Streaming execution…',
            pendingRun: undefined,
        };
        this.emit();

        const stream = client.streamSessionUntilDone(sessionId, (event) => {
            this.outputChannel.appendLine(`[${event.type}] ${event.payload || ''}`);
            this.state = reduceSessionEvent(this.state, event);
            this.emit();
        });
        this.streamCancel = stream.cancel;

        try {
            await stream.promise;
        } catch (err: any) {
            this.failRun(err.message || String(err));
            return;
        } finally {
            this.streamCancel = undefined;
        }

        await this.finishRun(sessionId, this.state.runId || '', specialist, summary);
    }

    async confirmPendingRun(): Promise<void> {
        const pending = this.state.pendingRun;
        if (!pending) {
            return;
        }

        const settings = await this.getSettings();
        const client = new GovernanceClient(settings);

        this.state = {
            ...this.state,
            status: 'running',
            statusDetail: 'Streaming execution…',
            pendingRun: undefined,
            sessionId: pending.sessionId,
            runId: pending.runId,
            specialist: pending.specialist,
            specialistReason: pending.reason,
        };
        this.state = appendChatMessage(this.state, 'system', `Confirmed **${pending.specialist}**. Running…`);
        this.state = appendTimeline(this.state, 'Confirmed', pending.specialist);
        this.emit();

        const stream = client.streamSessionUntilDone(pending.sessionId, (event) => {
            this.outputChannel.appendLine(`[${event.type}] ${event.payload || ''}`);
            this.state = reduceSessionEvent(this.state, event);
            this.emit();
        });
        this.streamCancel = stream.cancel;

        try {
            const streamDone = stream.promise;
            await client.confirmSession(pending.sessionId, pending.specialist, Boolean(pending.humanConfirmationRequired));
            this.outputChannel.appendLine(`[agent] session confirmed: ${pending.sessionId}`);
            await streamDone;
        } catch (err: any) {
            stream.cancel();
            this.failRun(err.message || String(err));
            return;
        } finally {
            this.streamCancel = undefined;
        }

        await this.finishRun(pending.sessionId, pending.runId, pending.specialist, pending.userIntent);
    }

    cancelPendingRun(): void {
        this.state = { ...this.state, status: 'idle', statusDetail: 'Ready', pendingRun: undefined };
        this.state = appendChatMessage(this.state, 'system', 'Run cancelled.');
        this.emit();
    }

    async reviewPatches(): Promise<void> {
        const sessionId = this.state.sessionId;
        if (!sessionId || this.state.patches.length === 0) {
            return;
        }
        const settings = await this.getSettings();
        const client = new GovernanceClient(settings);
        const hydrated = await hydratePatches(client, sessionId, this.state.patches, this.outputChannel);
        await showPatchDiffs(hydrated, this.patchProvider, this.outputChannel);
        this.state = { ...this.state, patches: hydrated };
        this.emit();
    }

    async applyPatchesAndRecord(): Promise<void> {
        await this.applyPatchesWithMode('applied');
    }

    async partiallyApplyPatches(): Promise<void> {
        await this.applyPatchesWithMode('partially_applied', true);
    }

    async rejectPatches(): Promise<void> {
        const sessionId = this.state.sessionId;
        if (!sessionId) {
            return;
        }
        const settings = await this.getSettings();
        const client = new GovernanceClient(settings);
        const hydrated = await hydratePatches(client, sessionId, this.state.patches, this.outputChannel);
        await submitPatchDecisions(client, sessionId, hydrated, 'rejected');
        this.state = { ...this.state, status: 'idle', statusDetail: 'Ready', patches: [] };
        this.state = appendChatMessage(this.state, 'system', 'Patch rejected.');
        this.state = appendTimeline(this.state, 'Patch rejected');
        this.emit();
    }

    async showAudit(): Promise<void> {
        const sessionId = this.state.sessionId;
        if (!sessionId) {
            vscode.window.showWarningMessage('No active session to audit.');
            return;
        }
        const settings = await this.getSettings();
        const client = new GovernanceClient(settings);
        try {
            const audit = await client.lookupAudit(sessionId);
            const panel = vscode.window.createWebviewPanel(
                'aiAgentAudit',
                `Audit: ${sessionId}`,
                vscode.ViewColumn.One,
                {}
            );
            panel.webview.html = `<pre>${escapeHtml(JSON.stringify(audit, null, 2))}</pre>`;
            this.state = { ...this.state, timeline: timelineFromAuditEvents(audit.events || []) };
            this.emit();
        } catch (err: any) {
            vscode.window.showErrorMessage(`Audit lookup failed: ${err.message}`);
        }
    }

    setAgentHint(agent: string): void {
        this.state = { ...this.state, agentHint: agent || 'unit-tests' };
        this.emit();
    }

    newChat(): void {
        this.abortRun();
        const url = this.state.governanceUrl;
        this.state = {
            ...initialPanelState(url),
            governanceUrl: url,
            connected: this.state.connected,
            workspaceLabel: this.state.workspaceLabel,
            branch: this.state.branch,
            attachments: this.state.attachments,
            agentHint: this.state.agentHint,
            agents: this.state.agents,
            autoConfirm: this.state.autoConfirm,
            recentSessions: this.state.recentSessions,
            systemStatus: this.state.systemStatus,
        };
        this.lastCompletedSessionId = undefined;
        this.emit();
        void this.persistSnapshot();
    }

    private async applyPatchesWithMode(decision: string, pickFiles = false): Promise<void> {
        const sessionId = this.state.sessionId;
        if (!sessionId) {
            return;
        }
        const settings = await this.getSettings();
        const client = new GovernanceClient(settings);
        const hydrated = await hydratePatches(client, sessionId, this.state.patches, this.outputChannel);

        if (pickFiles) {
            const paths = new Set<string>();
            for (const patch of hydrated) {
                for (const file of patch.files || []) {
                    const path = file.path || file.filename;
                    if (path) {
                        paths.add(path);
                    }
                }
            }
            const picks = await vscode.window.showQuickPick([...paths], { canPickMany: true, placeHolder: 'Select files to apply' });
            if (!picks?.length) {
                return;
            }
            const applied = await applySelectedPatchFiles(hydrated, new Set(picks), this.outputChannel);
            await submitPatchDecisions(client, sessionId, applied, decision, 'User applied selected files in VS Code.');
        } else {
            await applyPatches(hydrated, this.outputChannel);
            await submitPatchDecisions(client, sessionId, hydrated, decision);
        }

        this.state = { ...this.state, status: 'idle', statusDetail: 'Ready', patches: [] };
        this.state = appendChatMessage(this.state, 'system', `Patch ${decision.replace('_', ' ')} and recorded.`);
        this.state = appendTimeline(this.state, 'Patch decision', decision);
        this.emit();
        vscode.window.showInformationMessage('Patch decision recorded.');
    }

    private async finishRun(sessionId: string, runId: string, specialist: string, summary: string): Promise<void> {
        this.lastCompletedSessionId = sessionId;

        if (this.state.patches.length > 0) {
            this.state = {
                ...this.state,
                status: 'patch_ready',
                statusDetail: 'Review proposed patch',
            };
            this.state = appendChatMessage(this.state, 'system', `${this.state.patches.length} patch(es) ready for review.`);
        } else {
            this.state = { ...this.state, status: 'idle', statusDetail: 'Ready for follow-up' };
        }

        const workspaceName = this.workspaceContext.workspaceName || 'default';
        if (this.storage) {
            const recent = await rememberRecentSession(this.storage, workspaceName, {
                sessionId,
                runId,
                workspaceName,
                specialist,
                summary: summary.slice(0, 120),
                updatedAt: Date.now(),
            });
            this.state = { ...this.state, recentSessions: recent };
        }

        await this.refreshAuditTimeline(sessionId);
        this.emit();
        void this.persistSnapshot();
    }

    private async refreshAuditTimeline(sessionId: string): Promise<void> {
        try {
            const settings = await this.getSettings();
            const client = new GovernanceClient(settings);
            const audit = await client.lookupAudit(sessionId);
            this.state = { ...this.state, timeline: timelineFromAuditEvents(audit.events || []) };
            if (audit.usage_summary) {
                this.state = { ...this.state, sessionUsage: mapUsage(audit.usage_summary) };
            }
        } catch {
            // keep stream-built timeline
        }
    }

    private async refreshSessionUsage(client: GovernanceClient, sessionId: string): Promise<void> {
        try {
            const audit = await client.lookupAudit(sessionId);
            if (audit.usage_summary) {
                this.state = { ...this.state, sessionUsage: mapUsage(audit.usage_summary) };
                this.emit();
            }
        } catch {
            // ignore
        }
    }

    private async refreshMcpTools(client: GovernanceClient, sessionId: string): Promise<void> {
        try {
            const settings = await this.getSettings();
            const mcp = new BridgeMcpClient(settings);
            const catalog = await mcp.fetchCatalog(sessionId);
            const tools = flattenMcpTools(catalog).map((item) => ({
                serverId: item.serverId,
                toolName: item.toolName,
                label: `${item.serverId}/${item.toolName}`,
            }));
            this.state = { ...this.state, mcpTools: tools };
            this.emit();
        } catch {
            this.state = { ...this.state, mcpTools: [] };
        }
    }

    private async loadRegistryOptions(client: GovernanceClient): Promise<void> {
        try {
            const [useCases, workflows] = await Promise.all([
                client.listUseCases(),
                client.listWorkflows(),
            ]);
            this.state = {
                ...this.state,
                useCases: useCases.map((uc) => ({
                    id: uc.id,
                    label: `${uc.id} (${uc.risk_level})`,
                    riskLevel: uc.risk_level,
                })),
                workflows: workflows.map((wf) => ({
                    id: wf.id,
                    label: wf.name || wf.id,
                })),
            };
            if (!this.state.useCaseId && useCases.length > 0) {
                this.state.useCaseId = useCases[0].id;
            }
            if (!this.state.workflowId && workflows.length > 0) {
                this.state.workflowId = workflows[0].id;
            }
        } catch {
            // registry list optional at connect time
        }
    }

    private async mutateContext(mutator: () => Promise<WorkspacePromptContext>): Promise<void> {
        try {
            this.workspaceContext = await mutator();
            this.patchAttachments();
            vscode.window.showInformationMessage('Context updated.');
        } catch (err: any) {
            vscode.window.showWarningMessage(err.message);
        }
    }

    private failRun(message: string): void {
        this.outputChannel.appendLine(`[agent] error: ${message}`);
        this.state = {
            ...this.state,
            status: 'error',
            statusDetail: 'Run failed',
            error: message,
            pendingRun: undefined,
        };
        this.state = appendChatMessage(this.state, 'system', `Error: ${message}`);
        this.state = appendTimeline(this.state, 'Error', message);
        this.emit();
        vscode.window.showErrorMessage(`Agent run failed: ${message}`);
    }

    private patchAttachments(): void {
        this.state = {
            ...this.state,
            attachments: contextAttachments(this.workspaceContext),
            workspaceLabel: workspaceLabelFromContext(this.workspaceContext),
            branch: this.workspaceContext.branch,
            contextCharEstimate: estimateContextChars(this.workspaceContext),
        };
        this.emit();
    }

    private async persistSnapshot(): Promise<void> {
        if (!this.storage) {
            return;
        }
        const workspaceName = this.workspaceContext.workspaceName || this.state.workspaceLabel || 'default';
        await savePanelSnapshot(this.storage, workspaceName, {
            messages: this.state.messages,
            agentHint: this.state.agentHint,
            lastSessionId: this.state.sessionId,
            lastRunId: this.state.runId,
        });
    }

    private emit(): void {
        this.onStateChange({ ...this.state });
        void this.persistSnapshot();
    }
}

function mapUsage(summary: SessionUsageSummary) {
    return {
        totalTokens: summary.total_tokens || 0,
        promptTokens: summary.prompt_tokens || 0,
        completionTokens: summary.completion_tokens || 0,
        estimatedCostUsd: summary.estimated_cost_usd || 0,
        modelProxyCalls: summary.model_proxy_calls || 0,
        mcpProxyCalls: summary.mcp_proxy_calls || 0,
        turnCount: summary.turn_count || 0,
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
