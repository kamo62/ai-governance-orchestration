import * as vscode from 'vscode';

import { AgentPanelState } from './bridgeAgentTypes';
import { BridgeAgentController } from './bridgeAgentController';
import { getAgentWebviewHtml } from './bridgeAgentWebview';

export class BridgeAgentPanelProvider implements vscode.WebviewViewProvider {
    public static readonly viewType = 'aiAgentBridge.agentPanel';

    private view?: vscode.WebviewView;
    private controller?: BridgeAgentController;

    constructor(private readonly extensionUri: vscode.Uri) {}

    bindController(controller: BridgeAgentController): void {
        this.controller = controller;
        if (this.view) {
            this.postState(controller.getState());
        }
    }

    resolveWebviewView(
        webviewView: vscode.WebviewView,
        _context: vscode.WebviewViewResolveContext,
        _token: vscode.CancellationToken
    ): void {
        this.view = webviewView;
        webviewView.webview.options = {
            enableScripts: true,
            localResourceRoots: [this.extensionUri],
        };

        const nonce = getNonce();
        webviewView.webview.html = getAgentWebviewHtml(nonce);
        webviewView.webview.onDidReceiveMessage(async (message) => {
            const controller = this.controller;
            if (!controller) {
                return;
            }
            switch (message.type) {
                case 'ready':
                    await controller.refreshConnection();
                    break;
                case 'send':
                    await controller.sendMessage(message.text || '');
                    break;
                case 'confirm':
                    await controller.confirmPendingRun();
                    break;
                case 'cancel':
                    controller.cancelPendingRun();
                    break;
                case 'abort':
                    controller.abortRun();
                    break;
                case 'attachSelection':
                    await controller.attachSelection();
                    break;
                case 'attachFile':
                    await controller.attachActiveFile();
                    break;
                case 'attachFiles':
                    await controller.attachFiles();
                    break;
                case 'attachTerminal':
                    await controller.attachTerminal();
                    break;
                case 'attachSearch':
                    await controller.attachSearch();
                    break;
                case 'mcpTool':
                    await controller.invokeMcpTool(message.serverId || '', message.toolName || '');
                    break;
                case 'approveMcpTool':
                    await controller.approvePendingMcpTool();
                    break;
                case 'dismissTool':
                    controller.dismissPendingTool();
                    break;
                case 'setUseCase':
                    controller.setUseCaseId(message.useCaseId || '');
                    break;
                case 'setWorkflow':
                    controller.setWorkflowId(message.workflowId || '');
                    break;
                case 'reviewPatches':
                    await controller.reviewPatches();
                    break;
                case 'applyPatches':
                    await controller.applyPatchesAndRecord();
                    break;
                case 'partialApply':
                    await controller.partiallyApplyPatches();
                    break;
                case 'rejectPatches':
                    await controller.rejectPatches();
                    break;
                case 'showAudit':
                    await controller.showAudit();
                    break;
                case 'newChat':
                    controller.newChat();
                    break;
                case 'setAgent':
                    controller.setAgentHint(message.agent || 'unit-tests');
                    break;
                case 'setAutoConfirm':
                    controller.setAutoConfirm(!!message.enabled);
                    await vscode.workspace.getConfiguration('aiAgentBridge').update(
                        'autoConfirmSpecialist',
                        !!message.enabled,
                        vscode.ConfigurationTarget.Global
                    );
                    break;
                case 'setTab':
                    controller.setActiveTab(message.tab === 'timeline' ? 'timeline' : 'chat');
                    break;
                case 'resumeSession':
                    await controller.resumeSession(message.sessionId || '');
                    break;
                case 'refresh':
                    await controller.refreshConnection();
                    break;
                default:
                    break;
            }
        });

        if (this.controller) {
            this.postState(this.controller.getState());
        }
    }

    postState(state: AgentPanelState): void {
        this.view?.webview.postMessage({ type: 'state', state });
    }

    reveal(): void {
        if (this.view) {
            this.view.show?.(true);
        } else {
            vscode.commands.executeCommand('aiAgentBridge.agentPanel.focus');
        }
    }
}

function getNonce(): string {
    const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
    let text = '';
    for (let i = 0; i < 32; i++) {
        text += chars.charAt(Math.floor(Math.random() * chars.length));
    }
    return text;
}