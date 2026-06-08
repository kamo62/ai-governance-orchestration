import * as vscode from 'vscode';

import { BridgeAgentController } from './bridgeAgentController';
import { BridgeAgentPanelProvider } from './bridgeAgentPanel';
import {
    DEFAULT_GOVERNANCE_URL,
    DEFAULT_IDENTITY,
    DEV_TOKEN_SECRET_KEY,
    BridgeSettings,
    bridgeConnectionStatus,
    hasUsableDevToken,
    normalizeGovernanceUrl,
    resolveBridgeSettings,
    validateGovernanceReadyResponse,
} from './bridgeConfig';
import { GovernanceClient } from './bridgeGovernanceClient';
import { PatchContentProvider } from './bridgePatchService';

const patchProvider = new PatchContentProvider();
let panelProvider: BridgeAgentPanelProvider;
let agentController: BridgeAgentController;

export async function activate(context: vscode.ExtensionContext) {
    const outputChannel = vscode.window.createOutputChannel('AI Agent Bridge');
    await refreshBridgeSettings(context);

    panelProvider = new BridgeAgentPanelProvider(context.extensionUri);
    agentController = new BridgeAgentController(
        () => refreshBridgeSettings(context),
        () => vscode.workspace.getConfiguration('aiAgentBridge').get<boolean>('autoConfirmSpecialist', false),
        patchProvider,
        outputChannel,
        context.globalState,
        (state) => panelProvider.postState(state)
    );
    panelProvider.bindController(agentController);

    context.subscriptions.push(
        vscode.workspace.registerTextDocumentContentProvider('aiagentbridge-patch', patchProvider),
        vscode.window.registerWebviewViewProvider(BridgeAgentPanelProvider.viewType, panelProvider, {
            webviewOptions: { retainContextWhenHidden: true },
        })
    );

    const focusPanel = vscode.commands.registerCommand('aiAgentBridge.focusPanel', () => {
        panelProvider.reveal();
    });

    const onboardCommand = vscode.commands.registerCommand('aiAgentBridge.onboard', async () => {
        await runOnboarding(context, outputChannel);
    });

    const checkConnectionCommand = vscode.commands.registerCommand('aiAgentBridge.checkConnection', async () => {
        await runConnectionCheck(context, outputChannel, true);
        await agentController.refreshConnection();
    });

    const invokeCommand = vscode.commands.registerCommand('aiAgentBridge.invokeAgent', async () => {
        if (!await ensureBridgeReady(context, outputChannel)) {
            return;
        }
        panelProvider.reveal();
        const prompt = await vscode.window.showInputBox({
            prompt: 'What would you like the agent to do?',
            placeHolder: 'e.g., write Playwright tests for this component',
        });
        if (prompt) {
            await agentController.sendMessage(prompt);
        }
    });

    const showAuditCommand = vscode.commands.registerCommand('aiAgentBridge.showAudit', async (sessionId?: string) => {
        if (!await ensureBridgeReady(context, outputChannel)) {
            return;
        }
        if (sessionId) {
            const settings = await refreshBridgeSettings(context);
            const client = new GovernanceClient(settings);
            try {
                const audit = await client.lookupAudit(sessionId);
                const panel = vscode.window.createWebviewPanel(
                    'aiAgentAudit',
                    `Audit: ${sessionId}`,
                    vscode.ViewColumn.One,
                    {}
                );
                panel.webview.html = `<pre>${escapeHtml(JSON.stringify(audit, null, 2))}</pre>`;
            } catch (err: any) {
                vscode.window.showErrorMessage(`Audit lookup failed: ${err.message}`);
            }
            return;
        }
        await agentController.showAudit();
        panelProvider.reveal();
    });

    const attachSelection = vscode.commands.registerCommand('aiAgentBridge.attachSelection', async () => {
        panelProvider.reveal();
        await agentController.attachSelection();
    });

    const attachActiveFile = vscode.commands.registerCommand('aiAgentBridge.attachActiveFile', async () => {
        panelProvider.reveal();
        await agentController.attachActiveFile();
    });

    context.subscriptions.push(
        focusPanel,
        onboardCommand,
        checkConnectionCommand,
        invokeCommand,
        showAuditCommand,
        attachSelection,
        attachActiveFile,
        outputChannel
    );

    await agentController.refreshConnection();

    const settings = await refreshBridgeSettings(context);
    if (!hasUsableDevToken(settings)) {
        const action = await vscode.window.showInformationMessage(
            'AI Agent Bridge needs first-run setup before it can call the Governance Shell.',
            'Run Setup',
            'Open Agent Panel',
            'Later'
        );
        if (action === 'Run Setup') {
            await runOnboarding(context, outputChannel);
        } else if (action === 'Open Agent Panel') {
            panelProvider.reveal();
        }
    } else {
        setTimeout(() => panelProvider.reveal(), 500);
    }
}

async function refreshBridgeSettings(context: vscode.ExtensionContext): Promise<BridgeSettings> {
    const config = vscode.workspace.getConfiguration('aiAgentBridge');
    const settings = resolveBridgeSettings({
        configuredGovernanceUrl: config.get<string>('governanceUrl'),
        configuredDevToken: config.get<string>('devToken'),
        configuredIdentity: config.get<string>('identity'),
        envDevToken: process.env.AI_ORCH_DEV_TOKEN,
        secretDevToken: await context.secrets.get(DEV_TOKEN_SECRET_KEY),
    });

    return settings;
}

