import { describe, expect, test } from 'bun:test';

import { contextManifestId, estimateContextChars } from '../src/bridgeContextManifest';
import { buildContextualPrompt } from '../src/bridgeWorkspace';

describe('Bridge context manifest helpers', () => {
    test('estimates bounded context size', () => {
        const estimate = estimateContextChars(
            {
                selectedText: 'a'.repeat(100),
                attachedFiles: [{ path: 'x.ts', excerpt: 'b'.repeat(50) }],
            },
            'hello'
        );
        expect(estimate).toBe(155);
    });

    test('builds stable manifest ids', () => {
        const a = contextManifestId('sess_1', 'vscode-bridge', 'src/foo.ts');
        const b = contextManifestId('sess_1', 'vscode-bridge', 'src/foo.ts');
        expect(a).toBe(b);
        expect(a.startsWith('cm_')).toBe(true);
    });

    test('includes attached files in governed prompt', () => {
        const prompt = buildContextualPrompt('fix tests', {
            workspaceName: 'demo',
            attachedFiles: [{ path: 'a.ts', excerpt: 'export const x = 1;' }],
        });
        expect(prompt).toContain('attached_file: a.ts');
        expect(prompt).toContain('export const x = 1;');
    });
});