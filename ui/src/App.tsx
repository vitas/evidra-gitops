import { useEffect, useMemo, useState, type KeyboardEvent } from "react";
import { CollapsibleRawEvidence } from "./components/CollapsibleRawEvidence";
import { EvidenceSummary } from "./components/EvidenceSummary";
import { IncidentTimeline } from "./components/IncidentTimeline";
import { RootCauseBanner } from "./components/RootCauseBanner";
import { SubjectSelect } from "./components/SubjectSelect";
import { useChanges } from "./hooks/useChanges";
import { useFilterState } from "./hooks/useFilterState";
import type { ChangeDetail, ChangeItem, ExportJob } from "./types";
import { buildEvidenceSummary } from "./ux";
import { computeOverallStatus, sortEventsChronologically } from "./utils/evidenceSummary";
import { fmtDate } from "./utils/time";

type RuntimeCfg = {
  apiBase?: string;
  authMode?: string;
  authToken?: string;
};

const runtimeCfg = resolveRuntimeConfig();
const apiBase = (runtimeCfg.apiBase || "").replace(/\/$/, "");
const authMode = runtimeCfg.authMode || "none";
const authToken = runtimeCfg.authToken || "";
const changePathPrefix = "/ui/explorer/change/";
const embeddedMode = new URL(window.location.href).searchParams.get("embedded") === "argocd";
const buildLabel = String(import.meta.env.VITE_UI_BUILD || "dev");

