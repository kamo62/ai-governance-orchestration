import { PatchEnvelope } from './bridgeWorkflow';
import { WorkspacePromptContext } from './bridgeWorkspace';

export type AgentTaskStatus =
    | 'idle'
    | 'connecting'
    | 'routing'
    | 'awaiting_confirm'
    | 'running'
    | 'patch_ready'
    | 'done'
    | 'error';

export type ChatRole = 'user' | 'assistant' | 'system';

export interface ContextAttachment {
    id: string;
    label: string;
    kind: 'selection' | 'active_file' | 'file' | 'metadata' | 'terminal' | 'search';
}

export interface TimelineEntry {
    id: string;
    label: string;
    detail?: string;
    timestamp: number;
}

export interface RecentSessionRecord {
    sessionId: string;
    runId?: string;
    workspaceName: string;
    specialist?: string;
    summary: string;
    updatedAt: number;
}

export interface SystemStatusInfo {
    version?: string;
    modelBackend?: string;
    modelGatewayAddr?: string;
}

export interface SessionUsageView {
    totalTokens: number;
    promptTokens: number;
    completionTokens: number;
    estimatedCostUsd: number;
    modelProxyCalls: number;
    mcpProxyCalls: number;
    turnCount: number;
}

export interface UseCaseOption {
    id: string;
    label: string;
    riskLevel?: string;
}

export interface WorkflowOption {
    id: string;
    label: string;
}

export interface McpToolOption {
    serverId: string;
    toolName: string;
    label: string;
}

export interface PendingToolRequest {
    payload: string;
    suggestedServerId?: string;
    suggestedToolName?: string;
}

export interface ChatMessage {
    id: string;
    role: ChatRole;
    text: string;
    timestamp: number;
}

export interface PendingRun {
    runId: string;
    sessionId: string;
    specialist: string;
    reason: string;
    userIntent: string;
}

export interface AgentPanelState {
    status: AgentTaskStatus;
    statusDetail: string;
    governanceUrl: string;
    connected: boolean;
    workspaceLabel: string;
    branch?: string;
    sessionId?: string;
    runId?: string;
    specialist?: string;
    specialistReason?: string;
    messages: ChatMessage[];
    attachments: ContextAttachment[];
    pendingRun?: PendingRun;
    patches: PatchEnvelope[];
    agentHint: string;
    agents: Array<{ name: string; phase?: string }>;
    autoConfirm: boolean;
    contextCharEstimate: number;
    timeline: TimelineEntry[];
    recentSessions: RecentSessionRecord[];
    systemStatus?: SystemStatusInfo;
    activeTab: 'chat' | 'timeline';
    useCaseId: string;
    workflowId: string;
    useCases: UseCaseOption[];
    workflows: WorkflowOption[];
    sessionUsage?: SessionUsageView;
    mcpTools: McpToolOption[];
    pendingToolRequest?: PendingToolRequest;
    error?: string;
}

export function initialPanelState(governanceUrl: string): AgentPanelState {
    return {
        status: 'idle',
        statusDetail: 'Ready',
        governanceUrl,
        connected: false,
        workspaceLabel: 'No workspace folder',
        messages: [],
        attachments: [],
        patches: [],
        agentHint: 'unit-tests',
        agents: [],
        autoConfirm: false,
        contextCharEstimate: 0,
        timeline: [],
        recentSessions: [],
        activeTab: 'chat',
        useCaseId: 'uc-exploratory',
        workflowId: 'wf-unit-tests',
        useCases: [],
        workflows: [],
        mcpTools: [],
    };
}

export function attachmentsFromContext(context: WorkspacePromptContext): ContextAttachment[] {
    const items: ContextAttachment[] = [];
    if (context.workspaceName) {
        items.push({ id: 'workspace', label: context.workspaceName, kind: 'metadata' });
    }
    if (context.branch) {
        items.push({ id: 'branch', label: context.branch, kind: 'metadata' });
    }
    if (context.activeFile) {
        items.push({ id: 'active', label: context.activeFile, kind: 'active_file' });
    }
    if (context.selectedText?.trim()) {
        const range = context.selectedRange ? ` (${context.selectedRange})` : '';
        items.push({ id: 'selection', label: `Selection${range}`, kind: 'selection' });
    }
    for (const file of context.attachedFiles || []) {
        items.push({ id: `file-${file.path}`, label: file.path, kind: 'file' });
    }
    if (context.terminalExcerpt?.trim()) {
        items.push({ id: 'terminal', label: 'Terminal output', kind: 'terminal' });
    }
    if (context.searchHits?.length) {
        items.push({ id: 'search', label: `${context.searchHits.length} search hit(s)`, kind: 'search' });
    }
    return items;
}

export function nextMessageId(): string {
    return `msg_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
}