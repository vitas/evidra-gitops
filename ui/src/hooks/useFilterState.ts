import { useMemo, useState } from "react";

const changePathPrefix = "/ui/explorer/change/";

type FilterState = {
  subject: string;
  from: string;
  to: string;
  resultStatus: string;
  externalChangeID: string;
  ticketID: string;
  approvalReference: string;
  corrKey: string;
  q: string;
};

type InitialURLState = FilterState;

export type UseFilterStateReturn = {
  subject: string;
  setSubject: (v: string) => void;
  from: string;
  setFrom: (v: string) => void;
  to: string;
  setTo: (v: string) => void;
  resultStatus: string;
  setResultStatus: (v: string) => void;
  externalChangeID: string;
  setExternalChangeID: (v: string) => void;
  ticketID: string;
  setTicketID: (v: string) => void;
  approvalReference: string;
  setApprovalReference: (v: string) => void;
  corrKey: string;
  setCorrKey: (v: string) => void;
  q: string;
  setQ: (v: string) => void;
  activeFilterCount: number;
  syncURL: (changeID: string) => void;
  localToRFC3339: (v: string) => string;
};

export function useFilterState(): UseFilterStateReturn {
  const initial = parseInitialURLState();
  const [subject, setSubject] = useState(initial.subject);
  const [from, setFrom] = useState(initial.from);
  const [to, setTo] = useState(initial.to);
  const [resultStatus, setResultStatus] = useState(initial.resultStatus);
  const [externalChangeID, setExternalChangeID] = useState(initial.externalChangeID);
  const [ticketID, setTicketID] = useState(initial.ticketID);
  const [approvalReference, setApprovalReference] = useState(initial.approvalReference);
  const [corrKey, setCorrKey] = useState(initial.corrKey);
  const [q, setQ] = useState(initial.q);

  const activeFilterCount = useMemo(
    () => [resultStatus, externalChangeID, ticketID, approvalReference, corrKey, q]
      .filter((v) => v.trim() !== "").length,
    [resultStatus, externalChangeID, ticketID, approvalReference, corrKey, q],
  );

  function syncURL(changeID: string) {
    const url = new URL(window.location.href);
    url.pathname = changeID ? `${changePathPrefix}${encodeURIComponent(changeID)}` : "/ui/";
    url.search = "";
    const fromRFC = localToRFC3339(from);
    const toRFC = localToRFC3339(to);
    if (subject) url.searchParams.set("subject", subject);
    if (fromRFC) url.searchParams.set("from", fromRFC);
    if (toRFC) url.searchParams.set("to", toRFC);
    if (resultStatus) url.searchParams.set("result_status", resultStatus);
    if (externalChangeID) url.searchParams.set("external_change_id", externalChangeID);
    if (ticketID) url.searchParams.set("ticket_id", ticketID);
    if (approvalReference) url.searchParams.set("approval_reference", approvalReference);
    if (q) url.searchParams.set("q", q);
    if (corrKey) url.searchParams.set("corr_key", corrKey);
    window.history.pushState({}, "", url.toString());
  }

  return {
    subject, setSubject,
    from, setFrom,
    to, setTo,
    resultStatus, setResultStatus,
    externalChangeID, setExternalChangeID,
    ticketID, setTicketID,
    approvalReference, setApprovalReference,
    corrKey, setCorrKey,
    q, setQ,
    activeFilterCount,
    syncURL,
    localToRFC3339,
  };
}

export function localToRFC3339(v: string): string {
  if (!v) return "";
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? "" : d.toISOString();
}

function parseInitialURLState(): InitialURLState {
  const url = new URL(window.location.href);
  return {
    subject: url.searchParams.get("subject") || "",
    from: rfc3339ToLocal(url.searchParams.get("from") || "") || defaultFrom(),
    to: rfc3339ToLocal(url.searchParams.get("to") || "") || defaultTo(),
    resultStatus: url.searchParams.get("result_status") || "",
    externalChangeID: url.searchParams.get("external_change_id") || "",
    ticketID: url.searchParams.get("ticket_id") || "",
    approvalReference: url.searchParams.get("approval_reference") || "",
    corrKey: url.searchParams.get("corr_key") || "",
    q: url.searchParams.get("q") || "",
  };
}

function defaultFrom(): string {
  const to = new Date();
  return toLocalInput(new Date(to.getTime() - 7 * 24 * 60 * 60 * 1000));
}

function defaultTo(): string {
  return toLocalInput(new Date());
}

function toLocalInput(d: Date): string {
  const adjusted = new Date(d.getTime() - d.getTimezoneOffset() * 60000);
  return adjusted.toISOString().slice(0, 16);
}

function rfc3339ToLocal(v: string): string {
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? "" : toLocalInput(d);
}
