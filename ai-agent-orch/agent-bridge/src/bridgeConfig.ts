export const DEFAULT_GOVERNANCE_URL = 'http://127.0.0.1:18080';
export const DEFAULT_IDENTITY = 'developer';
export const DEV_TOKEN_SECRET_KEY = 'aiAgentBridge.devToken';

export interface RawBridgeSettings {
    configuredGovernanceUrl?: string;
    configuredDevToken?: string;
    configuredIdentity?: string;
    envDevToken?: string;
    secretDevToken?: string;
}

export interface BridgeSettings {
    governanceUrl: string;
    devToken: string;
    identity: string;
}

export interface BridgeConnectionStatusInput {
    settings: BridgeSettings;
    ready: boolean;
    readyMessage?: string;
}

export interface BridgeConnectionStatus {
    ok: boolean;
    needsSetup: boolean;
    message: string;
    tokenMessage: string;
}

interface GovernanceReadyPayload {
    service?: string;
    status?: string;
}

export function normalizeGovernanceUrl(value?: string): string {
    const raw = (value || '').trim();
    if (!raw) {
        return DEFAULT_GOVERNANCE_URL;
    }

    if (raw.includes('://') && !/^https?:\/\//i.test(raw)) {
        throw new Error('Governance URL must use http or https.');
    }

    const withProtocol = /^https?:\/\//i.test(raw) ? raw : `http://${raw}`;
    let url: URL;
    try {
        url = new URL(withProtocol);
    } catch {
        throw new Error('Governance URL must be a valid http or https URL.');
    }
    if (url.protocol !== 'http:' && url.protocol !== 'https:') {
        throw new Error('Governance URL must use http or https.');
    }

    url.hash = '';
    url.search = '';
    if (url.pathname === '/') {
        url.pathname = '';
    } else {
        url.pathname = url.pathname.replace(/\/+$/, '');
    }
    return url.toString().replace(/\/$/, '');
}

export function resolveBridgeSettings(raw: RawBridgeSettings): BridgeSettings {
    return {
        governanceUrl: normalizeGovernanceUrl(raw.configuredGovernanceUrl),
        devToken: firstNonBlank(raw.secretDevToken, raw.configuredDevToken, raw.envDevToken),
        identity: firstNonBlank(raw.configuredIdentity) || DEFAULT_IDENTITY,
    };
}

export function hasUsableDevToken(settings: BridgeSettings): boolean {
    return settings.devToken.trim().length > 0;
}

export function bridgeConnectionStatus(input: BridgeConnectionStatusInput): BridgeConnectionStatus {
    const tokenConfigured = hasUsableDevToken(input.settings);
    const tokenMessage = tokenConfigured ? 'developer token is configured' : 'developer token is missing';
    if (!input.ready) {
        const readyMessage = input.readyMessage || 'not reachable';
        return {
            ok: false,
            needsSetup: false,
            message: `Governance Shell check failed: ${readyMessage}`,
            tokenMessage,
        };
    }

    return {
        ok: tokenConfigured,
        needsSetup: !tokenConfigured,
        message: `AI Agent Bridge connected to ${input.settings.governanceUrl}; ${tokenMessage}.`,
        tokenMessage,
    };
}

export function validateGovernanceReadyResponse(statusCode: number, body: string): { ok: boolean; message: string } {
    if (statusCode < 200 || statusCode >= 300) {
        return { ok: false, message: `HTTP ${statusCode}` };
    }

    let payload: GovernanceReadyPayload;
    try {
        payload = JSON.parse(body) as GovernanceReadyPayload;
    } catch {
        return { ok: false, message: 'readyz did not return Governance Shell JSON' };
    }

    if (payload.service !== 'governance-shell' || payload.status !== 'ready') {
        return { ok: false, message: 'readyz is not the AI Agent Orchestration Governance Shell' };
    }

    return { ok: true, message: 'ready' };
}

function firstNonBlank(...values: Array<string | undefined>): string {
    for (const value of values) {
        const trimmed = (value || '').trim();
        if (trimmed) {
            return trimmed;
        }
    }
    return '';
}
