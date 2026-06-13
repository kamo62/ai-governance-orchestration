import { Buffer } from 'buffer';
import * as http from 'http';
import * as https from 'https';

import { BridgeSettings, validateGovernanceReadyResponse } from './bridgeConfig';
import { PatchEnvelope, SessionEvent, authHeadersForBridge, parseSessionEventLine } from './bridgeWorkflow';
import { WorkspacePromptContext } from './bridgeWorkspace';

export interface GovernedRun {
    run_id: string;
    session_id: string;
    status: string;
    specialist: string;
    reason: string;
    routing_confidence?: string;
    human_confirmation_required?: boolean;
    routing_alternates?: string[];
    next_gate: string;
    sse_url: string;
}

export interface AgentListItem {
    name: string;
    phase?: string;
}

export class GovernanceClient {
    constructor(private readonly settings: BridgeSettings) {}

    get baseUrl(): string {
        return this.settings.governanceUrl;
    }

    authHeaders(extra: Record<string, string> = {}): Record<string, string> {
        return authHeadersForBridge(
            {
                devToken: this.settings.devToken,
                identity: this.settings.identity,
                trustedClientToken: this.settings.trustedClientToken,
            },
            extra
        );
    }

    async checkReady(): Promise<{ ok: boolean; message: string }> {
        try {
            const response = await fetch(`${this.baseUrl}/readyz`, { method: 'GET' });
            const body = await response.text();
            return validateGovernanceReadyResponse(response.status, body);
        } catch (err: any) {
            return { ok: false, message: err.message || String(err) };
        }
    }

    async listAgents(): Promise<AgentListItem[]> {
        const response = await fetch(`${this.baseUrl}/v1/agents`, {
            method: 'GET',
            headers: this.authHeaders(),
        });
        if (!response.ok) {
            const text = await response.text();
            throw new Error(`List agents failed: ${response.status} ${text}`);
        }
        const payload = await response.json() as { agents?: AgentListItem[] };
        return payload.agents || [];
    }

    async startRun(input: {
        prompt: string;
        userIntent: string;
        workspaceContext: WorkspacePromptContext;
        agentHint?: string;
        useCaseId?: string;
        workflowId?: string;
    }): Promise<GovernedRun> {
        const response = await fetch(`${this.baseUrl}/v1/runs`, {
            method: 'POST',
            headers: this.authHeaders({ 'Content-Type': 'application/json' }),
            body: JSON.stringify({
                agent: input.agentHint || 'unit-tests',
                classification: 'internal',
                prompt: input.prompt,
                repo_url: input.workspaceContext.repoUrl || undefined,
                branch: input.workspaceContext.branch || undefined,
                work_item_id: input.workspaceContext.workItemId || undefined,
                work_item_type: input.workspaceContext.workItemType || undefined,
                source_system: input.workspaceContext.sourceSystem || undefined,
                use_case_id: input.useCaseId || undefined,
                workflow_id: input.workflowId || undefined,
                intent: input.userIntent,
                permission_mode: 'reviewed',
                approval_mode: 'manual',
                workspace_mode: 'local',
            }),
        });
        if (!response.ok) {
            const text = await response.text();
            throw new Error(`Start run failed: ${response.status} ${text}`);
        }
        return response.json() as Promise<GovernedRun>;
    }

    async confirmSession(sessionId: string, agent: string): Promise<{ session_id: string; status: string }> {
        const response = await fetch(`${this.baseUrl}/v1/sessions/${sessionId}/confirm`, {
            method: 'POST',
            headers: this.authHeaders({ 'Content-Type': 'application/json' }),
            body: JSON.stringify({ agent }),
        });
        if (!response.ok) {
            const text = await response.text();
            throw new Error(`Confirm session failed: ${response.status} ${text}`);
        }
        return response.json() as Promise<{ session_id: string; status: string }>;
    }

