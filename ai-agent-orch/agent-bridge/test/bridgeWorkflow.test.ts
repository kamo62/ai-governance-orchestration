import { describe, expect, test } from 'bun:test';

import {
    authHeadersForBridge,
    parsePatchPayload,
    parseSessionEventLine,
    patchID,
    workspacePathParts,
} from '../src/bridgeWorkflow';

describe('Bridge workflow helpers', () => {
    test('extracts patch IDs from camelCase and snake_case envelopes', () => {
        expect(patchID({ patchId: 'patch_camel' })).toBe('patch_camel');
        expect(patchID({ patch_id: 'patch_snake' })).toBe('patch_snake');
        expect(patchID({})).toBe('unknown');
    });

    test('rejects patch SSE payloads without a patch ID', () => {
        expect(() => parsePatchPayload('{"files":[]}')).toThrow('patch payload is missing patchId');
    });

    test('parses patch SSE payloads with metadata-only files', () => {
        const patch = parsePatchPayload('{"patchId":"patch_1","files":[{"path":"tests/example.spec.ts","action":"create"}]}');

        expect(patchID(patch)).toBe('patch_1');
        expect(patch.files?.[0]?.path).toBe('tests/example.spec.ts');
    });

    test('validates workspace patch paths before diff or apply', () => {
        expect(workspacePathParts('src/example.ts')).toEqual(['src', 'example.ts']);
        expect(workspacePathParts('src\\example.ts')).toEqual(['src', 'example.ts']);
        expect(() => workspacePathParts('../secret.txt')).toThrow('unsafe segments');
        expect(() => workspacePathParts('/tmp/secret.txt')).toThrow('must be relative');
        expect(() => workspacePathParts('C:\\secret.txt')).toThrow('must be relative');
        expect(() => workspacePathParts('')).toThrow('Patch file path is required');
    });

    test('builds governed auth headers and fails closed without a token', () => {
        expect(authHeadersForBridge({ devToken: ' local-dev ', identity: 'developer' }, { Accept: 'text/event-stream' })).toEqual({
            Authorization: 'Bearer local-dev',
            'X-AI-Orch-Local-Identity': 'developer',
            Accept: 'text/event-stream',
        });
        expect(() => authHeadersForBridge({ devToken: ' ', identity: 'developer' })).toThrow('developer token is required');
    });

    test('parses only SSE data lines used by the governed session stream', () => {
        expect(parseSessionEventLine('event: message')).toBeUndefined();
        expect(parseSessionEventLine('data: {"type":"done","payload":"ok"}')).toEqual({ type: 'done', payload: 'ok' });
        expect(() => parseSessionEventLine('data: not-json')).toThrow('Failed to parse SSE payload');
    });
});
