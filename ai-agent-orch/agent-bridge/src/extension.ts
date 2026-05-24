import * as vscode from 'vscode';

// Governance Shell client configuration
const GOVERNANCE_URL = vscode.workspace.getConfiguration('aiAgentBridge').get<string>('governanceUrl') || 'http://127.0.0.1:8080';
const DEV_TOKEN = process.env.AI_ORCH_DEV_TOKEN || '';

export function activate(context: vscode.ExtensionContext) {
    const outputChannel = vscode.window.createOutputChannel('AI Agent Bridge');

    const invokeCommand = vscode.commands.registerCommand('aiAgentBridge.invokeAgent', async () => {
        const prompt = await vscode.window.showInputBox({
            prompt: 'What would you like the agent to do?',
            placeHolder: 'e.g., write Playwright tests for this component'
        });

        if (!prompt) {
            return;
        }

        outputChannel.appendLine(`Invoking agent with prompt length: ${prompt.length} characters`);

        try {
            // Step 1: Create a governed session
            const session = await createSession(prompt);
            outputChannel.appendLine(`Session created: ${session.session_id}`);

            // Step 2: Send message (triggers routing)
            const routeResult = await sendMessage(session.session_id, prompt);
            outputChannel.appendLine(`Recommended specialist: ${routeResult.specialist} (${routeResult.reason})`);

            // Step 3: Ask user to confirm
            const confirm = await vscode.window.showInformationMessage(
                `Recommended agent: ${routeResult.specialist}. Reason: ${routeResult.reason}`,
                'Confirm',
                'Cancel'
            );

            if (confirm !== 'Confirm') {
                outputChannel.appendLine('User cancelled specialist selection.');
                return;
            }

            // Step 4: Confirm session
            const confirmed = await confirmSession(session.session_id, routeResult.specialist);
            outputChannel.appendLine(`Session confirmed: ${confirmed.status}`);

            vscode.window.showInformationMessage(
                `Agent ${routeResult.specialist} is executing. Check output channel for progress.`,
                'Show Output'
            ).then(selection => {
                if (selection === 'Show Output') {
                    outputChannel.show();
                }
            });

        } catch (err: any) {
            vscode.window.showErrorMessage(`Agent invocation failed: ${err.message}`);
            outputChannel.appendLine(`Error: ${err.message}`);
        }
    });

    const showAuditCommand = vscode.commands.registerCommand('aiAgentBridge.showAudit', async () => {
        const sessionId = await vscode.window.showInputBox({
            prompt: 'Enter session ID to view audit',
            placeHolder: 'sess_...'
        });

        if (!sessionId) {
            return;
        }

        try {
            const audit = await lookupAudit(sessionId);
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
    });

    context.subscriptions.push(invokeCommand, showAuditCommand, outputChannel);
}

async function createSession(prompt: string): Promise<any> {
    const response = await fetch(`${GOVERNANCE_URL}/v1/sessions`, {
        method: 'POST',
        headers: {
            'Authorization': `Bearer ${DEV_TOKEN}`,
            'Content-Type': 'application/json'
        },
        body: JSON.stringify({
            agent: 'test-generation',
            classification: 'internal',
            prompt: prompt
        })
    });

    if (!response.ok) {
        const text = await response.text();
        throw new Error(`Create session failed: ${response.status} ${text}`);
    }

    return response.json();
}

async function sendMessage(sessionId: string, prompt: string): Promise<any> {
    const response = await fetch(`${GOVERNANCE_URL}/v1/sessions/${sessionId}/messages`, {
        method: 'POST',
        headers: {
            'Authorization': `Bearer ${DEV_TOKEN}`,
            'Content-Type': 'application/json'
        },
        body: JSON.stringify({ prompt })
    });

    if (!response.ok) {
        const text = await response.text();
        throw new Error(`Send message failed: ${response.status} ${text}`);
    }

    return response.json();
}

async function confirmSession(sessionId: string, agent: string): Promise<any> {
    const response = await fetch(`${GOVERNANCE_URL}/v1/sessions/${sessionId}/confirm`, {
        method: 'POST',
        headers: {
            'Authorization': `Bearer ${DEV_TOKEN}`,
            'Content-Type': 'application/json'
        },
        body: JSON.stringify({ agent })
    });

    if (!response.ok) {
        const text = await response.text();
        throw new Error(`Confirm session failed: ${response.status} ${text}`);
    }

    return response.json();
}

async function lookupAudit(sessionId: string): Promise<any> {
    const response = await fetch(`${GOVERNANCE_URL}/v1/audit/sessions/${sessionId}`, {
        method: 'GET',
        headers: {
            'Authorization': `Bearer ${DEV_TOKEN}`
        }
    });

    if (!response.ok) {
        const text = await response.text();
        throw new Error(`Audit lookup failed: ${response.status} ${text}`);
    }

    return response.json();
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
