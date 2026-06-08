export function getAgentWebviewHtml(nonce: string): string {
    return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>AI Agent Bridge</title>
  <style>
    :root {
      --bg: var(--vscode-sideBar-background);
      --fg: var(--vscode-sideBar-foreground);
      --muted: var(--vscode-descriptionForeground);
      --border: var(--vscode-panel-border, rgba(128,128,128,0.35));
      --input-bg: var(--vscode-input-background);
      --input-fg: var(--vscode-input-foreground);
      --btn-bg: var(--vscode-button-background);
      --btn-fg: var(--vscode-button-foreground);
      --btn2-bg: var(--vscode-button-secondaryBackground);
      --btn2-fg: var(--vscode-button-secondaryForeground);
      --accent: var(--vscode-focusBorder, #3794ff);
      --user-bg: var(--vscode-editor-selectionBackground, rgba(55,148,255,0.2));
      --sys-bg: var(--vscode-textBlockQuote-background, rgba(128,128,128,0.12));
    }
    * { box-sizing: border-box; }
    body { margin: 0; font-family: var(--vscode-font-family); font-size: var(--vscode-font-size); color: var(--fg); background: var(--bg); height: 100vh; display: flex; flex-direction: column; }
    header { padding: 8px 10px 6px; border-bottom: 1px solid var(--border); flex-shrink: 0; }
    header h1 { margin: 0 0 4px; font-size: 10px; font-weight: 600; letter-spacing: 0.05em; text-transform: uppercase; color: var(--muted); }
    .status-row { display: flex; align-items: center; gap: 6px; font-size: 11px; }
    .dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
    .dot.ok { background: #3fb950; } .dot.warn { background: #d29922; } .dot.err { background: #f85149; }
    .meta { margin-top: 4px; font-size: 10px; color: var(--muted); line-height: 1.35; word-break: break-word; }
    .estimate { font-size: 10px; color: var(--muted); margin-top: 4px; }
    .attachments { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 4px; }
    .chip { font-size: 9px; padding: 2px 5px; border-radius: 4px; border: 1px solid var(--border); color: var(--muted); }
    .tabs { display: flex; gap: 4px; padding: 6px 10px 0; border-bottom: 1px solid var(--border); }
    .tabs button { background: transparent; color: var(--muted); border: none; border-bottom: 2px solid transparent; padding: 4px 8px; font-size: 10px; cursor: pointer; }
    .tabs button.active { color: var(--fg); border-bottom-color: var(--accent); }
    .panel { flex: 1; overflow: hidden; display: none; flex-direction: column; }
    .panel.active { display: flex; }
    #messages, #timeline { flex: 1; overflow-y: auto; padding: 8px 10px; }
    #timeline .entry { font-size: 11px; padding: 6px 0; border-bottom: 1px solid var(--border); }
    #timeline .entry .t { color: var(--muted); font-size: 9px; }
    .msg { padding: 7px 9px; border-radius: 8px; line-height: 1.4; white-space: pre-wrap; word-break: break-word; font-size: 11px; margin-bottom: 8px; }
    .msg.user { background: var(--user-bg); border: 1px solid var(--border); }
    .msg.assistant { border: 1px solid var(--border); }
    .msg.system { background: var(--sys-bg); border-left: 3px solid var(--accent); color: var(--muted); font-size: 10px; }
    .msg .role { font-size: 9px; text-transform: uppercase; color: var(--muted); margin-bottom: 3px; }
    .recent { padding: 6px 10px; border-bottom: 1px solid var(--border); max-height: 72px; overflow-y: auto; }
    .recent button { display: block; width: 100%; text-align: left; margin: 2px 0; font-size: 10px; padding: 4px 6px; background: var(--btn2-bg); color: var(--btn2-fg); border: none; border-radius: 4px; cursor: pointer; }
    footer { border-top: 1px solid var(--border); padding: 6px 10px 8px; flex-shrink: 0; }
    .toolbar { display: flex; flex-wrap: wrap; gap: 4px; margin-bottom: 5px; }
    .toolbar button { padding: 3px 7px; font-size: 9px; cursor: pointer; border: none; border-radius: 4px; background: var(--btn-bg); color: var(--btn-fg); }
    .toolbar button.secondary { background: var(--btn2-bg); color: var(--btn2-fg); }
    .toolbar label { font-size: 9px; color: var(--muted); display: flex; align-items: center; gap: 4px; }
    textarea { width: 100%; min-height: 64px; max-height: 140px; resize: vertical; padding: 7px; border: 1px solid var(--border); border-radius: 6px; background: var(--input-bg); color: var(--input-fg); font-family: inherit; font-size: 11px; }
    .send-row { display: flex; gap: 5px; margin-top: 5px; align-items: center; }
    .send-row select { flex: 1; min-width: 0; padding: 4px; border-radius: 4px; border: 1px solid var(--border); background: var(--input-bg); color: var(--input-fg); font-size: 10px; }
    .error-banner { color: #f85149; font-size: 10px; margin-top: 4px; }
  </style>
</head>
<body>
  <header>
    <h1>AI Agent Bridge</h1>
    <div class="status-row"><span class="dot" id="statusDot"></span><span id="statusText">Connecting…</span></div>
    <div class="meta" id="workspaceMeta"></div>
    <div class="estimate" id="contextEstimate"></div>
    <div class="estimate" id="usageEstimate"></div>
    <div class="attachments" id="attachments"></div>
  </header>
  <div class="recent" id="recentSessions" hidden></div>
  <div class="tabs">
    <button type="button" class="active" data-tab="chat">Chat</button>
    <button type="button" data-tab="timeline">Timeline</button>
  </div>
  <section class="panel active" id="panelChat"><div id="messages" role="log" aria-live="polite"></div></section>
  <section class="panel" id="panelTimeline"><div id="timeline"></div></section>
  <footer>
    <div class="toolbar" id="toolbar"></div>
    <div class="send-row">
      <select id="useCaseSelect" title="Use case"></select>
      <select id="workflowSelect" title="Workflow"></select>
    </div>
    <textarea id="prompt" placeholder="Same-session follow-ups use /turns after the first run (Bridge POC)" rows="3"></textarea>
    <div class="send-row">
      <select id="agentSelect" title="Agent hint"></select>
      <button class="primary" id="sendBtn">Send</button>
    </div>
    <div class="toolbar" id="mcpToolbar"></div>
    <div class="error-banner" id="errorBanner" hidden></div>
  </footer>
  <script nonce="${nonce}">
    const vscode = acquireVsCodeApi();
    let state = null;
    let busy = false;
    const els = {
      statusDot: document.getElementById('statusDot'),
      statusText: document.getElementById('statusText'),
      workspaceMeta: document.getElementById('workspaceMeta'),
      contextEstimate: document.getElementById('contextEstimate'),
      usageEstimate: document.getElementById('usageEstimate'),
      useCaseSelect: document.getElementById('useCaseSelect'),
      workflowSelect: document.getElementById('workflowSelect'),
      mcpToolbar: document.getElementById('mcpToolbar'),
      attachments: document.getElementById('attachments'),
      messages: document.getElementById('messages'),
      timeline: document.getElementById('timeline'),
      recentSessions: document.getElementById('recentSessions'),
      toolbar: document.getElementById('toolbar'),
      prompt: document.getElementById('prompt'),
      sendBtn: document.getElementById('sendBtn'),
      agentSelect: document.getElementById('agentSelect'),
      errorBanner: document.getElementById('errorBanner'),
    };

    document.querySelectorAll('.tabs button').forEach(btn => {
      btn.addEventListener('click', () => {
        document.querySelectorAll('.tabs button').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        document.getElementById('panelChat').classList.toggle('active', btn.dataset.tab === 'chat');
        document.getElementById('panelTimeline').classList.toggle('active', btn.dataset.tab === 'timeline');
        vscode.postMessage({ type: 'setTab', tab: btn.dataset.tab });
      });
    });

    function setBusy(value) {
      busy = value;
      els.sendBtn.disabled = value || !state?.connected;
      els.prompt.disabled = value;
    }

    function statusClass(status) {
      if (state?.connected && status !== 'error') return 'ok';
      if (status === 'error') return 'err';
      return 'warn';
    }

    function renderAgents() {
      const current = state?.agentHint || 'unit-tests';
      const agents = (state?.agents || []).map(a => a.name);
      const options = agents.length ? agents : ['unit-tests', 'code-review', 'router-agent'];
      if (!options.includes(current)) options.unshift(current);
      els.agentSelect.innerHTML = options.map(a => '<option value="' + escapeHtml(a) + '">' + escapeHtml(a) + '</option>').join('');
      els.agentSelect.value = current;
    }

    function render() {
      if (!state) return;
      els.statusDot.className = 'dot ' + statusClass(state.status);
      els.statusText.textContent = state.statusDetail || state.status;
      const sys = state.systemStatus;
      const meta = [
        state.governanceUrl,
        state.workspaceLabel,
        state.branch ? 'branch: ' + state.branch : '',
        sys?.version ? 'v' + sys.version : '',
        state.sessionId ? 'session: ' + state.sessionId.slice(0, 18) + '…' : '',
      ].filter(Boolean).join(' · ');
      els.workspaceMeta.textContent = meta;
      els.contextEstimate.textContent = state.contextCharEstimate ? ('~' + state.contextCharEstimate.toLocaleString() + ' context chars (bounded)') : '';
      const u = state.sessionUsage;
      els.usageEstimate.textContent = u
        ? ('Usage: ' + u.totalTokens.toLocaleString() + ' tokens · $' + u.estimatedCostUsd.toFixed(4) + ' · MCP ' + u.mcpProxyCalls + ' · model ' + u.modelProxyCalls + (u.turnCount ? ' · turns ' + u.turnCount : ''))
        : '';
      renderGovernancePickers();
      els.attachments.innerHTML = (state.attachments || []).map(a => '<span class="chip">' + escapeHtml(a.label) + '</span>').join('');
      els.errorBanner.hidden = !state.error;
      els.errorBanner.textContent = state.error || '';
      renderAgents();
      const running = ['connecting','routing','running','awaiting_confirm'].includes(state.status);
      setBusy(running);
      els.messages.innerHTML = (state.messages || []).map(m => {
        const roleLabel = m.role === 'user' ? 'You' : m.role === 'assistant' ? 'Agent' : 'System';
        return '<div class="msg ' + m.role + '"><div class="role">' + roleLabel + '</div>' + escapeHtml(m.text) + '</div>';
      }).join('');
      els.messages.scrollTop = els.messages.scrollHeight;
      els.timeline.innerHTML = (state.timeline || []).map(e =>
        '<div class="entry"><div class="t">' + new Date(e.timestamp).toLocaleTimeString() + '</div><strong>' + escapeHtml(e.label) + '</strong>' + (e.detail ? '<div>' + escapeHtml(e.detail) + '</div>' : '') + '</div>'
      ).join('') || '<div class="entry">No timeline events yet.</div>';
      const tab = state.activeTab || 'chat';
      document.querySelectorAll('.tabs button').forEach(b => {
        b.classList.toggle('active', b.dataset.tab === tab);
      });
      document.getElementById('panelChat').classList.toggle('active', tab === 'chat');
      document.getElementById('panelTimeline').classList.toggle('active', tab === 'timeline');
      const recent = state.recentSessions || [];
      if (recent.length) {
        els.recentSessions.hidden = false;
        els.recentSessions.innerHTML = recent.slice(0, 5).map(r =>
          '<button type="button" data-session="' + escapeHtml(r.sessionId) + '">' + escapeHtml(r.summary.slice(0, 60)) + ' · ' + escapeHtml(r.specialist || '') + '</button>'
        ).join('');
        els.recentSessions.querySelectorAll('button').forEach(b => {
          b.addEventListener('click', () => vscode.postMessage({ type: 'resumeSession', sessionId: b.dataset.session }));
        });
      } else {
        els.recentSessions.hidden = true;
      }
      renderToolbar();
      renderMcpToolbar();
    }

    function renderGovernancePickers() {
      const ucs = state.useCases || [];
      const wfs = state.workflows || [];
      els.useCaseSelect.innerHTML = ucs.length
        ? ucs.map(u => '<option value="' + escapeHtml(u.id) + '">' + escapeHtml(u.label) + '</option>').join('')
        : '<option value="uc-exploratory">uc-exploratory</option>';
      els.workflowSelect.innerHTML = wfs.length
        ? wfs.map(w => '<option value="' + escapeHtml(w.id) + '">' + escapeHtml(w.label) + '</option>').join('')
        : '<option value="wf-unit-tests">wf-unit-tests</option>';
      if (state.useCaseId) els.useCaseSelect.value = state.useCaseId;
      if (state.workflowId) els.workflowSelect.value = state.workflowId;
    }

    function renderMcpToolbar() {
      const tools = state.mcpTools || [];
      if (!tools.length) {
        els.mcpToolbar.innerHTML = '<span style="font-size:9px;color:var(--muted)">MCP tools (gateway): start a session to load catalog</span>';
        return;
      }
      const items = tools.slice(0, 8).map(t =>
        btn(t.label, 'mcpTool', false, false, t.serverId + '::' + t.toolName)
      );
      els.mcpToolbar.innerHTML = '<span style="font-size:9px;color:var(--muted);width:100%">MCP (gateway-enforced):</span>' + items.join('');
      els.mcpToolbar.querySelectorAll('button').forEach(b => {
        b.addEventListener('click', () => {
          const parts = (b.dataset.payload || '').split('::');
          vscode.postMessage({ type: 'mcpTool', serverId: parts[0], toolName: parts[1] });
        });
      });
    }

    function renderToolbar() {
      const items = [];
      items.push(btn('Selection', 'attachSelection'));
      items.push(btn('File', 'attachFile'));
      items.push(btn('Files…', 'attachFiles'));
      items.push(btn('Terminal', 'attachTerminal'));
      items.push(btn('Search', 'attachSearch'));
      items.push(btn('New chat', 'newChat'));
      items.push(btn('Audit', 'showAudit', !state.sessionId));
      items.push('<label><input type="checkbox" id="autoConfirm"' + (state.autoConfirm ? ' checked' : '') + '> Auto-confirm</label>');
      if (state.status === 'awaiting_confirm' && state.pendingRun) {
        items.push(btn('Confirm ' + state.pendingRun.specialist, 'confirm'));
        items.push(btn('Cancel', 'cancel', false, true));
      }
      if (['running','routing','connecting'].includes(state.status)) items.push(btn('Abort', 'abort'));
      if (state.pendingToolRequest) {
        items.push(btn('Approve MCP', 'approveMcpTool'));
        items.push(btn('Dismiss tool', 'dismissTool', false, true));
      }
      if (state.status === 'patch_ready' && state.patches?.length) {
        items.push(btn('Review', 'reviewPatches'));
        items.push(btn('Apply all', 'applyPatches'));
        items.push(btn('Apply some', 'partialApply'));
        items.push(btn('Reject', 'rejectPatches', false, true));
      }
      els.toolbar.innerHTML = items.join('');
      els.toolbar.querySelectorAll('button').forEach(b => {
        b.addEventListener('click', () => vscode.postMessage({ type: b.dataset.action }));
      });
      const auto = document.getElementById('autoConfirm');
      if (auto) auto.addEventListener('change', () => vscode.postMessage({ type: 'setAutoConfirm', enabled: auto.checked }));
    }

    function btn(label, action, disabled, secondary, payload) {
      return '<button type="button" class="' + (secondary ? 'secondary' : '') + '" data-action="' + action + '"' + (payload ? ' data-payload="' + escapeHtml(payload) + '"' : '') + (disabled ? ' disabled' : '') + '>' + escapeHtml(label) + '</button>';
    }

    function escapeHtml(s) {
      return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
    }

    function send() {
      const text = els.prompt.value.trim();
      if (!text || busy) return;
      els.prompt.value = '';
      vscode.postMessage({ type: 'send', text });
      setBusy(true);
    }

    els.sendBtn.addEventListener('click', send);
    els.prompt.addEventListener('keydown', e => {
      if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); }
    });
    els.agentSelect.addEventListener('change', () => vscode.postMessage({ type: 'setAgent', agent: els.agentSelect.value }));
    els.useCaseSelect.addEventListener('change', () => vscode.postMessage({ type: 'setUseCase', useCaseId: els.useCaseSelect.value }));
    els.workflowSelect.addEventListener('change', () => vscode.postMessage({ type: 'setWorkflow', workflowId: els.workflowSelect.value }));

    window.addEventListener('message', e => {
      if (e.data?.type === 'state') { state = e.data.state; render(); }
    });

    vscode.postMessage({ type: 'ready' });
  </script>
</body>
</html>`;
}