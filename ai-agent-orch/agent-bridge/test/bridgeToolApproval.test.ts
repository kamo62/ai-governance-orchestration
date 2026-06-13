import { describe, expect, test } from 'bun:test';

import {
    evaluateToolApproval,
    normalizeApprovalChoice,
    ToolApprovalRequest,
} from '../src/bridgeToolApproval';

const highRisk: ToolApprovalRequest = { tool: 'docs/getPage', risk: 'high' };

describe('evaluateToolApproval', () => {
    test('allows execution only on explicit approve', () => {
        expect(evaluateToolApproval(highRisk, 'approve')).toBe(true);
    });

    test('blocks a denied tool', () => {
        expect(evaluateToolApproval(highRisk, 'deny')).toBe(false);
    });

    test('blocks a dismissed prompt', () => {
        expect(evaluateToolApproval(highRisk, 'dismiss')).toBe(false);
    });

    test('fails closed when no decision was made', () => {
        expect(evaluateToolApproval(highRisk, undefined)).toBe(false);
    });

    test('blocks every risk level without approval', () => {
        for (const risk of ['low', 'medium', 'high'] as const) {
            expect(evaluateToolApproval({ tool: 'x', risk }, 'deny')).toBe(false);
            expect(evaluateToolApproval({ tool: 'x', risk }, undefined)).toBe(false);
        }
    });
});

describe('normalizeApprovalChoice', () => {
    test('maps the VS Code modal results', () => {
        expect(normalizeApprovalChoice('Approve')).toBe('approve');
        expect(normalizeApprovalChoice('Deny')).toBe('deny');
        expect(normalizeApprovalChoice(undefined)).toBe('dismiss');
        expect(normalizeApprovalChoice('anything else')).toBe('dismiss');
    });

    test('a closed prompt never approves', () => {
        expect(evaluateToolApproval(highRisk, normalizeApprovalChoice(undefined))).toBe(false);
    });
});
