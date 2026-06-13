// Pure, vscode-free approval policy for governed tool calls so the gate can be
// unit-tested independently of the VS Code UI layer.

export type ToolRisk = 'low' | 'medium' | 'high';

// ApprovalDecision is the normalized outcome of the approval prompt.
// `undefined` represents a dismissed/closed prompt (no choice made).
export type ApprovalDecision = 'approve' | 'deny' | 'dismiss' | undefined;

export interface ToolApprovalRequest {
    tool: string;
    risk: ToolRisk;
}

// evaluateToolApproval decides whether a governed tool may execute. It fails
// closed: only an explicit 'approve' permits execution. Deny, dismiss, or a
// missing decision all block, so a tool is never run without the user
// affirmatively approving it.
export function evaluateToolApproval(
    _request: ToolApprovalRequest,
    decision: ApprovalDecision
): boolean {
    return decision === 'approve';
}

// normalizeApprovalChoice maps a VS Code modal result ('Approve' | 'Deny' |
// undefined) to an ApprovalDecision.
export function normalizeApprovalChoice(choice: string | undefined): ApprovalDecision {
    if (choice === 'Approve') {
        return 'approve';
    }
    if (choice === 'Deny') {
        return 'deny';
    }
    return 'dismiss';
}
