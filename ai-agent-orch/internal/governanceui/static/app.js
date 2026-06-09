(function () {
  const state = {
    baseUrl: localStorage.getItem("ai_orch_base_url") || window.location.origin,
    devToken: localStorage.getItem("ai_orch_dev_token") || "",
    adminToken: localStorage.getItem("ai_orch_admin_token") || "",
    lastReady: null,
    lastStatus: null,
    lastMetrics: null,
    lastAgents: [],
    lastSessions: [],
    selectedSessionID: "",
  };

  const el = {
    baseUrl: document.getElementById("baseUrl"),
    devToken: document.getElementById("devToken"),
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
    copilotStatusBadge: document.getElementById("copilotStatusBadge"),
    copilotStatusText: document.getElementById("copilotStatusText"),
    copilotLogin: document.getElementById("copilotLogin"),
    copilotModels: document.getElementById("copilotModels"),
    copilotOutput: document.getElementById("copilotOutput"),
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
  };

  el.baseUrl.value = state.baseUrl;
  el.devToken.value = state.devToken;
  el.adminToken.value = state.adminToken;

  function baseUrl() {
    return (state.baseUrl || window.location.origin).replace(/\/+$/, "");
  }

  function devHeaders() {
    const headers = { "Content-Type": "application/json" };
    if (state.devToken) headers.Authorization = "Bearer " + state.devToken;
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
    return state.devToken.trim().length > 0;
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
    const checks = [
      {
        label: "Service",
        ok: Boolean(state.lastReady && state.lastReady.service),
        value: state.lastReady && state.lastReady.service ? state.lastReady.service : "pending",
      },
      {
        label: "Auth",
        ok: hasDevToken(),
        value: hasDevToken() ? "token set" : "token required",
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
        label: "Smoke",
        ok: metricNumber("sessions_created") > 0 && patchDecisions > 0,
        value: metricNumber("sessions_created") + " sessions / " + patchDecisions + " patches",
      },
    ];
    const passed = checks.filter((check) => check.ok).length;
    const allPassed = passed === checks.length;
    const authPending = !hasDevToken();
    el.readinessBadge.textContent = allPassed ? "Demo ready" : authPending ? "Auth pending" : "Needs smoke";
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
    emptyList(el.gatewayList, "Developer token required");
    emptyList(el.agentList, "Developer token required");
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
    log(action + " requires developer token");
    renderProtectedPending();
    return false;
  }

  function renderGatewayStatus(status) {
    state.lastStatus = status || null;
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
    loadCopilotStatus().catch(() => {});
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
      if (el.copilotOutput) el.copilotOutput.textContent = result.output || "ok";
      refreshAll();
    } catch (err) {
      log("backend " + action + " failed: " + err.message);
      if (el.copilotOutput) el.copilotOutput.textContent = err.message;
    }
  }

  async function loadCopilotStatus() {
    if (!hasDevToken()) return;
    try {
      const status = await api("/v1/copilot/status", { headers: devHeaders() });
      el.copilotStatusBadge.textContent = status.configured ? "Configured" : "Not configured";
      el.copilotStatusText.textContent = status.configured
        ? "GitHub " + (status.github_login || "unknown") + " for " + status.actor_subject
        : "No Copilot token for " + (status.actor_subject || "current actor") + ".";
    } catch (err) {
      el.copilotStatusBadge.textContent = "Unavailable";
      el.copilotStatusText.textContent = err.message;
    }
  }

  async function startCopilotLogin() {
    if (!requireDevToken("Copilot login")) return;
    const login = await api("/v1/copilot/login/start", { method: "POST", headers: devHeaders() });
    const url = login.verification_uri_complete || login.verification_uri;
    el.copilotOutput.textContent = "Open: " + url + "\nCode: " + login.user_code + "\nLogin ID: " + login.login_id;
    log("copilot login started");
  }

  async function listCopilotModels() {
    if (!requireDevToken("Copilot models")) return;
    const models = await api("/v1/copilot/models", { headers: devHeaders() });
    el.copilotOutput.textContent = JSON.stringify(models, null, 2);
    log("copilot models loaded");
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
    const sessions = Number(metrics.sessions_created || 0);
    const applied = Number(metrics.patches_applied || 0);
    const rejected = Number(metrics.patches_rejected || 0);
    const hits = Number(metrics.cache_hits || 0);
    const misses = Number(metrics.cache_misses || 0);
    const rate = hits + misses === 0 ? "-" : Math.round((hits / (hits + misses)) * 100) + "%";
    el.sessionCount.textContent = String(sessions);
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
	list.slice(0, 20).forEach((session) => {
	  const button = document.createElement("button");
	  button.type = "button";
	  button.className = "session-item" + (session.session_id === state.selectedSessionID ? " selected" : "");
	  button.dataset.sessionId = session.session_id;
	  const created = formatDate(session.created_at);
	  const summary = session.usage_summary || {};
	  const mode = formatModeLabels(session);
	  const model = formatModelSummary(summary);
	  const tokens = formatTokenSummary(summary);
	  const cost = formatCostSummary(summary);
	  button.innerHTML = [
	    "<strong>" + escapeHtml(session.agent || "session") + "</strong>",
	    "<span>" + escapeHtml(session.status || "-") + "</span>",
	    "<code>" + escapeHtml(session.session_id || "") + "</code>",
	    "<p>" + escapeHtml([created, mode].filter(Boolean).join(" - ")) + "</p>",
	    "<p>" + escapeHtml(model) + "</p>",
	    "<p>" + escapeHtml(tokens + " - " + cost) + "</p>",
	  ].join("");
	  button.addEventListener("click", () => loadAuditTrail(session.session_id));
	  el.sessionList.appendChild(button);
	});
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
	    event.model_alias ? "model " + event.model_alias : "",
	    event.model_resolved ? "resolved " + event.model_resolved : "",
	    event.gateway_backend ? "gateway " + event.gateway_backend : "",
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
    const audit = await api("/v1/audit/sessions/" + encodeURIComponent(sessionID), {
      headers: devHeaders(),
    });
    renderAuditTrail(audit);
    log("audit loaded: " + sessionID);
  }

      function formatDate(value) {
	if (!value) return "";
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return String(value);
	return date.toLocaleString();
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

    if (!hasDevToken()) {
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

    try {
      const response = await api("/v1/sessions?limit=20", { headers: devHeaders() });
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
    state.devToken = el.devToken.value.trim();
    state.adminToken = el.adminToken.value.trim();
    localStorage.setItem("ai_orch_base_url", state.baseUrl);
    localStorage.setItem("ai_orch_dev_token", state.devToken);
    localStorage.setItem("ai_orch_admin_token", state.adminToken);
    log("connection saved");
    refreshAll();
  });

  el.refresh.addEventListener("click", refreshAll);
  if (el.copilotLogin) {
    el.copilotLogin.addEventListener("click", () => {
      startCopilotLogin().catch((err) => log("copilot login failed: " + err.message));
    });
  }
  if (el.copilotModels) {
    el.copilotModels.addEventListener("click", () => {
      listCopilotModels().catch((err) => log("copilot models failed: " + err.message));
    });
  }
  el.clearLog.addEventListener("click", () => {
    el.activityLog.innerHTML = "";
  });

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
