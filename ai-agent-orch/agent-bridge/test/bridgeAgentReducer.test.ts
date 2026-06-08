import { describe, expect, test } from 'bun:test';

import { appendAssistantStream, parseStreamToolHint, reduceSessionEvent } from '../src/bridgeAgentReducer';
import { initialPanelState } from '../src/bridgeAgentTypes';

describe('Bridge agent reducer', () => {
    test('appends stream chunks to a single assistant message', () => {
        let state = initialPanelState('http://127.0.0.1:18080');
        state = appendAssistantStream(state, 'Hello ');
        state = appendAssistantStream(state, 'world');
        expect(state.messages.length).toBe(1);
        expect(state.messages[0].role).toBe('assistant');
        expect(state.messages[0].text).toBe('Hello world');
        expect(state.status).toBe('running');
    });

    test('reduces patch events into patch_ready state', () => {
        let state = initialPanelState('http://127.0.0.1:18080');
        state = reduceSessionEvent(state, {
            type: 'patch',
            payload: JSON.stringify({ patchId: 'patch_1', files: [{ path: 'a.ts', action: 'modify' }] }),
        });
        expect(state.status).toBe('patch_ready');
        expect(state.patches.length).toBe(1);
        expect(state.patches[0].patchId).toBe('patch_1');
    });

    test('reduces done without patches to done status', () => {
        const state = reduceSessionEvent(initialPanelState('http://127.0.0.1:18080'), {
            type: 'done',
            payload: '',
        });
        expect(state.status).toBe('done');
    });

    test('reduces tool_request into pending MCP approval state', () => {
        const state = reduceSessionEvent(initialPanelState('http://127.0.0.1:18080'), {
            type: 'tool_request',
            payload: 'read_file src/main.ts',
        });
        expect(state.pendingToolRequest?.payload).toBe('read_file src/main.ts');
        expect(state.statusDetail).toContain('MCP');
    });

    test('detects tool hints in stream payloads', () => {
        expect(parseStreamToolHint('[tool_update] read_file')).toBe('read_file');
        expect(parseStreamToolHint('[think] planning')).toBe('Thinking…');
        expect(parseStreamToolHint('plain text')).toBeUndefined();
    });

    test('reduces errors into error state', () => {
        const state = reduceSessionEvent(initialPanelState('http://127.0.0.1:18080'), {
            type: 'error',
            payload: 'boom',
        });
        expect(state.status).toBe('error');
        expect(state.error).toBe('boom');
    });
});
