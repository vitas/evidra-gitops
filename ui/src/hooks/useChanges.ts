import { useState } from "react";
import type { ChangeDetail, ChangeEvidence, ChangeItem, EventItem, ExportJob, SubjectInfo } from "../types";
import { sortEventsChronologically } from "../utils/evidenceSummary";
import { localToRFC3339 } from "./useFilterState";

type Status = "idle" | "error";
type ChangesState = "idle" | "loading" | "loaded" | "empty" | "error";

type UseChangesOptions = {
  apiBase: string;
  authMode: string;
  authToken: string;
};

export type UseChangesReturn = {
  changes: ChangeItem[];
  changesState: ChangesState;
  selectedChange: ChangeDetail | null;
  selectedEvents: EventItem[];
  supportingCount: number;
  approvalsCount: number;
  nextCursor: string;
  lastExport: ExportJob | null;
  detailAuthError: boolean;
  statusText: string;
  statusKind: Status;
  subjects: string[];
  autoSearchEnabled: boolean;
  loadSubjects: () => Promise<string[]>;
  loadChanges: (filters: ChangeFilters, preferredID?: string, reset?: boolean) => Promise<void>;
  loadChangeDetail: (id: string, filters: ChangeFilters) => Promise<void>;
  pollExport: (id: string) => Promise<void>;
  createExport: (filters: ChangeFilters, corrKey: string, q: string) => Promise<void>;
  setSubjects: (s: string[]) => void;
  setSelectedChange: (c: ChangeDetail | null) => void;
  setSelectedEvents: (e: EventItem[]) => void;
  setLastExport: (j: ExportJob | null) => void;
  setStatus: (msg: string, error?: boolean) => void;
  setAutoSearchEnabled: (v: boolean) => void;
  setNextCursor: (v: string) => void;
};

export type ChangeFilters = {
  subject: string;
  from: string;
  to: string;
  resultStatus: string;
  externalChangeID: string;
  ticketID: string;
  approvalReference: string;
  nextCursor?: string;
  syncURL: (changeID: string) => void;
};

