import type { ChangeDetail, EventItem } from "../types";

/** Create a minimal ChangeDetail for use in tests. Override any field. */
export function makeChange(overrides: Partial<ChangeDetail> = {}): ChangeDetail {
  return {
    id: "chg_demo",
    change_id: "chg_demo",
    subject: "payments-api",
    application: "payments-api",
    target_cluster: "eu-1",
    namespace: "prod-eu",
    primary_provider: "argo",
    result_status: "succeeded",
    health_status: "healthy",
    started_at: "2026-02-16T12:00:00Z",
    completed_at: "2026-02-16T12:02:00Z",
    event_count: 1,
    ...overrides,
  };
}

/** Create a minimal EventItem for use in tests. Override any field. */
export function makeEvent(overrides: Partial<EventItem> = {}): EventItem {
  return {
    id: "evt_1",
    source: "argocd",
    type: "argo.sync.finished",
    time: "2026-02-16T12:02:00Z",
    subject: "payments-api",
    extensions: { status: "Succeeded" },
    ...overrides,
  };
}
