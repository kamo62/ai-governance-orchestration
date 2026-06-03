import { describe, expect, test } from 'bun:test';

import {
    DEFAULT_GOVERNANCE_URL,
    bridgeConnectionStatus,
    hasUsableDevToken,
    normalizeGovernanceUrl,
    resolveBridgeSettings,
    validateGovernanceReadyResponse,
} from '../src/bridgeConfig';

describe('Bridge configuration helpers', () => {
    test('normalizes local governance URLs for first-run input', () => {
        expect(normalizeGovernanceUrl('127.0.0.1:8080/')).toBe('http://127.0.0.1:8080');
        expect(normalizeGovernanceUrl(' http://localhost:18080/// ')).toBe('http://localhost:18080');
        expect(normalizeGovernanceUrl('')).toBe(DEFAULT_GOVERNANCE_URL);
        expect(DEFAULT_GOVERNANCE_URL).toBe('http://127.0.0.1:18080');
    });

    test('rejects non-http governance URLs', () => {
        expect(() => normalizeGovernanceUrl('file:///tmp/server')).toThrow('Governance URL must use http or https');
    });

    test('resolves Bridge settings with SecretStorage token first', () => {
        const settings = resolveBridgeSettings({
            configuredGovernanceUrl: 'localhost:8080/',
            configuredDevToken: 'settings-token',
            configuredIdentity: ' developer ',
            envDevToken: 'env-token',
            secretDevToken: 'secret-token',
        });

        expect(settings.governanceUrl).toBe('http://localhost:8080');
        expect(settings.devToken).toBe('secret-token');
        expect(settings.identity).toBe('developer');
    });

    test('detects missing dev token after trimming whitespace', () => {
        expect(hasUsableDevToken({ governanceUrl: DEFAULT_GOVERNANCE_URL, devToken: ' ', identity: 'developer' })).toBe(false);
        expect(hasUsableDevToken({ governanceUrl: DEFAULT_GOVERNANCE_URL, devToken: 'local-dev', identity: 'developer' })).toBe(true);
    });

    test('marks a ready service without a token as needing setup', () => {
        const status = bridgeConnectionStatus({
            settings: { governanceUrl: DEFAULT_GOVERNANCE_URL, devToken: '', identity: 'developer' },
            ready: true,
        });

        expect(status.ok).toBe(false);
        expect(status.needsSetup).toBe(true);
        expect(status.message).toContain('developer token is missing');
    });

    test('accepts only the Governance Shell readiness payload', () => {
        expect(validateGovernanceReadyResponse(200, '{"service":"governance-shell","status":"ready"}')).toEqual({
            ok: true,
            message: 'ready',
        });
        expect(validateGovernanceReadyResponse(200, '<!doctype html><title>Other app</title>')).toEqual({
            ok: false,
            message: 'readyz did not return Governance Shell JSON',
        });
        expect(validateGovernanceReadyResponse(200, '{"service":"other","status":"ready"}').ok).toBe(false);
        expect(validateGovernanceReadyResponse(404, '{"error":"not found"}')).toEqual({
            ok: false,
            message: 'HTTP 404',
        });
    });
});
