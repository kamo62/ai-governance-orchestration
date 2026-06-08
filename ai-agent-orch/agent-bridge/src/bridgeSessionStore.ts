import * as vscode from 'vscode';

import { ChatMessage } from './bridgeAgentTypes';

const RECENT_SESSIONS_KEY = 'aiAgentBridge.recentSessions';
const MAX_RECENT = 12;

export interface RecentSessionRecord {
    sessionId: string;
    runId?: string;
    workspaceName: string;
    specialist?: string;
    summary: string;
    updatedAt: number;
}

export interface PersistedPanelSnapshot {
    messages: ChatMessage[];
    agentHint: string;
    lastSessionId?: string;
    lastRunId?: string;
}

export function recentSessionsKey(workspaceName: string): string {
    return `${RECENT_SESSIONS_KEY}:${workspaceName}`;
}

export async function loadRecentSessions(
    storage: vscode.Memento,
    workspaceName: string
): Promise<RecentSessionRecord[]> {
    const raw = storage.get<RecentSessionRecord[]>(recentSessionsKey(workspaceName), []);
    return raw.sort((a, b) => b.updatedAt - a.updatedAt).slice(0, MAX_RECENT);
}

export async function rememberRecentSession(
    storage: vscode.Memento,
    workspaceName: string,
    record: RecentSessionRecord
): Promise<RecentSessionRecord[]> {
    const existing = await loadRecentSessions(storage, workspaceName);
    const filtered = existing.filter((item) => item.sessionId !== record.sessionId);
    const next = [record, ...filtered].slice(0, MAX_RECENT);
    await storage.update(recentSessionsKey(workspaceName), next);
    return next;
}

export function panelSnapshotKey(workspaceName: string): string {
    return `aiAgentBridge.panelSnapshot:${workspaceName}`;
}

export async function loadPanelSnapshot(
    storage: vscode.Memento,
    workspaceName: string
): Promise<PersistedPanelSnapshot | undefined> {
    return storage.get<PersistedPanelSnapshot>(panelSnapshotKey(workspaceName));
}

export async function savePanelSnapshot(
    storage: vscode.Memento,
    workspaceName: string,
    snapshot: PersistedPanelSnapshot
): Promise<void> {
    const trimmed: PersistedPanelSnapshot = {
        ...snapshot,
        messages: snapshot.messages.slice(-40),
    };
    await storage.update(panelSnapshotKey(workspaceName), trimmed);
}