async function runOnboarding(context: vscode.ExtensionContext, outputChannel: vscode.OutputChannel): Promise<boolean> {
    const current = await refreshBridgeSettings(context);
    const config = vscode.workspace.getConfiguration('aiAgentBridge');

    const urlInput = await vscode.window.showInputBox({
        title: 'AI Agent Bridge Setup',
        prompt: 'Governance Shell URL',
        value: current.governanceUrl,
        placeHolder: DEFAULT_GOVERNANCE_URL,
        validateInput: (value) => {
            try {
                normalizeGovernanceUrl(value);
                return undefined;
            } catch (err: any) {
                return err.message;
            }
        },
    });
    if (urlInput === undefined) {
        return false;
    }

    const nextUrl = normalizeGovernanceUrl(urlInput);
    await config.update('governanceUrl', nextUrl, vscode.ConfigurationTarget.Global);

    const identityInput = await vscode.window.showInputBox({
        title: 'AI Agent Bridge Setup',
        prompt: 'Identity label for audit events',
        value: current.identity || DEFAULT_IDENTITY,
        placeHolder: DEFAULT_IDENTITY,
    });
    if (identityInput === undefined) {
        return false;
    }
    await config.update('identity', identityInput.trim() || DEFAULT_IDENTITY, vscode.ConfigurationTarget.Global);

    const existingToken = hasUsableDevToken(current);
    const tokenInput = await vscode.window.showInputBox({
        title: 'AI Agent Bridge Setup',
        prompt: existingToken
            ? 'Developer token. Leave blank to keep the stored token.'
            : 'Developer token. For the local Compose default, use local-dev.',
        placeHolder: 'local-dev',
        password: true,
        validateInput: (value) => {
            if (existingToken || value.trim()) {
                return undefined;
            }
            return 'A developer token is required. Use local-dev for the default local Compose stack.';
        },
    });
    if (tokenInput === undefined) {
        return false;
    }
    if (tokenInput.trim()) {
        await context.secrets.store(DEV_TOKEN_SECRET_KEY, tokenInput.trim());
    }

    outputChannel.appendLine(`Bridge setup saved. Governance URL: ${nextUrl}`);
    const ready = await runConnectionCheck(context, outputChannel, false);
    await agentController.refreshConnection();
    if (!ready) {
        const action = await vscode.window.showWarningMessage(
            'Setup saved, but the Governance Shell is not reachable yet.',
            'Show Output'
        );
        if (action === 'Show Output') {
            outputChannel.show();
        }
        return false;
    }

    const action = await vscode.window.showInformationMessage(
        'AI Agent Bridge is ready.',
        'Open Agent Panel',
        'Show Output'
    );
    if (action === 'Open Agent Panel') {
        panelProvider.reveal();
    } else if (action === 'Show Output') {
        outputChannel.show();
    }
    return true;
}

async function ensureBridgeReady(context: vscode.ExtensionContext, outputChannel: vscode.OutputChannel): Promise<boolean> {
    const settings = await refreshBridgeSettings(context);
    if (!hasUsableDevToken(settings)) {
        const action = await vscode.window.showWarningMessage(
            'AI Agent Bridge needs setup before it can call the Governance Shell.',
            'Run Setup',
            'Cancel'
        );
        if (action === 'Run Setup') {
            return runOnboarding(context, outputChannel);
        }
        return false;
    }

    const ready = await checkGovernanceReady(settings.governanceUrl);
    if (ready.ok) {
        return true;
    }

    outputChannel.appendLine(`[setup] Governance Shell is not reachable at ${settings.governanceUrl}: ${ready.message}`);
    const action = await vscode.window.showWarningMessage(
        `Governance Shell is not reachable at ${settings.governanceUrl}.`,
        'Run Setup',
        'Show Output',
        'Cancel'
    );
    if (action === 'Run Setup') {
        return runOnboarding(context, outputChannel);
    }
    if (action === 'Show Output') {
        outputChannel.show();
    }
    return false;
}

async function runConnectionCheck(context: vscode.ExtensionContext, outputChannel: vscode.OutputChannel, showResult: boolean): Promise<boolean> {
    const settings = await refreshBridgeSettings(context);
    const ready = await checkGovernanceReady(settings.governanceUrl);
    if (!ready.ok) {
        outputChannel.appendLine(`[setup] Governance Shell check failed at ${settings.governanceUrl}: ${ready.message}`);
        if (showResult) {
            vscode.window.showWarningMessage(`Governance Shell check failed: ${ready.message}`, 'Show Output').then((action) => {
                if (action === 'Show Output') {
                    outputChannel.show();
                }
            });
        }
        return false;
    }

    const status = bridgeConnectionStatus({ settings, ready: ready.ok, readyMessage: ready.message });
    outputChannel.appendLine(`[setup] Governance Shell ready at ${settings.governanceUrl}; ${status.tokenMessage}.`);
    if (showResult) {
        if (status.needsSetup) {
            const action = await vscode.window.showWarningMessage(status.message, 'Run Setup', 'Show Output', 'Cancel');
            if (action === 'Run Setup') {
                return runOnboarding(context, outputChannel);
            }
            if (action === 'Show Output') {
                outputChannel.show();
            }
        } else {
            vscode.window.showInformationMessage(status.message);
        }
    }
    return status.ok;
}

async function checkGovernanceReady(baseUrl: string): Promise<{ ok: boolean; message: string }> {
    try {
        const response = await fetch(`${baseUrl}/readyz`, { method: 'GET' });
        const body = await response.text();
        return validateGovernanceReadyResponse(response.status, body);
    } catch (err: any) {
        return { ok: false, message: err.message || String(err) };
    }
}

function escapeHtml(value: string): string {
    return value
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

export function deactivate() {}