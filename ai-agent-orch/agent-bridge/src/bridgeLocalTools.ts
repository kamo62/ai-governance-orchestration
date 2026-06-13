import { Buffer } from 'buffer';
import * as vscode from 'vscode';

import { workspacePathParts } from './bridgeWorkflow';

export interface LocalToolRequest {
    tool: 'read_file' | 'list_files' | 'search_workspace' | 'run_command';
    label: string;
    detail: string;
    risk: 'low' | 'medium' | 'high';
    execute: () => Promise<string>;
}

export async function proposeLocalTool(request: LocalToolRequest): Promise<string | undefined> {
    const choice = await vscode.window.showWarningMessage(
        `${request.label}\n${request.detail}`,
        { modal: true },
        'Approve',
        'Deny'
    );
    if (choice !== 'Approve') {
        return undefined;
    }
    return request.execute();
}

export function readFileTool(relativePath: string): LocalToolRequest {
    return {
        tool: 'read_file',
        label: `Read file: ${relativePath}`,
        detail: 'Loads file contents into the next agent context (bounded).',
        risk: 'low',
        execute: async () => {
            const uri = workspaceFileUri(relativePath);
            const bytes = await vscode.workspace.fs.readFile(uri);
            const text = Buffer.from(bytes).toString('utf8');
            return text.slice(0, 12000);
        },
    };
}

export function listFilesTool(globPattern: string): LocalToolRequest {
    return {
        tool: 'list_files',
        label: `List files: ${globPattern}`,
        detail: 'Lists matching workspace paths (max 50).',
        risk: 'low',
        execute: async () => {
            const uris = await vscode.workspace.findFiles(globPattern, '**/node_modules/**', 50);
            return uris.map((uri) => vscode.workspace.asRelativePath(uri, false)).join('\n');
        },
    };
}

export function searchWorkspaceTool(query: string): LocalToolRequest {
    return {
        tool: 'search_workspace',
        label: `Search workspace: ${query}`,
        detail: 'Uses VS Code workspace symbol search (bounded).',
        risk: 'low',
        execute: async () => {
            const symbols = await vscode.commands.executeCommand<vscode.SymbolInformation[]>(
                'vscode.executeWorkspaceSymbolProvider',
                query
            );
            if (!symbols?.length) {
                return 'No symbols found.';
            }
            return symbols
                .slice(0, 30)
                .map((sym) => `${vscode.workspace.asRelativePath(sym.location.uri, false)}:${sym.name}`)
                .join('\n');
        },
    };
}

export function runCommandTool(command: string, cwd?: string): LocalToolRequest {
    return {
        tool: 'run_command',
        label: `Run command: ${command}`,
        detail: `Working directory: ${cwd || 'workspace root'}. Command runs in integrated terminal after approval.`,
        risk: 'high',
        execute: async () => {
            const terminal = vscode.window.createTerminal({
                name: 'AI Agent Bridge',
                cwd: cwd || vscode.workspace.workspaceFolders?.[0]?.uri.fsPath,
            });
            terminal.show();
            terminal.sendText(command);
            return `Command sent to terminal: ${command}`;
        },
    };
}

function workspaceFileUri(relativePath: string): vscode.Uri {
    const root = vscode.workspace.workspaceFolders?.[0];
    if (!root) {
        throw new Error('No workspace folder is open.');
    }
    return vscode.Uri.joinPath(root.uri, ...workspacePathParts(relativePath));
}