export function App() {
  const filters = useFilterState();
  const {
    subject, setSubject, from, setFrom, to, setTo,
    resultStatus, setResultStatus, externalChangeID, setExternalChangeID,
    ticketID, setTicketID, approvalReference, setApprovalReference,
    corrKey, setCorrKey, q, setQ,
    activeFilterCount, syncURL, localToRFC3339,
  } = filters;

  const api = useChanges({ apiBase, authMode, authToken });
  const {
    changes, changesState, selectedChange, selectedEvents,
    supportingCount, approvalsCount, nextCursor, lastExport, detailAuthError,
    statusText, statusKind, subjects, autoSearchEnabled,
    loadSubjects, loadChanges, loadChangeDetail, createExport,
    setAutoSearchEnabled,
  } = api;

  const [selectedEventID, setSelectedEventID] = useState("");

  const sortedEvents = useMemo(() => sortEventsChronologically(selectedEvents), [selectedEvents]);
  const selectedEvent = useMemo(() => sortedEvents.find((e) => e.id === selectedEventID) || sortedEvents[0] || null, [sortedEvents, selectedEventID]);
  const overallStatus = useMemo(() => computeOverallStatus(selectedChange, sortedEvents), [selectedChange, sortedEvents]);
  const evidenceGroups = useMemo(() => buildEvidenceSummary(selectedChange, sortedEvents), [selectedChange, sortedEvents]);
  const filtersHelperText = useMemo(() => {
    if (statusKind === "error") return statusText;
    if (activeFilterCount > 0 && changes.length > 0) return `${changes.length} changes match current filters`;
    return "";
  }, [statusKind, statusText, activeFilterCount, changes.length]);

  const currentFilters = useMemo(() => ({
    subject, from, to, resultStatus, externalChangeID, ticketID, approvalReference,
    nextCursor, syncURL,
  }), [subject, from, to, resultStatus, externalChangeID, ticketID, approvalReference, nextCursor, syncURL]);

  useEffect(() => {
    void loadSubjects().then((list) => {
      if (list.length > 0 && !subject) {
        setSubject(list[0]);
      }
      setAutoSearchEnabled(true);
    });
  }, []);

  useEffect(() => {
    if (!autoSearchEnabled) return;
    const timer = window.setTimeout(() => {
      void loadChanges(currentFilters, "", true);
    }, 300);
    return () => window.clearTimeout(timer);
  }, [subject, from, to, resultStatus, q, externalChangeID, ticketID, approvalReference, autoSearchEnabled]);

  useEffect(() => {
    const id = changeIDFromPath(window.location.pathname);
    if (id && subject) {
      void loadChanges(currentFilters, id, true);
    }
  }, [subject]);

  function handleLoadChanges(preferredID = "", reset = true) {
    return loadChanges({ ...currentFilters, nextCursor: reset ? "" : nextCursor }, preferredID, reset);
  }

  function handleLoadChangeDetail(id: string) {
    return loadChangeDetail(id, currentFilters);
  }

  function handleCreateExport() {
    return createExport(currentFilters, corrKey, q);
  }

  function downloadLastExport() {
    if (!lastExport?.id) return;
    window.open(`${apiBase}/v1/exports/${encodeURIComponent(lastExport.id)}/download`, "_blank", "noopener,noreferrer");
  }

  function applyQuickFilter(kind: "external" | "ticket" | "approval", value: string) {
    const next = value.trim();
    if (!next) return;
    if (kind === "external") setExternalChangeID(next);
    if (kind === "ticket") setTicketID(next);
    if (kind === "approval") setApprovalReference(next);
    window.setTimeout(() => void handleLoadChanges("", true), 0);
  }

  function handleCorrelationEnter(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key !== "Enter") return;
    e.preventDefault();
    void handleLoadChanges("", true);
  }

  function copyToClipboard(value: string, label: string) {
    const text = value.trim();
    if (!text) return;
    void navigator.clipboard.writeText(text)
      .then(() => api.setStatus(`Copied ${label}.`))
      .catch(() => api.setStatus(`Unable to copy ${label}.`, true));
  }

  function copyRevision() {
    const revision = (selectedChange?.revision || "").trim();
    if (!revision) return;
    copyToClipboard(revision, "revision");
  }

  function currentPermalink(changeID: string): string {
    return absoluteChangeLink(changeID, {
      subject, from, to, resultStatus, externalChangeID, ticketID, approvalReference, q, corrKey,
    });
  }

  async function searchCorrelationFallback() {
    const value = q.trim();
    if (!value) {
      api.setStatus("Set correlation value.", true);
      return;
    }
    if (!corrKey) {
      await handleLoadChanges("", true);
      api.setStatus("Applied text search with current filters.");
      return;
    }
    try {
      const params = new URLSearchParams({ value });
      const resp = await apiFetch<{ items: unknown[] }>(`/v1/correlations/${encodeURIComponent(corrKey)}?${params.toString()}`);
      const events = resp.items || [];
      if (events.length === 0) {
        api.setStatus("No correlation matches.");
        return;
      }
      api.setStatus(`Correlation fallback returned ${events.length} events. Narrow with subject/time and load changes.`);
    } catch (err) {
      api.setStatus(`Correlation search failed: ${(err as Error).message}`, true);
    }
  }

  const payloadSource = selectedEvent || selectedChange || {};

  return (
    <main className={`layout ${embeddedMode ? "layout-embedded" : ""}`.trim()} data-testid="explorer-root">
      <header className="topbar">
        <h1>Evidence Explorer</h1>
        <p>Investigation view for correlated GitOps evidence.</p>
      </header>

      <section className="controls" aria-label="Filters" data-testid="filters-panel">
        <div className="row">
          <label>Subject
            <SubjectSelect value={subject} onChange={setSubject} options={subjects} />
          </label>
          <label>From
            <input type="datetime-local" value={from} onChange={(e) => setFrom(e.target.value)} data-testid="from-input" />
          </label>
          <label>To
            <input type="datetime-local" value={to} onChange={(e) => setTo(e.target.value)} data-testid="to-input" />
          </label>
          <label>Result status
            <select value={resultStatus} onChange={(e) => setResultStatus(e.target.value)} data-testid="result-status-select">
              <option value="">Any</option>
              <option value="succeeded">succeeded</option>
              <option value="failed">failed</option>
              <option value="unknown">unknown</option>
            </select>
          </label>
          <label>External Change ID
            <input type="text" value={externalChangeID} onChange={(e) => setExternalChangeID(e.target.value)} data-testid="external-change-id-input" placeholder="CHG123456" />
          </label>
          <label>Ticket ID
            <input type="text" value={ticketID} onChange={(e) => setTicketID(e.target.value)} data-testid="ticket-id-input" placeholder="JIRA-42" />
          </label>
          <button type="button" onClick={() => void handleLoadChanges("", true)} data-testid="find-changes-button">Find changes</button>
        </div>

        <details className="advanced-block" data-testid="advanced-correlation">
          <summary>Advanced correlation</summary>
          <div className="row">
            <label>Approval reference
              <input type="text" value={approvalReference} onChange={(e) => setApprovalReference(e.target.value)} data-testid="approval-reference-input" placeholder="APR-7" />
            </label>
            <label>Correlation key
              <select value={corrKey} onChange={(e) => setCorrKey(e.target.value)} data-testid="correlation-key-select">
                <option value="">any key</option>
                <option value="repo">repo</option>
                <option value="commit_sha">commit_sha</option>
                <option value="pr_id">pr_id</option>
                <option value="argocd_app">argocd_app</option>
                <option value="sync_revision">sync_revision</option>
                <option value="deploy_id">deploy_id</option>
                <option value="operation_id">operation_id</option>
                <option value="ticket_key">ticket_key</option>
                <option value="ticket_id">ticket_id</option>
                <option value="external_change_id">external_change_id</option>
                <option value="approval_reference">approval_reference</option>
              </select>
            </label>
            <label className="grow">Correlation value
              <input type="text" value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={handleCorrelationEnter} data-testid="correlation-value-input" placeholder="abc123 / PROJ-42 / 12345" />
            </label>
            <button type="button" onClick={() => void searchCorrelationFallback()} data-testid="search-raw-events-button">Search raw events</button>
            <button type="button" onClick={() => void handleCreateExport()} data-testid="create-export-button">Create export</button>
            {lastExport?.status === "completed" ? (
              <button type="button" onClick={downloadLastExport} data-testid="download-export-button">Download export</button>
            ) : null}
            {lastExport ? <span className="meta" data-testid="export-status">Export {lastExport.id}: {lastExport.status}</span> : null}
          </div>
        </details>
      </section>

      {filtersHelperText ? (
        <p className={`filters-helper ${statusKind === "error" ? "error" : ""}`} aria-live="polite" data-testid="filters-helper">
          {filtersHelperText}
        </p>
      ) : null}

      <section className="content" data-testid="content-layout">
        <aside className="panel" aria-label="Changes" data-testid="changes-panel">
          <h2>Changes ({changes.length})</h2>
          {changes.length === 0 ? (
            <p className="meta empty-hint" data-testid="changes-empty-state" data-state={changesState}>
              {changesState === "idle" ? "Adjust filters or search by correlation key to find production changes." : null}
              {changesState === "loading" ? "Searching changes..." : null}
              {changesState === "empty" ? "No changes found. Adjust filters or search by correlation key to find production changes." : null}
              {changesState === "error" ? "Unable to load changes. Adjust filters and retry." : null}
            </p>
          ) : null}
          <ul id="changeList" data-testid="changes-list">
            {changes.map((c) => (
              <li key={c.id} className={`item ${selectedChange?.id === c.id ? "selected" : ""}`.trim()} onClick={() => void handleLoadChangeDetail(c.id)} data-testid="change-item" data-change-id={c.id} aria-selected={selectedChange?.id === c.id}>
                <strong>{c.application || c.subject}</strong>
                <span className={`badge ${c.result_status}`}>{c.result_status}</span>
                <div className="meta">{c.id} | {fmtDate(c.started_at)} - {fmtDate(c.completed_at)}</div>
                <div className="meta chips">
                  <span className="chip">Ext: {c.external_change_id || "n/a"}</span>
                  <span className="chip">Ticket: {c.ticket_id || "n/a"}</span>
                  <span className="chip">Approval: {c.approval_reference || "n/a"}</span>
                </div>
              </li>
            ))}
          </ul>
          {nextCursor ? <button type="button" onClick={() => void handleLoadChanges("", false)} data-testid="load-more-button">Load more</button> : null}
        </aside>

        <section className="panel main" aria-label="Timeline and details" data-testid="timeline-panel">
          {selectedChange ? (
            <RootCauseBanner
              change={selectedChange}
              events={sortedEvents}
              permalink={currentPermalink(selectedChange.change_id || selectedChange.id)}
              onCopyPermalink={() => copyToClipboard(currentPermalink(selectedChange.change_id || selectedChange.id), "permalink")}
              onExport={() => void handleCreateExport()}
            />
          ) : (
            <div className="selected-change-bar" data-testid="selected-change-header"><div className="meta">No change selected.</div></div>
          )}

          {selectedChange ? (
            <section className="selected-change-bar selected-change-header" data-testid="selected-change-header">
              <div className="selected-change-header__primary">
                <div className="selected-change-header__id">
                  <strong>{selectedChange.change_id || selectedChange.id}</strong>
                  <span className={`badge ${overallStatus.status}`}>{overallStatus.status}</span>
                </div>
                <div><strong>Application:</strong> {selectedChange.application || selectedChange.subject}</div>
                <div>
                  <strong>Revision:</strong> {selectedChange.revision || "n/a"}
                  {selectedChange.revision ? (
                    <button type="button" className="secondary inline-button" onClick={copyRevision} data-testid="copy-revision-button">Copy revision</button>
                  ) : null}
                </div>
                <div className="chips">
                  {selectedChange.external_change_id ? <span className="chip">Change {selectedChange.external_change_id}</span> : null}
                  {selectedChange.ticket_id ? <span className="chip">Ticket {selectedChange.ticket_id}</span> : null}
                </div>
                <button type="button" className="secondary" onClick={() => void handleCreateExport()} data-testid="export-header-button">Export</button>
              </div>

              <details className="selected-change-header__secondary">
                <summary>More details</summary>
                <div className="meta">
                  Project: {selectedChange.project || "n/a"} | Cluster: {selectedChange.target_cluster || "n/a"} | Namespace: {selectedChange.namespace || "n/a"}
                </div>
                <div className="meta">
                  Initiator: {selectedChange.initiator || "n/a"} | Started: {fmtDate(selectedChange.started_at)} | Completed: {fmtDate(selectedChange.completed_at)}
                </div>
                <div className="meta">
                  Supporting observations: {supportingCount}
                </div>
              </details>
            </section>
          ) : null}

          <IncidentTimeline
            events={sortedEvents}
            selectedEventID={selectedEvent?.id || ""}
            onSelect={setSelectedEventID}
            breakingEventID={overallStatus.breakingEvent?.id || ""}
          />

          <EvidenceSummary groups={evidenceGroups} />

          <section className="evidence-links meta">
            <strong>References:</strong> {selectedChange ? (
              <>
                External{" "}
                <button type="button" className="linkish" onClick={() => applyQuickFilter("external", selectedChange.external_change_id || "")}>
                  {selectedChange.external_change_id || "n/a"}
                </button>{" "}
                | Ticket{" "}
                <button type="button" className="linkish" onClick={() => applyQuickFilter("ticket", selectedChange.ticket_id || "")}>
                  {selectedChange.ticket_id || "n/a"}
                </button>{" "}
                | Approval{" "}
                <button type="button" className="linkish" onClick={() => applyQuickFilter("approval", selectedChange.approval_reference || "")}>
                  {selectedChange.approval_reference || "n/a"}
                </button>{" "}
                | Approvals: {approvalsCount}
              </>
            ) : "n/a"}
          </section>

          <CollapsibleRawEvidence payload={payloadSource} selected={!!selectedChange || !!selectedEvent} detailAuthError={detailAuthError} />
        </section>
      </section>

      <footer className="build-info" data-testid="ui-build-info">
        UI build: {buildLabel} ({embeddedMode ? "argocd-embedded" : "standalone"})
      </footer>
    </main>
  );
}