    streamSessionEvents(sessionId: string, onEvent: (event: SessionEvent) => void, onError: (err: Error) => void): { cancel: () => void } {
        const url = `${this.baseUrl}/v1/sessions/${sessionId}/events`;
        const requestGet = url.startsWith('https') ? https.get : http.get;
        let settled = false;
        const reqHolder: { req?: http.ClientRequest } = {};

        const finish = () => {
            if (settled) {
                return;
            }
            settled = true;
        };

        const fail = (err: Error) => {
            if (settled) {
                return;
            }
            settled = true;
            onError(err);
        };

        const timeout = setTimeout(() => {
            reqHolder.req?.destroy();
            fail(new Error('SSE stream timed out'));
        }, 300000);

        reqHolder.req = requestGet(url, {
            headers: this.authHeaders({ Accept: 'text/event-stream' }),
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
                        onEvent(data);
                        if (data.type === 'error') {
                            fail(new Error(data.payload || 'runtime emitted an error'));
                            return;
                        }
                        if (data.type === 'done') {
                            clearTimeout(timeout);
                            finish();
                            return;
                        }
                    } catch (err: any) {
                        fail(new Error(`Failed to parse SSE payload: ${err.message}`));
                        return;
                    }
                }
            });

            res.on('end', () => {
                clearTimeout(timeout);
                finish();
            });
            res.on('error', fail);
        });

        reqHolder.req.on('error', fail);

        return {
            cancel: () => {
                clearTimeout(timeout);
                reqHolder.req?.destroy();
                finish();
            },
        };
    }

    streamSessionUntilDone(
        sessionId: string,
        onEvent: (event: SessionEvent) => void
    ): { promise: Promise<void>; cancel: () => void } {
        let settled = false;
        let cancelRef: () => void = () => undefined;

        const promise = new Promise<void>((resolve, reject) => {
            const finish = () => {
                if (settled) {
                    return;
                }
                settled = true;
                resolve();
            };
            const fail = (err: Error) => {
                if (settled) {
                    return;
                }
                settled = true;
                reject(err);
            };

            const stream = this.streamSessionEvents(
                sessionId,
                (event) => {
                    onEvent(event);
                    if (event.type === 'done') {
                        finish();
                    }
                },
                fail
            );
            cancelRef = () => {
                stream.cancel();
                finish();
            };
        });

        return { promise, cancel: () => cancelRef() };
    }

    async fetchPatch(sessionId: string, patchId: string): Promise<PatchEnvelope> {
        const response = await fetch(
            `${this.baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/patches/${encodeURIComponent(patchId)}`,
            { method: 'GET', headers: this.authHeaders() }
        );
        if (!response.ok) {
            const text = await response.text();
            throw new Error(`Patch fetch failed: ${response.status} ${text}`);
        }
        return response.json() as Promise<PatchEnvelope>;
    }

    async submitPatchDecision(sessionId: string, patchId: string, decision: string, reason?: string): Promise<void> {
        const response = await fetch(`${this.baseUrl}/v1/sessions/${sessionId}/patch-decision`, {
            method: 'POST',
            headers: this.authHeaders({ 'Content-Type': 'application/json' }),
            body: JSON.stringify({ patch_id: patchId, decision, reason }),
        });
        if (!response.ok) {
            const text = await response.text();
            throw new Error(`Patch decision failed: ${response.status} ${text}`);
        }
    }

    async postTurn(
        sessionId: string,
        input: { prompt: string; agent?: string; autoConfirm?: boolean; useCaseId?: string; workflowId?: string }
    ): Promise<TurnResponse> {
        const response = await fetch(`${this.baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/turns`, {
            method: 'POST',
            headers: this.authHeaders({ 'Content-Type': 'application/json' }),
            body: JSON.stringify({
                prompt: input.prompt,
                agent: input.agent,
                auto_confirm: input.autoConfirm ?? true,
                use_case_id: input.useCaseId,
                workflow_id: input.workflowId,
            }),
        });
        if (!response.ok) {
            const text = await response.text();
            throw new Error(`Follow-up turn failed: ${response.status} ${text}`);
        }
        return response.json() as Promise<TurnResponse>;
    }

    async listUseCases(): Promise<UseCaseRecord[]> {
        const response = await fetch(`${this.baseUrl}/v1/use-cases`, {
            method: 'GET',
            headers: this.authHeaders(),
        });
        if (!response.ok) {
            const text = await response.text();
            throw new Error(`List use cases failed: ${response.status} ${text}`);
        }
        const payload = await response.json() as { use_cases?: UseCaseRecord[] };
        return payload.use_cases || [];
    }

    async listWorkflows(): Promise<WorkflowRecord[]> {
        const response = await fetch(`${this.baseUrl}/v1/workflows`, {
            method: 'GET',
            headers: this.authHeaders(),
        });
        if (!response.ok) {
            const text = await response.text();
            throw new Error(`List workflows failed: ${response.status} ${text}`);
        }
        const payload = await response.json() as { workflows?: WorkflowRecord[] };
        return payload.workflows || [];
    }

    async lookupAudit(sessionId: string): Promise<AuditLookupResponse> {
        const response = await fetch(`${this.baseUrl}/v1/audit/sessions/${sessionId}`, {
            method: 'GET',
            headers: this.authHeaders(),
        });
        if (!response.ok) {
            const text = await response.text();
            throw new Error(`Audit lookup failed: ${response.status} ${text}`);
        }
        return response.json() as Promise<AuditLookupResponse>;
    }

    async fetchSystemStatus(): Promise<SystemStatusResponse> {
        const response = await fetch(`${this.baseUrl}/v1/system/status`, {
            method: 'GET',
            headers: this.authHeaders(),
        });
        if (!response.ok) {
            const text = await response.text();
            throw new Error(`System status failed: ${response.status} ${text}`);
        }
        return response.json() as Promise<SystemStatusResponse>;
    }

    async createContextManifest(body: {
        id: string;
        session_id: string;
        summary: string;
        source_system: string;
        source_object_id: string;
        source_path?: string;
        classification: string;
        cache_status?: string;
        chunk_hashes?: string[];
    }): Promise<{ id: string }> {
        const response = await fetch(`${this.baseUrl}/v1/context-manifests`, {
            method: 'POST',
            headers: this.authHeaders({ 'Content-Type': 'application/json' }),
            body: JSON.stringify({
                actor: 'vscode-bridge',
                auth_scope: 'managed_client',
                influenced_model: true,
                ...body,
            }),
        });
        if (!response.ok) {
            const text = await response.text();
            throw new Error(`Context manifest failed: ${response.status} ${text}`);
        }
        return response.json() as Promise<{ id: string }>;
    }
}

export interface AuditEventRecord {
    event_type?: string;
    event_id?: string;
    reason?: string;
    agent?: string;
    recorded_at?: string;
}

export interface TurnResponse {
    session_id: string;
    status: string;
    specialist?: string;
    reason?: string;
    routing_confidence?: string;
    human_confirmation_required?: boolean;
    routing_alternates?: string[];
    next_gate?: string;
    sse_url?: string;
    turn?: boolean;
}

export interface UseCaseRecord {
    id: string;
    owner: string;
    domain: string;
    expected_benefit?: string;
    classification: string;
    risk_level: string;
}

export interface WorkflowRecord {
    id: string;
    name: string;
    description?: string;
    stages?: string[];
}

export interface SessionUsageSummary {
    total_tokens: number;
    prompt_tokens: number;
    completion_tokens: number;
    estimated_cost_usd: number;
    model_proxy_calls: number;
    mcp_proxy_calls: number;
    turn_count: number;
}

export interface AuditLookupResponse {
    session_id: string;
    events: AuditEventRecord[];
    usage_summary?: SessionUsageSummary;
}

export interface SystemStatusResponse {
    version?: string;
    model_backend?: string;
    model_gateway_addr?: string;
}
