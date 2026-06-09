import { BridgeSettings } from './bridgeConfig';
import { authHeadersForBridge } from './bridgeWorkflow';

export interface McpCatalogServer {
    auth_mode?: string;
    tools: string[];
}

export interface McpToolCallResult {
    ok: boolean;
    status: number;
    body: string;
}

export class BridgeMcpClient {
    constructor(private readonly settings: BridgeSettings) {}

    private headers(sessionId: string, extra: Record<string, string> = {}): Record<string, string> {
        return authHeadersForBridge(
            {
                devToken: this.settings.devToken,
                identity: this.settings.identity,
                trustedClientToken: this.settings.trustedClientToken,
            },
            {
                'X-AI-Orch-Session-ID': sessionId,
                'Content-Type': 'application/json',
                ...extra,
            }
        );
    }

    async fetchCatalog(sessionId: string): Promise<Record<string, McpCatalogServer>> {
        const urls = [
            `${this.settings.governanceUrl}/v1/mcp/catalog`,
            `${this.settings.governanceUrl}/internal/v1/mcp/catalog`,
        ];
        let lastError = 'MCP catalog unavailable';
        for (const url of urls) {
            try {
                const response = await fetch(url, {
                    method: 'GET',
                    headers: this.headers(sessionId, { Accept: 'application/json' }),
                });
                const text = await response.text();
                if (!response.ok) {
                    lastError = `${response.status} ${text}`;
                    continue;
                }
                const payload = JSON.parse(text) as { servers?: Record<string, McpCatalogServer> };
                return payload.servers || {};
            } catch (err: any) {
                lastError = err.message || String(err);
            }
        }
        throw new Error(lastError);
    }

    async invokeTool(sessionId: string, serverId: string, toolName: string, args: Record<string, unknown> = {}): Promise<McpToolCallResult> {
        const paths = [
            `/v1/mcp/${encodeURIComponent(serverId)}/tools/${encodeURIComponent(toolName)}`,
            `/internal/v1/mcp/${encodeURIComponent(serverId)}/tools/${encodeURIComponent(toolName)}`,
        ];
        let lastError = 'MCP tool call failed';
        for (const path of paths) {
            try {
                const response = await fetch(`${this.settings.governanceUrl}${path}`, {
                    method: 'POST',
                    headers: this.headers(sessionId),
                    body: JSON.stringify(args),
                });
                const body = await response.text();
                if (response.ok) {
                    return { ok: true, status: response.status, body };
                }
                lastError = `${response.status} ${body}`;
            } catch (err: any) {
                lastError = err.message || String(err);
            }
        }
        return { ok: false, status: 0, body: lastError };
    }
}

export function flattenMcpTools(catalog: Record<string, McpCatalogServer>): Array<{ serverId: string; toolName: string }> {
    const items: Array<{ serverId: string; toolName: string }> = [];
    for (const [serverId, server] of Object.entries(catalog)) {
        for (const toolName of server.tools || []) {
            items.push({ serverId, toolName });
        }
    }
    return items.sort((a, b) => `${a.serverId}/${a.toolName}`.localeCompare(`${b.serverId}/${b.toolName}`));
}
