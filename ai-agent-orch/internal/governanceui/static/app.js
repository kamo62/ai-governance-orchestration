(function () {
  const state = {
    baseUrl: localStorage.getItem("ai_orch_base_url") || window.location.origin,
    devToken: localStorage.getItem("ai_orch_dev_token") || "",
    adminToken: localStorage.getItem("ai_orch_admin_token") || "",
    actorSubject: localStorage.getItem("ai_orch_actor_subject") || "",
    lastReady: null,
    lastStatus: null,
    lastMetrics: null,
    lastAgents: [],
    lastSessions: [],
    selectedSessionID: "",
  };

  const el = {
    baseUrl: document.getElementById("baseUrl"),
    adminToken: document.getElementById("adminToken"),
    saveConnection: document.getElementById("saveConnection"),
    refresh: document.getElementById("refresh"),
    statusLine: document.getElementById("statusLine"),
    readinessBadge: document.getElementById("readinessBadge"),
    readinessSummary: document.getElementById("readinessSummary"),
    readinessList: document.getElementById("readinessList"),
    modelBackend: document.getElementById("modelBackend"),
    agentCount: document.getElementById("agentCount"),
    sessionCount: document.getElementById("sessionCount"),
    evidenceCount: document.getElementById("evidenceCount"),
    cacheRate: document.getElementById("cacheRate"),
    patchCount: document.getElementById("patchCount"),
    versionBadge: document.getElementById("versionBadge"),
    gatewayList: document.getElementById("gatewayList"),
    backendCommandList: document.getElementById("backendCommandList"),
    copilotEnrollments: document.getElementById("copilotEnrollments"),
    sessionList: document.getElementById("sessionList"),
    sessionBadge: document.getElementById("sessionBadge"),
    auditSessionTitle: document.getElementById("auditSessionTitle"),
    auditEventBadge: document.getElementById("auditEventBadge"),
    auditSummary: document.getElementById("auditSummary"),
    agentList: document.getElementById("agentList"),
    agentBadge: document.getElementById("agentBadge"),
    maturityList: document.getElementById("maturityList"),
    maturityBadge: document.getElementById("maturityBadge"),
    evidenceList: document.getElementById("evidenceList"),
    evidenceBadge: document.getElementById("evidenceBadge"),
    useCaseForm: document.getElementById("useCaseForm"),
    workflowForm: document.getElementById("workflowForm"),
    auditForm: document.getElementById("auditForm"),
    auditList: document.getElementById("auditList"),
    activityLog: document.getElementById("activityLog"),
    clearLog: document.getElementById("clearLog"),
    envBadge: document.getElementById("envBadge"),
    autoRefresh: document.getElementById("autoRefresh"),
    killSwitchBadge: document.getElementById("killSwitchBadge"),
    killSwitchGlobalOn: document.getElementById("killSwitchGlobalOn"),
    killSwitchGlobalOff: document.getElementById("killSwitchGlobalOff"),
    killSwitchAgentForm: document.getElementById("killSwitchAgentForm"),
    killSwitchList: document.getElementById("killSwitchList"),
  };

  el.baseUrl.value = state.baseUrl;
  el.adminToken.value = state.adminToken;

  function baseUrl() {
    return (state.baseUrl || window.location.origin).replace(/\/+$/, "");
  }

  function devHeaders() {
    const headers = { "Content-Type": "application/json" };
    // The admin token is a superset credential server-side, so an
    // operator-only console works without any dev token.
    if (state.devToken) headers.Authorization = "Bearer " + state.devToken;
    else if (state.adminToken) headers.Authorization = "Bearer " + state.adminToken;
    // Sessions, audit, and Copilot enrollment are actor-scoped. Without this
    // header a dev-token UI views the empty "local-dev" actor instead of the
    // operator's own governed activity.
    if (state.actorSubject) headers["X-AI-Orch-Local-Identity"] = state.actorSubject;
    return headers;
  }

  function adminHeaders() {
    const headers = { "Content-Type": "application/json" };
    if (state.adminToken) headers.Authorization = "Bearer " + state.adminToken;
    return headers;
  }

  async function api(path, options) {
    const response = await fetch(baseUrl() + path, options || {});
    const text = await response.text();
    let data = {};
    if (text) {
      try {
        data = JSON.parse(text);
      } catch (_err) {
        data = { raw: text };
      }
    }
    if (!response.ok) {
      throw new Error(data.error || response.statusText || "request failed");
    }
    return data;
  }

  function setStatus(text, mode) {
    el.statusLine.className = "status-line " + (mode || "");
    el.statusLine.querySelector("span:last-child").textContent = text;
  }

  function log(message) {
    const item = document.createElement("li");
    item.textContent = new Date().toLocaleTimeString() + " " + message;
    el.activityLog.prepend(item);
    while (el.activityLog.children.length > 16) {
      el.activityLog.removeChild(el.activityLog.lastChild);
    }
  }

  function hasDevToken() {
    return state.devToken.trim().length > 0 || hasAdminToken();
  }

  function hasAdminToken() {
    return state.adminToken.trim().length > 0;
  }

  function hasAnyAuth() {
    return hasDevToken() || hasAdminToken();
  }

  function emptyList(target, message) {
    target.innerHTML = "";
    const item = document.createElement("div");
    item.className = "item";
    item.innerHTML = "<strong>" + escapeHtml(message) + "</strong>";
    target.appendChild(item);
  }

  function escapeHtml(value) {
    return String(value || "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#039;");
  }

  function metricNumber(name) {
    return Number((state.lastMetrics && state.lastMetrics[name]) || 0);
  }

  function renderReadiness() {
    const patchDecisions = metricNumber("patches_applied") + metricNumber("patches_rejected");
    const evidenceRecords = Number(el.evidenceBadge.textContent || 0);
    const checks = [
      {
        label: "Service",
        ok: Boolean(state.lastReady && state.lastReady.service),
        value: state.lastReady && state.lastReady.service ? state.lastReady.service : "pending",
      },
      {
        label: "Operator access",
        ok: hasAnyAuth(),
        value: hasAdminToken() ? "org-wide" : hasDevToken() ? "actor-scoped" : "token required",
      },
      {
        label: "Gateway",
        ok: Boolean(state.lastStatus && state.lastStatus.model_backend),
        value: state.lastStatus && state.lastStatus.model_backend ? state.lastStatus.model_backend : "pending",
      },
      {
        label: "Runtime",
        ok: Boolean(state.lastStatus && state.lastStatus.runtime_gateway_enabled),
        value: state.lastStatus
          ? state.lastStatus.runtime_gateway_enabled ? "enabled" : "disabled"
          : "pending",
      },
      {
        label: "Agents",
        ok: state.lastAgents.length > 0,
        value: state.lastAgents.length > 0 ? String(state.lastAgents.length) : "pending",
      },
      {
        label: "Evidence",
        ok: evidenceRecords > 0 || patchDecisions > 0,
        value: evidenceRecords + " records / " + patchDecisions + " patch decisions",
      },
    ];
    const passed = checks.filter((check) => check.ok).length;
    const allPassed = passed === checks.length;
    const authPending = !hasDevToken();
    el.readinessBadge.textContent = allPassed ? "Governed" : authPending ? "Auth pending" : "Needs evidence";
    el.readinessSummary.textContent = passed + "/" + checks.length + " checks clear";
    el.readinessList.innerHTML = "";
    checks.forEach((check) => {
      const item = document.createElement("div");
      item.className = "readiness-item" + (check.ok ? " ok" : authPending && check.label !== "Service" ? "" : " bad");
      item.innerHTML = [
        "<span>" + escapeHtml(check.label) + "</span>",
        "<strong>" + escapeHtml(check.value) + "</strong>",
      ].join("");
      el.readinessList.appendChild(item);
    });
  }

  function renderProtectedPending() {
    state.lastStatus = null;
    state.lastAgents = [];
    state.lastSessions = [];
    state.selectedSessionID = "";
    el.modelBackend.textContent = "-";
    el.versionBadge.textContent = "-";
    el.agentCount.textContent = "-";
    el.agentBadge.textContent = "0";
    el.evidenceCount.textContent = "-";
    emptyList(el.gatewayList, "Operator token required");
    emptyList(el.agentList, "Operator token required");
    renderSessions([]);
    renderAuditTrail({ session_id: "", events: [] });
    renderRecords(el.evidenceList, el.evidenceBadge, [], ["evidence_type", "description"]);
    renderRecords(el.maturityList, el.maturityBadge, [], ["workflow_id", "use_case_id", "session_id"]);
    renderReadiness();
  }

  function requireDevToken(action) {
    if (hasDevToken()) {
      return true;
    }
    setStatus("Token required", "warn");
    log(action + " requires operator token");
    renderProtectedPending();
    return false;
  }

  function renderEnvironment(status) {
    const env = (status && status.environment) || "local";
    el.envBadge.textContent = env;
    el.envBadge.className = "badge env-badge" + (env === "production" ? " prod" : "");
    document.title = (env === "production" ? "[PROD] " : "") + "AI Orch Governance";
  }

  function renderGatewayStatus(status) {
    state.lastStatus = status || null;
    renderEnvironment(status);
    el.modelBackend.textContent = status.model_backend || "-";
    el.versionBadge.textContent = status.version || "-";
    el.gatewayList.innerHTML = "";
    const gateways = Array.isArray(status.gateways) ? status.gateways : [];
    if (!gateways.length) {
      emptyList(el.gatewayList, "No gateway options returned");
      return;
    }
    gateways.forEach((gateway) => {
      const card = document.createElement("article");
      const active = gateway.id === status.model_backend;
      card.className = "gateway-card" + (active ? " active" : "");
      card.innerHTML = [
        "<strong>" + escapeHtml(gateway.label || gateway.id) + "</strong>",
        "<p>" + escapeHtml(gateway.mode || "-") + (gateway.default ? " / default" : "") + "</p>",
        "<p>" + escapeHtml(gateway.compose_file || "docker-compose.yml") + "</p>",
      ].join("");
      el.gatewayList.appendChild(card);
    });
    loadBackends().catch((err) => log("backend commands unavailable: " + err.message));
    loadCopilotFleet().catch(() => {});
  }

  async function loadBackends() {
    if (!requireDevToken("Backend status")) return;
    const data = await api("/v1/backends", { headers: devHeaders() });
    el.backendCommandList.innerHTML = "";
    const commands = data.commands || {};
    Object.keys(commands).sort().forEach((key) => {
      const item = document.createElement("div");
      item.className = "command-item";
      item.innerHTML = [
        "<strong>" + escapeHtml(key) + "</strong>",
        "<code>" + escapeHtml(commands[key]) + "</code>",
        "<span><button type=\"button\" data-backend=\"" + escapeHtml(key) + "\" data-action=\"up\">Start</button> ",
        "<button type=\"button\" data-backend=\"" + escapeHtml(key) + "\" data-action=\"down\">Stop</button></span>",
      ].join("");
      el.backendCommandList.appendChild(item);
    });
    el.backendCommandList.querySelectorAll("button[data-backend]").forEach((button) => {
      button.addEventListener("click", () => runBackendAction(button.dataset.backend, button.dataset.action));
    });
  }

  async function runBackendAction(backend, action) {
    if (!state.adminToken.trim()) {
      setStatus("Admin token required", "warn");
      log("backend " + action + " requires admin token");
      return;
    }
    try {
      const result = await api("/v1/backends", {
        method: "POST",
        headers: adminHeaders(),
        body: JSON.stringify({ backend, action }),
      });
      log("backend " + action + " completed: " + backend);
      refreshAll();
    } catch (err) {
      log("backend " + action + " failed: " + err.message);
    }
  }

  // Enrollment is developer self-service from each machine; the console only
  // observes fleet adoption via the enrollment count.
  async function loadCopilotFleet() {
    if (!hasDevToken()) return;
    try {
      const status = await api("/v1/copilot/status", { headers: devHeaders() });
      el.copilotEnrollments.textContent = String(status.enrollments ?? 0);
    } catch (_err) {
      el.copilotEnrollments.textContent = "-";
    }
  }


  async function loadKillSwitches() {
    if (!state.adminToken.trim()) {
      el.killSwitchBadge.textContent = "-";
      emptyList(el.killSwitchList, "Admin token required");
      return;
    }
    try {
      const data = await api("/v1/admin/killswitch", { headers: adminHeaders() });
      const switches = data.killswitches || {};
      const entries = [];
      Object.keys(switches).sort().forEach((scope) => {
        (switches[scope] || []).forEach((id) => entries.push({ scope, id }));
      });
      el.killSwitchBadge.textContent = String(entries.length);
      el.killSwitchList.innerHTML = "";
      if (!entries.length) {
        emptyList(el.killSwitchList, "No active kill switches");
        return;
      }
      entries.forEach((entry) => {
        const item = document.createElement("div");
        item.className = "item kill-switch-item";
        item.innerHTML = [
          "<strong>" + escapeHtml(entry.scope + " / " + entry.id) + "</strong>",
          "<span><button type=\"button\" data-scope=\"" + escapeHtml(entry.scope) + "\" data-id=\"" + escapeHtml(entry.id) + "\">Unblock</button></span>",
        ].join("");
        el.killSwitchList.appendChild(item);
      });
      el.killSwitchList.querySelectorAll("button[data-scope]").forEach((button) => {
        button.addEventListener("click", () => toggleKillSwitch(button.dataset.scope, button.dataset.id, false));
      });
    } catch (err) {
      el.killSwitchBadge.textContent = "-";
      emptyList(el.killSwitchList, "Kill switch status unavailable: " + err.message);
    }
  }

  async function toggleKillSwitch(scope, id, enable) {
    if (!state.adminToken.trim()) {
      setStatus("Admin token required", "warn");
      log("kill switch change requires admin token");
      return;
    }
    try {
      await api("/v1/admin/killswitch/" + encodeURIComponent(scope) + "/" + encodeURIComponent(id), {
        method: enable ? "POST" : "DELETE",
        headers: adminHeaders(),
      });
      log("kill switch " + (enable ? "enabled" : "disabled") + ": " + scope + "/" + id);
      loadKillSwitches();
    } catch (err) {
      log("kill switch change failed: " + err.message);
    }
  }

  function renderAgents(agents) {
    const list = Array.isArray(agents) ? agents : [];
    state.lastAgents = list;
    el.agentCount.textContent = String(list.length);
    el.agentBadge.textContent = String(list.length);
    el.agentList.innerHTML = "";
    if (!list.length) {
      emptyList(el.agentList, "No agents");
      return;
    }
    list.slice(0, 12).forEach((agent) => {
      const item = document.createElement("div");
      item.className = "item";
      item.innerHTML = [
        "<strong>" + escapeHtml(agent.name || agent.id || "agent") + "</strong>",
        "<p>" + escapeHtml(agent.category || agent.path || "") + "</p>",
      ].join("");
      el.agentList.appendChild(item);
    });
  }

  function renderMetrics(metrics) {
    state.lastMetrics = metrics || {};
    const applied = Number(metrics.patches_applied || 0);
    const rejected = Number(metrics.patches_rejected || 0);
    const hits = Number(metrics.cache_hits || 0);
    const misses = Number(metrics.cache_misses || 0);
    const rate = hits + misses === 0 ? "-" : Math.round((hits / (hits + misses)) * 100) + "%";
    el.cacheRate.textContent = rate;
    el.patchCount.textContent = String(applied + rejected);
  }

  function renderSessions(sessions) {
  const list = Array.isArray(sessions) ? sessions : [];
  state.lastSessions = list;
  el.sessionCount.textContent = String(list.length);
  el.sessionBadge.textContent = String(list.length);
    el.sessionList.innerHTML = "";
    if (!list.length) {
      emptyList(el.sessionList, "No governed sessions");
      return;
    }
  const table = document.createElement("table");
  table.className = "ledger-table";
  table.innerHTML = [
    "<thead><tr>",
    "<th>Time</th>",
    "<th>Actor / Source</th>",
    "<th>Work item</th>",
    "<th>Agent</th>",
    "<th>Model / Backend</th>",
    "<th>Status</th>",
    "<th>Tokens</th>",
    "<th>Cost</th>",
    "<th>Trust</th>",
    "</tr></thead><tbody></tbody>",
  ].join("");
  const tbody = table.querySelector("tbody");
  list.slice(0, 50).forEach((session) => {
    const summary = session.usage_summary || {};
    const row = document.createElement("tr");
    row.className = session.session_id === state.selectedSessionID ? "selected" : "";
    row.innerHTML = [
      cell(formatShortDate(session.latest_event_at || session.created_at), session.latest_event_type || "created"),
      cell(session.actor_subject || "-", session.source_system || session.actor_hint || "-"),
      cell(session.work_item_id || "-", [session.repo_url, session.branch].filter(Boolean).join(" / ") || session.use_case_id || "-"),
      cell(session.routed_agent || session.agent || "-", [session.parent_session_id ? "child of " + session.parent_session_id : "", formatModeLabels(session)].filter(Boolean).join(" / ") || "-"),
      cell(summary.model_alias || "-", [summary.model_resolved, summary.gateway_backend].filter(Boolean).join(" / ") || "-"),
      cell(session.status || "-", session.patch_state ? "patch " + session.patch_state : (session.tool_call_count ? session.tool_call_count + " tool calls" : "-")),
      cell(String(summary.total_tokens || 0), (summary.prompt_tokens || 0) + " in / " + (summary.completion_tokens || 0) + " out"),
      cell(formatCostValue(summary.estimated_cost_usd), costSourceLabel(summary.cost_source) || "unpriced"),
      cell(session.trust_level || "-", session.enforcement_mode || "-"),
    ].join("");
    row.addEventListener("click", () => loadAuditTrail(session.session_id));
    tbody.appendChild(row);
  });
  el.sessionList.appendChild(table);
      }

      function cell(primary, secondary) {
  return "<td><strong>" + escapeHtml(primary || "-") + "</strong><span>" + escapeHtml(secondary || "") + "</span></td>";
      }

  function renderAuditTrail(audit) {
	const sessionID = audit && audit.session_id ? audit.session_id : "";
	const events = audit && Array.isArray(audit.events) ? audit.events : [];
	const usage = audit && audit.usage_summary ? audit.usage_summary : null;
	el.auditEventBadge.textContent = String(events.length);
	el.auditSessionTitle.textContent = sessionID ? "Session Audit Trail" : "Session Audit Trail";
	el.auditSummary.textContent = sessionID
	  ? sessionID + " - " + events.length + " event" + (events.length === 1 ? "" : "s") + (usage ? " - " + formatTokenSummary(usage) + " - " + formatCostSummary(usage) : "")
	  : "Select a session to inspect its governed events.";
    el.auditList.innerHTML = "";
    if (!events.length) {
      emptyList(el.auditList, sessionID ? "No audit events for session" : "No session selected");
      return;
    }
    events.slice().reverse().forEach((event) => {
      const item = document.createElement("article");
      item.className = "audit-event";
	  const detail = [
	    event.agent ? "agent " + event.agent : "",
	    event.parent_session_id ? "parent " + event.parent_session_id : "",
	    event.model_alias ? "model " + event.model_alias : "",
	    event.model_resolved ? "resolved " + event.model_resolved : "",
	    event.gateway_backend ? "gateway " + event.gateway_backend : "",
	    formatToolCallSummary(event),
	    formatEventUsage(event),
	    event.reason ? "reason " + event.reason : "",
	  ].filter(Boolean).join(" | ");
      const trust = [event.trust_level, event.enforcement_mode].filter(Boolean).join(" / ");
      item.innerHTML = [
        "<header><strong>" + escapeHtml(event.event_type || "event") + "</strong><time>" + escapeHtml(formatDate(event.recorded_at)) + "</time></header>",
        "<p>" + escapeHtml(detail || event.correlation_subject || event.actor || "") + "</p>",
        "<footer>" + escapeHtml([event.actor, trust, event.event_id].filter(Boolean).join(" - ")) + "</footer>",
      ].join("");
      el.auditList.appendChild(item);
    });
  }

  async function loadAuditTrail(sessionID) {
    if (!sessionID) {
      renderAuditTrail({ session_id: "", events: [] });
      return;
    }
    state.selectedSessionID = sessionID;
    renderSessions(state.lastSessions);
    const orgWide = hasAdminToken();
    const audit = orgWide
      ? await api("/v1/admin/audit/sessions/" + encodeURIComponent(sessionID), { headers: adminHeaders() })
      : await api("/v1/audit/sessions/" + encodeURIComponent(sessionID), { headers: devHeaders() });
    renderAuditTrail(audit);
    log("audit loaded: " + sessionID);
  }

      function formatDate(value) {
	if (!value) return "";
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return String(value);
	return date.toLocaleString();
      }

      function formatShortDate(value) {
	if (!value) return "";
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return String(value);
	return date.toLocaleString([], { month: "short", day: "2-digit", hour: "2-digit", minute: "2-digit" });
      }

      function formatModeLabels(session) {
	return [
	  permissionLabel(session.permission_mode),
	  approvalLabel(session.approval_mode),
	  workspaceLabel(session.workspace_mode),
	].filter(Boolean).join(" / ");
      }

      function permissionLabel(value) {
	switch (value) {
	  case "read_only":
	    return "Read-only";
	  case "reviewed":
	    return "Review before changes";
	  case "auto_apply":
	    return "Auto-apply allowed";
	  case "full_access":
	    return "Full local access";
	  default:
	    return humanLabel(value);
	}
      }

      function approvalLabel(value) {
	switch (value) {
	  case "manual":
	    return "Manual approval";
	  case "yolo":
	    return "No approval gate";
	  default:
	    return humanLabel(value);
	}
      }

      function workspaceLabel(value) {
	switch (value) {
	  case "local":
	    return "Local workspace";
	  case "remote":
	    return "Remote workspace";
	  default:
	    return humanLabel(value);
	}
      }

      function humanLabel(value) {
	if (!value) return "";
	return String(value).replaceAll("_", " ").replace(/\b\w/g, (char) => char.toUpperCase());
      }

      function formatModelSummary(summary) {
	const alias = summary && summary.model_alias ? summary.model_alias : "";
	const resolved = summary && summary.model_resolved ? summary.model_resolved : "";
	const backend = summary && summary.gateway_backend ? " via " + summary.gateway_backend : "";
	if (!alias && !resolved) {
	  return "Model not reported";
	}
	if (alias && resolved && alias !== resolved) {
	  return "Model " + alias + " -> " + resolved + backend;
	}
	return "Model " + (alias || resolved) + backend;
      }

      function formatTokenSummary(summary) {
	const prompt = Number((summary && summary.prompt_tokens) || 0);
	const completion = Number((summary && summary.completion_tokens) || 0);
	const total = Number((summary && summary.total_tokens) || prompt + completion || 0);
	if (!prompt && !completion && !total) {
	  return "Tokens not reported";
	}
	return "Tokens " + prompt + " in / " + completion + " out / " + total + " total";
      }

      function formatCostSummary(summary) {
	const cost = Number((summary && summary.estimated_cost_usd) || 0);
	const source = costSourceLabel(summary && summary.cost_source);
	if (cost > 0) {
	  return "Cost " + formatCost(cost) + (source ? " (" + source + ")" : "");
	}
	const total = Number((summary && summary.total_tokens) || 0);
	if (total > 0) {
	  return "Cost pending" + (source ? " (" + source + ")" : "");
	}
	return "Cost unavailable";
      }

      function formatCost(value) {
	if (value < 0.01) {
	  return "$" + value.toFixed(6);
	}
	return "$" + value.toFixed(2);
      }

      function formatCostValue(value) {
	const cost = Number(value || 0);
	return cost > 0 ? formatCost(cost) : "$0";
      }

      function costSourceLabel(value) {
	switch (value) {
	  case "provider_reported":
	    return "provider";
	  case "pricing_table":
	    return "pricing table";
	  case "mixed":
	    return "mixed";
	  case "unavailable":
	    return "not priced";
	  default:
	    return "";
	}
      }

      function formatEventUsage(event) {
	const usage = event && event.token_usage ? event.token_usage : null;
	if (!usage) {
	  return "";
	}
	const tokens = formatTokenSummary({
	  prompt_tokens: usage.prompt_tokens,
	  completion_tokens: usage.completion_tokens,
	  total_tokens: usage.total_tokens,
	});
	const cost = Number(usage.cost_usd || 0);
	return cost > 0 ? tokens + " / cost " + formatCost(cost) : tokens;
      }

      function formatToolCallSummary(event) {
	const count = Number((event && event.tool_call_count) || 0);
	const names = Array.isArray(event && event.tool_call_names)
	  ? event.tool_call_names.filter(Boolean).slice(0, 6)
	  : [];
	if (!count && !names.length) {
	  return "";
	}
	const label = count === 1 ? "1 tool call" : count + " tool calls";
	return names.length ? label + " (" + names.join(", ") + ")" : label;
      }

      function renderRecords(target, badge, records, fields) {
    const list = Array.isArray(records) ? records : [];
    badge.textContent = String(list.length);
    target.innerHTML = "";
    if (!list.length) {
      emptyList(target, "No records");
      return;
    }
    list.slice(0, 10).forEach((record) => {
      const title = fields.map((field) => record[field]).find(Boolean) || "record";
      const detail = record.session_id || record.workflow_id || record.use_case_id || record.recorded_at || "";
      const item = document.createElement("div");
      item.className = "item";
      item.innerHTML = [
        "<strong>" + escapeHtml(title) + "</strong>",
        "<p>" + escapeHtml(detail) + "</p>",
      ].join("");
      target.appendChild(item);
    });
  }

  async function loadOrgSessions() {
    try {
      // With an admin token the console shows every actor's sessions
      // (governance oversight); otherwise it shows the configured actor's own.
      const orgWide = hasAdminToken();
      const response = orgWide
        ? await api("/v1/admin/sessions?limit=20", { headers: adminHeaders() })
        : await api("/v1/sessions?limit=20", { headers: devHeaders() });
      const sessions = response.sessions || [];
      const selectedStillExists = sessions.some((session) => session.session_id === state.selectedSessionID);
      renderSessions(sessions);
      const nextSessionID = selectedStillExists ? state.selectedSessionID : (sessions[0] && sessions[0].session_id) || "";
      if (nextSessionID) {
        await loadAuditTrail(nextSessionID);
      } else {
        state.selectedSessionID = "";
        renderAuditTrail({ session_id: "", events: [] });
      }
    } catch (err) {
      renderSessions([]);
      renderAuditTrail({ session_id: "", events: [] });
      log("sessions failed: " + err.message);
    }
  }

  async function loadOrgEvidenceAndReporting() {
    if (hasAdminToken()) {
      try {
        const evidence = await api("/v1/admin/evidence", { headers: adminHeaders() });
        const records = evidence.evidence || [];
        el.evidenceCount.textContent = String(records.length);
        renderRecords(el.evidenceList, el.evidenceBadge, records, ["evidence_type", "description"]);
      } catch (err) {
        el.evidenceCount.textContent = "-";
        renderRecords(el.evidenceList, el.evidenceBadge, [], ["evidence_type", "description"]);
        log("org evidence failed: " + err.message);
      }
      try {
        const maturity = await api("/v1/admin/reporting/maturity-governance", { headers: adminHeaders() });
        const records = maturity.exports || [];
        renderRecords(el.maturityList, el.maturityBadge, records, ["workflow_id", "use_case_id", "session_id"]);
      } catch (err) {
        renderRecords(el.maturityList, el.maturityBadge, [], ["workflow_id", "use_case_id", "session_id"]);
        log("org maturity export failed: " + err.message);
      }
      try {
        const cache = await api("/v1/admin/cache-outcomes", { headers: adminHeaders() });
        const outcomes = cache.outcomes || [];
        renderCacheRate(outcomes);
      } catch (err) {
        log("org cache outcomes failed: " + err.message);
      }
      return;
    }

    try {
      const evidence = await api("/v1/evidence", { headers: devHeaders() });
      const records = evidence.evidence || evidence.records || evidence.items || [];
      el.evidenceCount.textContent = String(records.length);
      renderRecords(el.evidenceList, el.evidenceBadge, records, ["evidence_type", "description"]);
    } catch (err) {
      el.evidenceCount.textContent = "-";
      renderRecords(el.evidenceList, el.evidenceBadge, [], ["evidence_type", "description"]);
      log("evidence failed: " + err.message);
    }

    try {
      const maturity = await api("/v1/reporting/maturity-governance", { headers: devHeaders() });
      const records = maturity.exports || maturity.records || maturity.items || [];
      renderRecords(el.maturityList, el.maturityBadge, records, ["workflow_id", "use_case_id", "session_id"]);
    } catch (err) {
      renderRecords(el.maturityList, el.maturityBadge, [], ["workflow_id", "use_case_id", "session_id"]);
      log("maturity export failed: " + err.message);
    }
  }

  function renderCacheRate(outcomes) {
    const list = Array.isArray(outcomes) ? outcomes : [];
    if (!list.length) {
      return;
    }
    const hits = list.filter((outcome) => Boolean(outcome.hit)).length;
    el.cacheRate.textContent = Math.round((hits / list.length) * 100) + "%";
  }

  async function refreshAll() {
    setStatus("Refreshing", "");
    try {
      const ready = await api("/readyz");
      state.lastReady = ready;
      setStatus(ready.service || "Ready", "ok");
    } catch (err) {
      state.lastReady = null;
      setStatus("Health failed", "bad");
      log("readiness failed: " + err.message);
    }

    try {
      const metrics = await api("/metrics");
      renderMetrics(metrics);
    } catch (err) {
      log("metrics failed: " + err.message);
    }

    if (!hasAnyAuth()) {
      setStatus(state.lastReady ? "Token required" : "Health failed", state.lastReady ? "warn" : "bad");
      renderProtectedPending();
      return;
    }

    try {
      const status = await api("/v1/system/status", { headers: devHeaders() });
      renderGatewayStatus(status);
    } catch (err) {
      state.lastStatus = null;
      el.modelBackend.textContent = "-";
      el.versionBadge.textContent = "-";
      emptyList(el.gatewayList, "System status unavailable");
      log("system status failed: " + err.message);
    }

    try {
      const agents = await api("/v1/agents", { headers: devHeaders() });
      renderAgents(agents.agents);
    } catch (err) {
      state.lastAgents = [];
      el.agentCount.textContent = "-";
      el.agentBadge.textContent = "0";
      emptyList(el.agentList, "Agents unavailable");
      log("agents failed: " + err.message);
    }

    await loadOrgEvidenceAndReporting();

    await loadOrgSessions();

    loadKillSwitches();
    renderReadiness();
  }

  function formObject(form) {
    const data = new FormData(form);
    const result = {};
    data.forEach((value, key) => {
      result[key] = String(value).trim();
    });
    return result;
  }

  el.saveConnection.addEventListener("click", () => {
    state.baseUrl = el.baseUrl.value.trim() || window.location.origin;
    state.adminToken = el.adminToken.value.trim();
    localStorage.setItem("ai_orch_base_url", state.baseUrl);
    localStorage.setItem("ai_orch_dev_token", state.devToken);
    localStorage.setItem("ai_orch_admin_token", state.adminToken);
    localStorage.setItem("ai_orch_actor_subject", state.actorSubject);
    log("connection saved");
    refreshAll();
  });

  el.refresh.addEventListener("click", refreshAll);
  el.clearLog.addEventListener("click", () => {
    el.activityLog.innerHTML = "";
  });

  if (el.killSwitchGlobalOn) {
    el.killSwitchGlobalOn.addEventListener("click", () => toggleKillSwitch("global", "sessions", true));
  }
  if (el.killSwitchGlobalOff) {
    el.killSwitchGlobalOff.addEventListener("click", () => toggleKillSwitch("global", "sessions", false));
  }
  if (el.killSwitchAgentForm) {
    el.killSwitchAgentForm.addEventListener("submit", (event) => {
      event.preventDefault();
      const agent = (new FormData(el.killSwitchAgentForm).get("agent") || "").toString().trim();
      if (!agent) return;
      const action = event.submitter && event.submitter.dataset.action === "unblock" ? false : true;
      toggleKillSwitch("agent", agent, action);
      el.killSwitchAgentForm.reset();
    });
  }

  // Auto-refresh keeps the dashboard live without manual clicks. Paused via
  // the toggle so an operator can inspect a moment in time.
  setInterval(() => {
    if (el.autoRefresh && el.autoRefresh.checked) {
      refreshAll();
    }
  }, 15000);

  // Hash router: each sidebar entry maps to a page section. All sections stay
  // in the DOM so render targets always exist; routing only toggles classes.
  const pageTitles = {
    overview: "Overview",
    sessions: "Activity Ledger",
    gateways: "Gateways",
    governance: "Risk Controls",
    catalog: "Evidence & Reporting",
    settings: "Settings",
  };

  function currentPage() {
    const raw = (window.location.hash || "").replace(/^#\/?/, "").trim();
    return pageTitles[raw] ? raw : "sessions";
  }

  function renderRoute() {
    const page = currentPage();
    document.querySelectorAll(".page").forEach((section) => {
      section.classList.toggle("active", section.dataset.page === page);
    });
    document.querySelectorAll("[data-page-link]").forEach((link) => {
      link.classList.toggle("active", link.dataset.pageLink === page);
    });
    const title = document.getElementById("pageTitle");
    if (title) title.textContent = pageTitles[page];
  }

  // Evidence exports: fetch with the admin credential and hand the browser a
  // blob download, since a bare link cannot carry the Authorization header.
  async function downloadExport(path, filename) {
    if (!hasAdminToken()) {
      setStatus("Admin token required", "warn");
      log("export requires admin token");
      return;
    }
    try {
      const response = await fetch(baseUrl() + path, { headers: adminHeaders() });
      if (!response.ok) throw new Error("HTTP " + response.status);
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
      log("export downloaded: " + filename);
    } catch (err) {
      log("export failed: " + err.message);
    }
  }

  const exportStamp = () => new Date().toISOString().replace(/[:.]/g, "-").slice(0, 19);
  const exportCsvButton = document.getElementById("exportCsv");
  const exportJsonButton = document.getElementById("exportJson");
  const exportTrailButton = document.getElementById("exportTrail");
  if (exportCsvButton) {
    exportCsvButton.addEventListener("click", () => downloadExport("/v1/admin/sessions/export?format=csv", "ai-orch-sessions-" + exportStamp() + ".csv"));
  }
  if (exportJsonButton) {
    exportJsonButton.addEventListener("click", () => downloadExport("/v1/admin/sessions/export?format=json", "ai-orch-sessions-" + exportStamp() + ".json"));
  }
  if (exportTrailButton) {
    exportTrailButton.addEventListener("click", () => {
      if (!state.selectedSessionID) {
        log("select a session before downloading its trail");
        return;
      }
      downloadExport("/v1/admin/audit/sessions/" + encodeURIComponent(state.selectedSessionID), "ai-orch-audit-" + state.selectedSessionID + ".json");
    });
  }

  window.addEventListener("hashchange", renderRoute);
  renderRoute();

  el.useCaseForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (!requireDevToken("use case registration")) {
      return;
    }
    const payload = formObject(el.useCaseForm);
    try {
      await api("/v1/use-cases", {
        method: "POST",
        headers: devHeaders(),
        body: JSON.stringify(payload),
      });
      el.useCaseForm.reset();
      log("use case registered: " + payload.id);
      refreshAll();
    } catch (err) {
      log("use case failed: " + err.message);
    }
  });

  el.workflowForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (!requireDevToken("workflow registration")) {
      return;
    }
    const payload = formObject(el.workflowForm);
    payload.stages = payload.stages ? payload.stages.split(",").map((stage) => stage.trim()).filter(Boolean) : [];
    try {
      await api("/v1/workflows", {
        method: "POST",
        headers: devHeaders(),
        body: JSON.stringify(payload),
      });
      el.workflowForm.reset();
      log("workflow registered: " + payload.id);
      refreshAll();
    } catch (err) {
      log("workflow failed: " + err.message);
    }
  });

  el.auditForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (!requireDevToken("audit lookup")) {
      return;
    }
    const payload = formObject(el.auditForm);
    try {
      await loadAuditTrail(payload.session_id);
    } catch (err) {
      log("audit lookup failed: " + err.message);
    }
  });

  refreshAll();
})();