export function useChanges({ apiBase, authMode, authToken }: UseChangesOptions): UseChangesReturn {
  const [changes, setChanges] = useState<ChangeItem[]>([]);
  const [changesState, setChangesState] = useState<ChangesState>("idle");
  const [selectedChange, setSelectedChange] = useState<ChangeDetail | null>(null);
  const [selectedEvents, setSelectedEvents] = useState<EventItem[]>([]);
  const [supportingCount, setSupportingCount] = useState(0);
  const [approvalsCount, setApprovalsCount] = useState(0);
  const [nextCursor, setNextCursor] = useState("");
  const [lastExport, setLastExport] = useState<ExportJob | null>(null);
  const [detailAuthError, setDetailAuthError] = useState(false);
  const [statusText, setStatusText] = useState("Ready.");
  const [statusKind, setStatusKind] = useState<Status>("idle");
  const [subjects, setSubjects] = useState<string[]>([]);
  const [autoSearchEnabled, setAutoSearchEnabled] = useState(false);

  function setStatus(msg: string, error = false) {
    setStatusText(msg);
    setStatusKind(error ? "error" : "idle");
  }

  async function loadSubjects() {
    try {
      const data = await apiGet<{ items: SubjectInfo[] }>("/v1/subjects", apiBase, authMode, authToken);
      const list = Array.from(new Set((data.items || []).map((s) => `${s.subject}:${s.namespace}:${s.cluster}`)));
      setSubjects(list);
      setStatus("");
      return list;
    } catch (err) {
      setStatus(`Failed to load subjects: ${(err as Error).message}`, true);
      return [];
    }
  }

  async function loadChanges(filters: ChangeFilters, preferredID = "", reset = true) {
    const { subject, from, to, resultStatus, externalChangeID, ticketID, approvalReference, nextCursor: cursor, syncURL } = filters;
    if (!subject) {
      setStatus("Select a subject before loading changes.", true);
      return;
    }
    const fromRFC = localToRFC3339(from);
    const toRFC = localToRFC3339(to);
    if (!fromRFC || !toRFC) {
      setStatus("Set valid from/to timestamps.", true);
      return;
    }
    try {
      if (reset) setChangesState("loading");
      const params = new URLSearchParams({ subject, from: fromRFC, to: toRFC, limit: "50" });
      if (resultStatus) params.set("result_status", resultStatus);
      if (externalChangeID) params.set("external_change_id", externalChangeID);
      if (ticketID) params.set("ticket_id", ticketID);
      if (approvalReference) params.set("approval_reference", approvalReference);
      if (!reset && cursor) params.set("cursor", cursor);

      const resp = await apiGet<{ items: ChangeItem[]; page?: { next_cursor?: string } }>(`/v1/changes?${params.toString()}`, apiBase, authMode, authToken);
      const items = resp.items || [];
      const mergedCount = (reset ? 0 : changes.length) + items.length;
      setNextCursor(resp.page?.next_cursor || "");
      setChanges((prev) => (reset ? items : [...prev, ...items]));

      if (mergedCount === 0) {
        setSelectedChange(null);
        setSelectedEvents([]);
        setSupportingCount(0);
        setApprovalsCount(0);
        setDetailAuthError(false);
        syncURL("");
        setChangesState("empty");
        setStatus("");
        return;
      }

      setChangesState("loaded");
      if (reset) await loadChangeDetail(preferredID || items[0].id, filters);
      setStatus("");
    } catch (err) {
      if (reset) setChangesState("error");
      setStatus(`Failed to load changes: ${(err as Error).message}`, true);
    }
  }

  async function loadChangeDetail(id: string, filters: ChangeFilters) {
    const { subject, from, to, syncURL } = filters;
    const fromRFC = localToRFC3339(from);
    const toRFC = localToRFC3339(to);
    if (!subject || !fromRFC || !toRFC) return;

    try {
      const params = new URLSearchParams({ subject, from: fromRFC, to: toRFC });
      const detail = await apiGet<ChangeDetail>(`/v1/changes/${encodeURIComponent(id)}?${params.toString()}`, apiBase, authMode, authToken);
      const timeline = await apiGet<{ items: EventItem[] }>(`/v1/changes/${encodeURIComponent(id)}/timeline?${params.toString()}`, apiBase, authMode, authToken);
      const evidence = await apiGet<ChangeEvidence>(`/v1/changes/${encodeURIComponent(id)}/evidence?${params.toString()}`, apiBase, authMode, authToken);

      const sorted = sortEventsChronologically(timeline.items || []);
      setSelectedChange(detail);
      setSelectedEvents(sorted);
      setSupportingCount(evidence.supporting_observations?.length || 0);
      setApprovalsCount(evidence.approvals?.length || 0);
      setDetailAuthError(false);
      syncURL(detail.id);
    } catch (err) {
      setDetailAuthError(isUnauthorized(err));
      setStatus(`Failed to load change detail: ${(err as Error).message}`, true);
    }
  }

  async function pollExport(id: string) {
    for (let i = 0; i < 20; i += 1) {
      await sleep(1000);
      const job = await apiGet<ExportJob>(`/v1/exports/${encodeURIComponent(id)}`, apiBase, authMode, authToken);
      setLastExport(job);
      if (job.status === "completed") {
        setStatus(`Export completed: ${job.id}`);
        return;
      }
      if (job.status === "failed") {
        setStatus(`Export failed: ${job.error || "unknown error"}`, true);
        return;
      }
    }
    setStatus(`Export polling timed out for ${id}.`, true);
  }

  async function createExport(filters: ChangeFilters, corrKey: string, q: string) {
    const { subject, from, to } = filters;
    const fromRFC = localToRFC3339(from);
    const toRFC = localToRFC3339(to);
    if (!subject || !fromRFC || !toRFC) {
      setStatus("Subject and time range are required for export.", true);
      return;
    }
    const filter: Record<string, string> = { subject, from: fromRFC, to: toRFC };
    if (q.trim() && corrKey) {
      filter.correlation_key = corrKey;
      filter.correlation_value = q.trim();
    }
    try {
      const job = await apiPost<ExportJob>("/v1/exports", { format: "json", filter }, apiBase, authMode, authToken);
      setLastExport(job);
      setStatus(`Export created: ${job.id} (${job.status}). Polling...`);
      await pollExport(job.id);
    } catch (err) {
      setStatus(`Export failed: ${(err as Error).message}`, true);
    }
  }

  return {
    changes, changesState, selectedChange, selectedEvents,
    supportingCount, approvalsCount, nextCursor, lastExport, detailAuthError,
    statusText, statusKind, subjects, autoSearchEnabled,
    loadSubjects,
    loadChanges, loadChangeDetail, pollExport, createExport,
    setSubjects, setSelectedChange, setSelectedEvents, setLastExport,
    setStatus, setAutoSearchEnabled, setNextCursor,
  };
}

async function apiGet<T>(path: string, apiBase: string, authMode: string, authToken: string): Promise<T> {
  const req: RequestInit = { headers: authHeaders(authMode, authToken) };
  if (authMode === "cookie") req.credentials = "include";
  const res = await fetch(`${apiBase}${path}`, req);
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body?.error?.message || `HTTP ${res.status}`);
  return body as T;
}

async function apiPost<T>(path: string, payload: unknown, apiBase: string, authMode: string, authToken: string): Promise<T> {
  const headers = authHeaders(authMode, authToken);
  headers["Content-Type"] = "application/json";
  const req: RequestInit = { method: "POST", headers, body: JSON.stringify(payload) };
  const res = await fetch(`${apiBase}${path}`, req);
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body?.error?.message || `HTTP ${res.status}`);
  return body as T;
}

function authHeaders(authMode: string, authToken: string): Record<string, string> {
  if (authMode === "bearer" && authToken) return { Authorization: `Bearer ${authToken}` };
  return {};
}

function isUnauthorized(err: unknown): boolean {
  const msg = String((err as Error)?.message || "");
  return msg.includes("HTTP 401") || msg.includes("HTTP 403");
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
