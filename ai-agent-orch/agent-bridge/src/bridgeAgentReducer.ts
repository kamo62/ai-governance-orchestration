import { AgentPanelState, ChatMessage, TimelineEntry, nextMessageId } from './bridgeAgentTypes';
import { PatchEnvelope, parsePatchPayload, patchID } from './bridgeWorkflow';
import { SessionEvent } from './bridgeWorkflow';

export function appendTimeline(state: AgentPanelState, label: string, detail?: string): AgentPanelState {
    const entry: TimelineEntry = {
        id: nextMessageId(),
        label,
        detail,
        timestamp: Date.now(),
    };
    return { ...state, timeline: [...state.timeline, entry].slice(-64) };
}

export function parseStreamToolHint(payload: string): string | undefined {
    if (payload.startsWith('[tool_update]')) {
        return payload.replace('[tool_update]', '').trim();
    }
    if (payload.startsWith('[think]')) {
        return 'Thinking…';
    }
    return undefined;
}

export function appendChatMessage(
    state: AgentPanelState,
    role: ChatMessage['role'],
    text: string
): AgentPanelState {
    return {
        ...state,
        messages: [
            ...state.messages,
            { id: nextMessageId(), role, text, timestamp: Date.now() },
        ],
    };
}

export function appendAssistantStream(state: AgentPanelState, chunk: string): AgentPanelState {
    if (!chunk) {
        return state;
    }
    const messages = [...state.messages];
    const last = messages[messages.length - 1];
    if (last?.role === 'assistant') {
        messages[messages.length - 1] = { ...last, text: last.text + chunk };
        return { ...state, messages, status: 'running', statusDetail: 'Streaming…' };
    }
    messages.push({
        id: nextMessageId(),
        role: 'assistant',
        text: chunk,
        timestamp: Date.now(),
    });
    return { ...state, messages, status: 'running', statusDetail: 'Streaming…' };
}

export function reduceSessionEvent(state: AgentPanelState, event: SessionEvent): AgentPanelState {
    switch (event.type) {
        case 'router':
            return {
                ...state,
                status: 'routing',
                statusDetail: event.payload || 'Routing specialist…',
            };
        case 'tool_request': {
            return {
                ...appendTimeline(state, 'Tool approval required', event.payload || ''),
                pendingToolRequest: { payload: event.payload || '' },
                statusDetail: 'Approve MCP tool call',
            };
        }
        case 'stream': {
            const toolHint = parseStreamToolHint(event.payload || '');
            let next = appendAssistantStream(state, event.payload || '');
            if (toolHint) {
                next = appendTimeline(next, 'Tool activity', toolHint);
            }
            return next;
        }
        case 'patch': {
            let patch: PatchEnvelope;
            try {
                patch = parsePatchPayload(event.payload || '');
            } catch (err: any) {
                return {
                    ...state,
                    status: 'error',
                    statusDetail: 'Patch error',
                    error: err.message || String(err),
                };
            }
            const patches = [...state.patches, patch];
            return appendTimeline(
                {
                    ...state,
                    patches,
                    status: 'patch_ready',
                    statusDetail: `Patch ${patchID(patch)} ready (${patch.files?.length || 0} file(s))`,
                },
                'Patch proposed',
                patchID(patch)
            );
        }
        case 'error':
            return {
                ...state,
                status: 'error',
                statusDetail: 'Run failed',
                error: event.payload || 'runtime emitted an error',
            };
        case 'done':
            return {
                ...state,
                status: state.patches.length > 0 ? 'patch_ready' : 'done',
                statusDetail: state.patches.length > 0 ? 'Review proposed patch' : 'Run complete',
            };
        default:
            return state;
    }
}