async function apiFetch<T>(path: string): Promise<T> {
  const headers: Record<string, string> = {};
  if (authMode === "bearer" && authToken) headers["Authorization"] = `Bearer ${authToken}`;
  const req: RequestInit = { headers };
  if (authMode === "cookie") req.credentials = "include";
  const res = await fetch(`${apiBase}${path}`, req);
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body?.error?.message || `HTTP ${res.status}`);
  return body as T;
}

type LinkContext = {
  subject?: string;
  from?: string;
  to?: string;
  resultStatus?: string;
  externalChangeID?: string;
  ticketID?: string;
  approvalReference?: string;
  q?: string;
  corrKey?: string;
};

function absoluteChangeLink(changeID: string, ctx?: LinkContext): string {
  const id = encodeURIComponent(changeID);
  const url = new URL(`${window.location.origin}${changePathPrefix}${id}`);
  const fromRFC = localToRFC3339(ctx?.from || "");
  const toRFC = localToRFC3339(ctx?.to || "");
  if (ctx?.subject) url.searchParams.set("subject", ctx.subject);
  if (fromRFC) url.searchParams.set("from", fromRFC);
  if (toRFC) url.searchParams.set("to", toRFC);
  if (ctx?.resultStatus) url.searchParams.set("result_status", ctx.resultStatus);
  if (ctx?.externalChangeID) url.searchParams.set("external_change_id", ctx.externalChangeID);
  if (ctx?.ticketID) url.searchParams.set("ticket_id", ctx.ticketID);
  if (ctx?.approvalReference) url.searchParams.set("approval_reference", ctx.approvalReference);
  if (ctx?.q) url.searchParams.set("q", ctx.q);
  if (ctx?.corrKey) url.searchParams.set("corr_key", ctx.corrKey);
  return url.toString();
}

function localToRFC3339(v: string): string {
  if (!v) return "";
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? "" : d.toISOString();
}

function changeIDFromPath(pathname: string): string {
  if (!pathname.startsWith(changePathPrefix)) return "";
  return decodeURIComponent(pathname.slice(changePathPrefix.length).split("/")[0] || "");
}

function resolveRuntimeConfig(): RuntimeCfg {
  const winCfg = (window as unknown as { __EVIDRA_CONFIG__?: RuntimeCfg }).__EVIDRA_CONFIG__ || {};
  const url = new URL(window.location.href);
  return {
    apiBase: ((url.searchParams.get("api_base") || "").trim() || winCfg.apiBase || "").replace(/\/$/, ""),
    authMode: (url.searchParams.get("auth_mode") || "").trim() || winCfg.authMode || "none",
    authToken: (url.searchParams.get("auth_token") || "").trim() || winCfg.authToken || "",
  };
}
