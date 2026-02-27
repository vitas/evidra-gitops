Part of the Evidra OSS toolset by SameBits.

# System Design: OpenTelemetry Integration for Evidra-GitOps

**Author:** System Architecture
**Date:** 2026-02-24
**Status:** Proposal
**Scope:** Traces, Metrics, Logs — all three OTel signals

---

## 1. Executive Summary

Evidra-GitOps currently has minimal observability: two Prometheus HTTP metrics (`evidra_http_requests_total`, `evidra_http_request_duration_seconds`) and ad-hoc structured logging via `logr/zap`. There is no distributed tracing, no database-level instrumentation, no pipeline metrics, and no correlation between logs and traces.

This document proposes integrating OpenTelemetry (OTel) as the unified observability framework across all three signal types — **traces**, **metrics**, and **logs**. The existing Prometheus `client_golang` instrumentation (`internal/observability/httpmetrics.go`, `promhttp.Handler()`) will be **deleted entirely** and replaced by OTel equivalents. There is no migration period or dual-metric phase.

### Why OpenTelemetry

| Concern | Current State | With OTel |
|---------|--------------|-----------|
| Metrics | Prometheus client_golang, HTTP only | OTel Metrics API with pluggable exporters (Prometheus, OTLP, stdout) |
| Tracing | None | Full request lifecycle tracing across HTTP → Service → Store → Collector |
| Logs | Mixed `log.Printf` + `logr/zap`, no trace correlation | OTel-aware structured logs with trace/span IDs |
| Correlation | None — metrics, logs, and errors are isolated | Trace IDs link metrics, logs, and spans into a single investigation |
| Vendor lock-in | Prometheus format only | OTLP protocol → any backend (Jaeger, Grafana Tempo, Datadog, etc.) |
| Ecosystem | Manual instrumentation only | Auto-instrumentation for `net/http`, `database/sql`, `pgx` |

### Key Benefits for Evidra-GitOps Specifically

1. **Investigation of Evidra-GitOps itself.** Evidra-GitOps's purpose is investigating production changes. The irony of having no observability into its own pipelines is a credibility gap. When Argo events stop appearing, operators need to know if the collector is failing, the normalizer is rejecting events, or the DB is slow.

2. **Ingest pipeline visibility.** The path from Argo CD watch event → normalize → integrity hash → store is completely opaque. Tracing reveals exactly where latency and errors occur.

3. **Change projection debugging.** The `/v1/changes` endpoint performs complex in-memory projections. Without tracing, performance regressions in projection logic are invisible.

4. **Multi-tenant SLA reporting.** OTel metrics with `subject`, `cluster`, and `provider` dimensions enable per-tenant throughput and latency reporting.

5. **Export pipeline monitoring.** Evidence pack generation can be slow for large time ranges. Operators need duration, size, and failure-rate metrics.

---

## 2. Architecture Overview

### 2.1 OTel SDK Initialization

A new `internal/observability/otel.go` module will initialize the OTel SDK and return a shutdown function. This runs at bootstrap before any other component starts. The current `httpmetrics.go` and all `promauto`/`promhttp` usage are deleted.

```
cmd/evidra/main.go
  → bootstrap.NewRuntime()
    → observability.InitTelemetry(ctx, cfg.Telemetry)  // returns shutdown func
    → buildRepository()                                 // wraps *sql.DB with otelsql
    → startArgoCollector()
    → api.NewServerWithOptions()
    → otelhttp middleware wraps server.Routes()
```

### 2.2 Signal Flow

```
┌─────────────────────────────────────────────────────────────┐
│                     Evidra-GitOps Process                    │
│                                                             │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐               │
│  │  Traces   │   │  Metrics  │   │   Logs   │               │
│  │  (spans)  │   │ (counters │   │ (logr →  │               │
│  │          │   │ histograms│   │  OTel    │               │
│  │          │   │  gauges)  │   │  bridge) │               │
│  └────┬─────┘   └────┬─────┘   └────┬─────┘               │
│       │              │              │                       │
│       ▼              ▼              ▼                       │
│  ┌─────────────────────────────────────────────┐           │
│  │           OTel SDK (BatchSpanProcessor,     │           │
│  │           PeriodicMetricReader)              │           │
│  └────────────────────┬────────────────────────┘           │
│                       │                                     │
│              ┌────────┴────────┐                           │
│              │   Exporters     │                           │
│              │                 │                           │
│              │  • OTLP/gRPC   │──→ Collector / Backend    │
│              │  • Prometheus   │──→ /metrics               │
│              │  • stdout (dev) │──→ console                │
│              └─────────────────┘                           │
└─────────────────────────────────────────────────────────────┘
```

The `/metrics` endpoint is now served by `go.opentelemetry.io/otel/exporters/prometheus`, not `promhttp`. The exposition format is the same (Prometheus text/OpenMetrics), but all metric names follow OTel semantic conventions.

### 2.3 Deployment Topology

**Option A — Direct export (simple deployments):**
Evidra-GitOps exports OTLP directly to the backend (Tempo, Jaeger, etc.) and exposes `/metrics` for Prometheus scraping.

**Option B — OTel Collector sidecar (production):**
Evidra-GitOps exports OTLP to a local OTel Collector, which handles sampling, batching, and routing to multiple backends. This is the recommended production topology.

```
Evidra-GitOps Pod
├── evidra-gitops (main container)
│   └── OTLP → localhost:4317
└── otel-collector (sidecar)
    ├── receivers: otlp
    ├── processors: batch, tail_sampling
    └── exporters: otlp/tempo, prometheus, loki
```

---

## 3. Configuration

All configuration via environment variables, consistent with existing Evidra-GitOps conventions.

```bash
# --- Telemetry control ---
EVIDRA_OTEL_SERVICE_NAME=evidra             # OTel service.name resource attribute
EVIDRA_OTEL_SERVICE_VERSION=                # Auto-detected from build info if empty
EVIDRA_OTEL_ENVIRONMENT=production          # deployment.environment resource attribute

# --- Trace export ---
EVIDRA_OTEL_TRACES_EXPORTER=otlp           # otlp | stdout | none
EVIDRA_OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317
EVIDRA_OTEL_EXPORTER_OTLP_PROTOCOL=grpc    # grpc | http/protobuf
EVIDRA_OTEL_EXPORTER_OTLP_INSECURE=false
EVIDRA_OTEL_TRACES_SAMPLER=parentbased_traceidratio
EVIDRA_OTEL_TRACES_SAMPLER_ARG=1.0         # Sample 100% in dev, lower in prod

# --- Metrics export ---
EVIDRA_OTEL_METRICS_EXPORTER=prometheus     # prometheus | otlp | stdout | none
EVIDRA_OTEL_METRICS_PROMETHEUS_PORT=        # If set, separate port; otherwise /metrics on main

# --- Logs ---
EVIDRA_OTEL_LOGS_EXPORTER=none             # otlp | stdout | none
EVIDRA_LOG_LEVEL=info                       # debug | info | warn | error
```

The naming convention follows the [OTel environment variable specification](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/) where applicable, prefixed with `EVIDRA_` for namespacing.

**No `EVIDRA_OTEL_ENABLED` toggle.** OTel is always initialized. To disable trace export, set `EVIDRA_OTEL_TRACES_EXPORTER=none`. To disable metrics export, set `EVIDRA_OTEL_METRICS_EXPORTER=none`. The SDK still initializes with no-op exporters, keeping instrumentation call-site overhead at ~1-2ns (no-op fast path).

---

## 4. Detailed Design

### 4.1 OTel SDK Bootstrap (`internal/observability/otel.go`)

```go
package observability

type TelemetryConfig struct {
    ServiceName       string
    ServiceVersion    string
    Environment       string
    TracesExporter    string   // "otlp", "stdout", "none"
    MetricsExporter   string   // "prometheus", "otlp", "stdout", "none"
    LogsExporter      string   // "otlp", "stdout", "none"
    OTLPEndpoint      string
    OTLPProtocol      string   // "grpc", "http/protobuf"
    OTLPInsecure      bool
    SamplerType       string
    SamplerArg        float64
    LogLevel          string
}

// InitTelemetry initializes OTel SDK and returns a shutdown function.
// Must be called before any other component initialization.
func InitTelemetry(ctx context.Context, cfg TelemetryConfig) (shutdown func(context.Context) error, err error)
```

**Responsibilities:**
1. Build `resource.Resource` with `service.name`, `service.version`, `deployment.environment`
2. Configure `TracerProvider` with chosen exporter + sampler
3. Configure `MeterProvider` with chosen exporter (Prometheus or OTLP)
4. Set global providers via `otel.SetTracerProvider()` / `otel.SetMeterProvider()`
5. Set global propagator: `propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})`
6. Return composite shutdown function

### 4.2 Code Deletions

The following are removed entirely:

| File / Symbol | Reason |
|--------------|--------|
| `internal/observability/httpmetrics.go` | Replaced by `otelhttp` middleware |
| `HTTPMetrics` struct, `NewHTTPMetrics()`, `statusWriter` | Replaced by `otelhttp` |
| `promhttp.Handler()` registration in `runtime.go` | Replaced by OTel Prometheus exporter handler |
| Direct imports of `prometheus/client_golang/prometheus` | No longer needed |
| Direct imports of `prometheus/client_golang/prometheus/promauto` | No longer needed |
| Direct imports of `prometheus/client_golang/prometheus/promhttp` | No longer needed |

`prometheus/client_golang` remains as a transitive dependency (used internally by the OTel Prometheus exporter) but Evidra-GitOps code no longer imports it directly.

### 4.3 HTTP Middleware

Replace the deleted `HTTPMetrics.Wrap()` with `otelhttp.NewHandler()`.

```go
// In bootstrap/runtime.go:
handler := otelhttp.NewHandler(server.Routes(), "evidra",
    otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
        return r.Method + " " + r.URL.Path
    }),
)

rootMux := http.NewServeMux()
rootMux.Handle("/metrics", promexporter.Handler())  // OTel Prometheus exporter
rootMux.Handle("/", handler)
```

**Metrics produced automatically by `otelhttp`:**
- `http.server.request.duration` (histogram)
- `http.server.request.body.size` (histogram)
- `http.server.response.body.size` (histogram)
- `http.server.active_requests` (up-down counter)

These replace the old `evidra_http_requests_total` and `evidra_http_request_duration_seconds`. The new names follow OTel HTTP semantic conventions.

**Span attributes on every HTTP span:**
- `http.method`, `http.route`, `http.status_code` (standard semconv)
- `evidra.request_id` (from header or generated)
- `evidra.auth.mechanism` (bearer/oidc/jwt/webhook/anonymous)
- `evidra.auth.actor` (if authenticated)

### 4.4 Database Instrumentation

#### 4.4.1 `database/sql` Auto-Instrumentation

Use `go.opentelemetry.io/contrib/instrumentation/database/sql/otelsql` to wrap the `*sql.DB` instance at creation time.

```go
// In buildRepository():
db, err := otelsql.Open(cfg.DBDriver, dsn,
    otelsql.WithAttributes(
        semconv.DBSystemPostgreSQL,  // or semconv.DBSystemSqlite
    ),
    otelsql.WithDBName("evidra"),
)
```

**Automatic spans created:**
- `db.query` — for every `QueryContext` / `ExecContext`
- `db.prepare` — for prepared statements
- `db.begin` / `db.commit` / `db.rollback` — transactions

**Automatic metrics:**
- `db.client.connections.open` (gauge) — pool size
- `db.client.connections.idle` (gauge)
- `db.client.connections.wait_time` (histogram) — time waiting for a connection

#### 4.4.2 Custom Store Metrics

Beyond auto-instrumentation, define application-level store metrics:

```go
var (
    storeOperationDuration = otel.Meter("evidra").Float64Histogram(
        "evidra.store.operation.duration",
        metric.WithDescription("Duration of store operations"),
        metric.WithUnit("s"),
    )
    storeOperationErrors = otel.Meter("evidra").Int64Counter(
        "evidra.store.operation.errors_total",
        metric.WithDescription("Total store operation errors"),
    )
)
```

**Labels:** `operation` (ingest, query_timeline, get_event, list_subjects, events_by_extension, create_export), `dialect` (postgres, sqlite, memory).

### 4.5 Ingest Pipeline Instrumentation

The ingest pipeline is Evidra-GitOps's most critical data path. Full tracing coverage:

```
HTTP POST /v1/events
  └─ span: "evidra.ingest"
       ├─ span: "evidra.ingest.parse"          (CloudEvent parsing)
       │    attributes: batch_size, content_type
       ├─ span: "evidra.ingest.validate"       (schema + integrity hash)
       │    attributes: event_type, subject
       ├─ span: "evidra.ingest.store"          (DB insert)
       │    attributes: status (accepted|duplicate|conflict)
       └─ event: "evidra.ingest.complete"
            attributes: accepted_count, duplicate_count, error_count
```

**Ingest metrics:**

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `evidra.ingest.events_total` | Counter | `status` (accepted/duplicate/conflict/error), `source`, `type` | Event throughput and deduplication rate |
| `evidra.ingest.duration_seconds` | Histogram | `source` | End-to-end ingest latency |
| `evidra.ingest.batch_size` | Histogram | `source` | Batch size distribution |
| `evidra.ingest.payload_bytes` | Histogram | — | Request payload size |
| `evidra.ingest.integrity_conflicts_total` | Counter | `source` | Same ID, different content (data integrity signal) |

### 4.6 Argo Collector Instrumentation

The Argo collector runs as a background goroutine. It needs its own trace roots and metrics.

**Traces:**

```
span: "evidra.argo.poll"  (one per poll cycle or informer event)
  ├─ span: "evidra.argo.fetch"         (API call or informer callback)
  │    attributes: app_count, mechanism (informer|rest)
  ├─ span: "evidra.argo.normalize"     (per application)
  │    attributes: app_name, cluster, history_entries
  ├─ span: "evidra.argo.ingest"        (per event)
  │    attributes: event_id, subject, status
  └─ span: "evidra.argo.checkpoint"    (save cursor)
       attributes: app_name, history_id
```

**Metrics:**

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `evidra.argo.polls_total` | Counter | `mechanism` (informer/rest), `status` (success/error) | Collector liveness |
| `evidra.argo.poll_duration_seconds` | Histogram | `mechanism` | Time per collection cycle |
| `evidra.argo.events_collected_total` | Counter | `app`, `cluster` | Events from Argo CD |
| `evidra.argo.normalize_errors_total` | Counter | `app`, `error_type` | Normalization failures |
| `evidra.argo.duplicates_skipped_total` | Counter | `app` | Checkpoint-based skips |
| `evidra.argo.checkpoint_saves_total` | Counter | `status` (success/error) | Checkpoint reliability |
| `evidra.argo.lag_seconds` | Gauge | `app` | Time since last successful ingest per app |

`evidra.argo.lag_seconds` is particularly critical — it answers "how stale is our data for this application?"

### 4.7 Webhook Pipeline Instrumentation

**Traces (extend HTTP parent span):**

```
span: "evidra.webhook.process"
  ├─ span: "evidra.webhook.authorize"    (HMAC verification)
  │    attributes: provider, mechanism
  ├─ span: "evidra.webhook.parse"        (adapter.Parse)
  │    attributes: provider, event_type, event_count
  └─ span: "evidra.webhook.ingest"       (per event)
```

**Metrics:**

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `evidra.webhook.received_total` | Counter | `provider`, `event_type` | Webhook traffic by provider |
| `evidra.webhook.auth_failures_total` | Counter | `provider`, `reason` | HMAC/auth failures |
| `evidra.webhook.parse_errors_total` | Counter | `provider`, `event_type` | Parser failures |
| `evidra.webhook.events_produced_total` | Counter | `provider` | Events generated per webhook |

### 4.8 Change Projection Instrumentation

The `/v1/changes` endpoint performs CPU-intensive in-memory projections. This is a likely performance bottleneck as event volume grows.

**Traces:**

```
span: "evidra.changes.list"
  ├─ span: "evidra.changes.query"        (fetch raw events from store)
  │    attributes: filter_subject, filter_cluster, time_range
  ├─ span: "evidra.changes.project"      (build Change structs)
  │    attributes: event_count, change_count
  └─ span: "evidra.changes.enrich"       (health transitions, approvals)
```

**Metrics:**

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `evidra.changes.query_duration_seconds` | Histogram | `endpoint` (list/get/timeline/evidence) | Query performance |
| `evidra.changes.projection_duration_seconds` | Histogram | — | Time in projection logic vs DB time |
| `evidra.changes.event_count` | Histogram | — | Events processed per change query |
| `evidra.changes.count` | Histogram | — | Changes returned per query |

### 4.9 Export Pipeline Instrumentation

**Traces:**

```
span: "evidra.export.create"
  ├─ span: "evidra.export.filter"       (select events for job)
  │    attributes: filter_json, event_count
  ├─ span: "evidra.export.generate"     (build evidence pack)
  │    attributes: format, event_count
  ├─ span: "evidra.export.write"        (write to filesystem)
  │    attributes: artifact_size_bytes, path
  └─ span: "evidra.export.checksum"     (compute SHA256)
```

**Metrics:**

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `evidra.export.jobs_total` | Counter | `status` (completed/failed), `format` | Export job outcomes |
| `evidra.export.duration_seconds` | Histogram | `format` | End-to-end export time |
| `evidra.export.artifact_bytes` | Histogram | `format` | Evidence pack size distribution |
| `evidra.export.events_per_pack` | Histogram | — | Events included per export |

### 4.10 Authentication & Authorization Instrumentation

**Metrics (augment existing audit logging):**

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `evidra.auth.decisions_total` | Counter | `decision` (allow/deny), `mechanism` (bearer/oidc/jwt/webhook/anonymous), `action` (read/ingest/export) | Auth decision rates |
| `evidra.auth.latency_seconds` | Histogram | `mechanism` | Auth overhead (especially JWKS fetch, OIDC validation) |
| `evidra.auth.rate_limit_hits_total` | Counter | `action` | Rate limiter activations |

### 4.11 Runtime & Health Metrics

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `evidra.uptime_seconds` | Gauge | — | Process uptime |
| `evidra.info` | Gauge | `version`, `go_version`, `db_dialect` | Build/runtime info (always 1) |
| `evidra.db.healthy` | Gauge | `dialect` | 1 if DB ping succeeds, 0 otherwise |

Go runtime metrics (`runtime/metrics`) are automatically exposed by the OTel SDK when using the Prometheus exporter.

### 4.12 Structured Logging with Trace Correlation

**Problem:** Current logging uses a mix of `log.Printf` and `logr/zap`. Neither injects trace IDs.

**Solution:** Bridge `logr` to OTel-aware logging that automatically attaches `trace_id` and `span_id` to every log line using `go.opentelemetry.io/contrib/bridges/otelzap`.

```go
// In main.go, after InitTelemetry:
zapLogger, _ := zap.NewProduction()
zapLogger = zapLogger.WithOptions(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
    return otelzap.NewCore("evidra", otelzap.WithCore(core))
}))
logger := zapr.NewLogger(zapLogger)
```

**All `log.Printf` calls are replaced:**
- `log.Printf` / `log.Fatalf` in `bootstrap/runtime.go` → `logger.Info` / `logger.Error`
- `log.Printf` in `api/audit.go` → structured logger
- This ensures 100% of log output carries trace context when available

---

## 5. Implementation Plan

### Phase 1 — Foundation (SDK + HTTP + Store + Logging)

**Scope:** OTel SDK init, delete old Prometheus code, HTTP middleware via otelhttp, database auto-instrumentation, logging unification.

**Files changed:**
- `internal/config/config.go` — add `Telemetry` config section
- `internal/observability/otel.go` — new: SDK initialization + Prometheus exporter handler
- `internal/observability/httpmetrics.go` — **delete entirely**
- `internal/bootstrap/runtime.go` — wire OTel init, replace `promhttp`/`HTTPMetrics` with otelhttp, wrap `*sql.DB` with otelsql, replace `log.Printf` with `logr`
- `cmd/evidra/main.go` — call shutdown on exit, set up otelzap bridge
- `internal/api/audit.go` — replace `log.Printf` with structured logger
- `go.mod` — add direct OTel deps, remove direct `prometheus/client_golang` import

**Outcome:** Every HTTP request creates a trace. DB queries appear as child spans. `/metrics` serves OTel-format metrics. All logs carry trace context. Old Prometheus code is gone.

### Phase 2 — Pipeline Instrumentation (Ingest + Argo + Webhooks)

**Scope:** Custom metrics and spans for the ingest pipeline, Argo collector, and webhook processing.

**Files changed:**
- `internal/api/handlers_events.go` — ingest spans + metrics
- `internal/app/service.go` — service-layer spans
- `internal/ingest/argo/collector.go` — collector spans + metrics
- `internal/ingest/registry.go` — webhook spans + metrics

**Outcome:** Full visibility into the event pipeline from source to storage.

### Phase 3 — Query & Export Instrumentation

**Scope:** Change projection tracing, export pipeline tracing, auth metrics.

**Files changed:**
- `internal/app/changes.go` — projection spans + metrics
- `internal/export/fs.go` — export spans + metrics
- `internal/api/auth.go` — auth metrics

**Outcome:** Complete observability across all subsystems.

---

## 6. Dependencies

### New Direct Dependencies

```
go.opentelemetry.io/otel                                    v1.36.0  (promote from indirect)
go.opentelemetry.io/otel/sdk                                v1.36.0
go.opentelemetry.io/otel/sdk/metric                         v1.36.0
go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc
go.opentelemetry.io/otel/exporters/prometheus
go.opentelemetry.io/otel/exporters/stdout/stdouttrace       (dev/testing)
go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp  v0.61.0  (promote from indirect)
go.opentelemetry.io/contrib/instrumentation/database/sql/otelsql
go.opentelemetry.io/contrib/bridges/otelzap
```

Most of these are already transitive dependencies. The actual new code pulled in is minimal.

### Removed Direct Dependencies

```
github.com/prometheus/client_golang/prometheus          (direct import removed)
github.com/prometheus/client_golang/prometheus/promauto (direct import removed)
github.com/prometheus/client_golang/prometheus/promhttp (direct import removed)
```

`prometheus/client_golang` remains in `go.mod` as a transitive dependency of the OTel Prometheus exporter, but Evidra-GitOps code no longer imports it.

---

## 7. Metric Naming Conventions

All custom Evidra-GitOps metrics follow these conventions:

- **Namespace:** `evidra.` prefix for all custom metrics
- **Subsystem:** component name (`ingest`, `argo`, `store`, `changes`, `export`, `auth`, `webhook`)
- **Unit suffix:** `_seconds` for durations, `_bytes` for sizes, `_total` for counters (Prometheus convention via OTel)
- **Standard OTel semantic conventions** for HTTP (`http.server.*`) and DB (`db.client.*`) metrics — no custom prefix

Example metric names in Prometheus exposition format:
```
evidra_ingest_events_total{status="accepted",source="argocd",type="deployment.synced"}
evidra_argo_poll_duration_seconds_bucket{mechanism="informer",le="0.5"}
evidra_store_operation_duration_seconds_bucket{operation="query_timeline",dialect="postgres",le="0.1"}
evidra_changes_projection_duration_seconds_bucket{le="1.0"}
evidra_export_duration_seconds_bucket{format="json",le="10"}
http_server_request_duration_seconds_bucket{http_method="GET",http_route="/v1/changes",le="0.5"}
```

---

## 8. Cardinality Management

High cardinality labels are the primary operational risk with metrics. Guidelines:

| Label | Allowed Values | Cardinality |
|-------|---------------|-------------|
| `status` | accepted, duplicate, conflict, error | 4 |
| `source` | argocd, github, gitlab, bitbucket, kubernetes, api | ~6 |
| `type` | bounded set of CloudEvent types | ~15 |
| `operation` | bounded set of store methods | ~10 |
| `mechanism` | bearer, oidc, jwt, webhook, anonymous | 5 |
| `provider` | github, gitlab, bitbucket, kubernetes | 4 |
| `app` | **CAUTION** — unbounded. Only on Argo gauge metrics, not counters/histograms | varies |

**Rules:**
- Never use `event_id`, `user_id`, `trace_id`, or `subject` as metric labels
- `app` label is only permitted on `evidra.argo.lag_seconds` (gauge) where per-app granularity is essential. Operators with >100 apps should use OTLP export with exemplars instead
- Use span attributes (not metric labels) for high-cardinality dimensions
- Enable exemplars on histograms to link metric samples to traces without adding labels

---

## 9. Sampling Strategy

| Environment | Sampler | Rate | Rationale |
|-------------|---------|------|-----------|
| Development | `always_on` | 100% | Full visibility during development |
| Staging | `parentbased_traceidratio` | 50% | Balance visibility and overhead |
| Production | `parentbased_traceidratio` | 10% | Baseline sampling |
| Production (with OTel Collector) | `parentbased_always_on` + tail sampling | varies | Collector-side tail sampling: keep 100% of errors, slow requests, and a % of healthy requests |

**Tail sampling rules (OTel Collector config):**

```yaml
processors:
  tail_sampling:
    policies:
      - name: errors
        type: status_code
        status_code: { status_codes: [ERROR] }
      - name: slow-requests
        type: latency
        latency: { threshold_ms: 2000 }
      - name: baseline
        type: probabilistic
        probabilistic: { sampling_percentage: 10 }
```

This ensures all errors and slow requests are captured while keeping storage costs manageable.

---

## 10. Context Propagation

### HTTP Inbound

`otelhttp` middleware automatically extracts `traceparent` / `tracestate` headers (W3C Trace Context) from incoming requests. This means:

- External systems calling `POST /v1/events` can propagate their trace context into Evidra-GitOps
- The Evidra-GitOps UI (fetch requests) can include trace context if instrumented with OTel JS SDK
- Argo CD webhook calls don't currently send trace headers, but will get their own root spans

### Internal Propagation

All internal method calls pass `context.Context`. The OTel SDK propagates trace/span through context automatically. No code changes needed for propagation — only for creating child spans.

### Argo Collector

The collector creates root spans (no incoming HTTP context). These are linked to the overall collection cycle. Each ingested event's span can carry an `evidra.argo.source_event_time` attribute linking it to the Argo CD event timestamp for correlation.

---

## 11. Testing Strategy

1. **Unit tests:** Mock `TracerProvider` and `MeterProvider` to verify spans and metrics are created with correct attributes. OTel provides `go.opentelemetry.io/otel/sdk/trace/tracetest` for this.

2. **Integration tests:** Use `sdktrace.NewTracerProvider(sdktrace.WithSyncer(tracetest.NewInMemoryExporter()))` to capture spans in-memory and assert on the span tree structure.

3. **Boundary tests:** Add `internal/architecture` boundary test to ensure `internal/store`, `internal/ingest`, and `internal/app` only import OTel API packages (`go.opentelemetry.io/otel`), never SDK packages (`go.opentelemetry.io/otel/sdk`). SDK initialization stays in `internal/observability` and `internal/bootstrap`.

4. **E2E validation:** Add a smoke test to `make evidra-demo-test` that curls `/metrics` and verifies key OTel metrics are present (e.g., `http_server_request_duration_seconds`, `evidra_ingest_events_total`).

---

## 12. Observability Dashboard Recommendations

### Ingest Health Dashboard

- **Events/sec** by source and type (counter rate)
- **Duplicate rate** — `rate(evidra_ingest_events_total{status="duplicate"})` / total rate
- **Integrity conflicts** — alert if > 0 (data corruption signal)
- **Ingest latency p50/p95/p99** — histogram quantiles
- **Argo collector lag** per application — gauge, alert if > 5 minutes

### API Performance Dashboard

- **Request rate** by endpoint and status code
- **Latency p50/p95/p99** by endpoint
- **Active requests** gauge
- **Error rate** — 5xx / total
- **Change projection time** vs DB query time (identifies bottleneck)

### Infrastructure Dashboard

- **DB connection pool** — open, idle, waiting
- **DB query latency** by operation
- **Export job duration and size**
- **Auth decision rate** and rate-limiter hits
- **Go runtime** — goroutines, GC pause, heap size

---

## 13. Alerting Recommendations

| Alert | Condition | Severity |
|-------|-----------|----------|
| Ingest pipeline down | `rate(evidra_ingest_events_total[5m]) == 0` for > 10m | Critical |
| Argo collector stale | `evidra_argo_lag_seconds > 300` | Warning |
| Argo collector errors | `rate(evidra_argo_normalize_errors_total[5m]) > 0.1` | Warning |
| High ingest latency | `histogram_quantile(0.99, evidra_ingest_duration_seconds) > 5` | Warning |
| Integrity conflicts | `increase(evidra_ingest_integrity_conflicts_total[1h]) > 0` | Critical |
| DB connection pool exhausted | `db_client_connections_idle == 0` for > 1m | Critical |
| Export failures | `rate(evidra_export_jobs_total{status="failed"}[1h]) > 0` | Warning |
| Auth rate-limit spike | `rate(evidra_auth_rate_limit_hits_total[5m]) > 10` | Warning |
| API error rate | `sum(rate(http_server_request_duration_seconds_count{http_status_code=~"5.."}[5m])) / sum(rate(http_server_request_duration_seconds_count[5m])) > 0.05` | Critical |

---

## 14. Complete Metrics Catalog

### HTTP (OTel Semconv — Automatic via `otelhttp`)
| Metric | Type |
|--------|------|
| `http.server.request.duration` | Histogram |
| `http.server.request.body.size` | Histogram |
| `http.server.response.body.size` | Histogram |
| `http.server.active_requests` | UpDownCounter |

### Database (OTel Semconv — Automatic via `otelsql`)
| Metric | Type |
|--------|------|
| `db.client.connections.open` | Gauge |
| `db.client.connections.idle` | Gauge |
| `db.client.connections.wait_time` | Histogram |

### Ingest Pipeline (Custom)
| Metric | Type |
|--------|------|
| `evidra.ingest.events_total` | Counter |
| `evidra.ingest.duration_seconds` | Histogram |
| `evidra.ingest.batch_size` | Histogram |
| `evidra.ingest.payload_bytes` | Histogram |
| `evidra.ingest.integrity_conflicts_total` | Counter |

### Argo Collector (Custom)
| Metric | Type |
|--------|------|
| `evidra.argo.polls_total` | Counter |
| `evidra.argo.poll_duration_seconds` | Histogram |
| `evidra.argo.events_collected_total` | Counter |
| `evidra.argo.normalize_errors_total` | Counter |
| `evidra.argo.duplicates_skipped_total` | Counter |
| `evidra.argo.checkpoint_saves_total` | Counter |
| `evidra.argo.lag_seconds` | Gauge |

### Store (Custom)
| Metric | Type |
|--------|------|
| `evidra.store.operation.duration` | Histogram |
| `evidra.store.operation.errors_total` | Counter |

### Changes (Custom)
| Metric | Type |
|--------|------|
| `evidra.changes.query_duration_seconds` | Histogram |
| `evidra.changes.projection_duration_seconds` | Histogram |
| `evidra.changes.event_count` | Histogram |
| `evidra.changes.count` | Histogram |

### Webhooks (Custom)
| Metric | Type |
|--------|------|
| `evidra.webhook.received_total` | Counter |
| `evidra.webhook.auth_failures_total` | Counter |
| `evidra.webhook.parse_errors_total` | Counter |
| `evidra.webhook.events_produced_total` | Counter |

### Export (Custom)
| Metric | Type |
|--------|------|
| `evidra.export.jobs_total` | Counter |
| `evidra.export.duration_seconds` | Histogram |
| `evidra.export.artifact_bytes` | Histogram |
| `evidra.export.events_per_pack` | Histogram |

### Auth (Custom)
| Metric | Type |
|--------|------|
| `evidra.auth.decisions_total` | Counter |
| `evidra.auth.latency_seconds` | Histogram |
| `evidra.auth.rate_limit_hits_total` | Counter |

### Runtime (Custom)
| Metric | Type |
|--------|------|
| `evidra.uptime_seconds` | Gauge |
| `evidra.info` | Gauge |
| `evidra.db.healthy` | Gauge |

**Total: 8 automatic + 30 custom = 38 metrics**

---

## 15. Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|-----------|
| OTel SDK overhead in hot path (ingest) | Increased p99 latency | Benchmark before/after. Batch span processor amortizes export cost. Set `TRACES_EXPORTER=none` if unacceptable. |
| Metric cardinality explosion | Prometheus OOM | Strict label policy (Section 8). No unbounded labels on counters/histograms. |
| OTLP exporter blocking on network issues | Backpressure on ingest | Use async `BatchSpanProcessor` with bounded queue. Dropped spans are counted via `otel.Handle` error handler. |
| Increased binary size from OTel deps | Slower container pulls | Most deps are already indirect. Measured impact: ~2-4MB. Acceptable. |
| Trace storage costs in production | Infrastructure cost | Tail sampling (Section 9) keeps only errors + slow + 10% baseline. |

---

## 16. Decision Log

| Decision | Chosen | Alternatives Considered | Rationale |
|----------|--------|------------------------|-----------|
| Clean replacement, no migration | Delete old Prometheus code | Dual-metric transition period | Simpler codebase, no dead code, no confusion about which metrics are canonical |
| OTel over pure Prometheus | OTel with pluggable exporters | Stay Prometheus-only; Add Jaeger client directly | OTel provides all three signals, vendor-neutral, and subsumes Prometheus |
| `otelhttp` middleware | otelhttp contrib | Custom middleware using OTel API | otelhttp is battle-tested, follows semconv, and is already a transitive dep |
| `otelsql` for DB | otelsql contrib | Manual span creation in Repository methods | Auto-instrumentation catches all queries including ones we might miss manually |
| OTel Prometheus exporter for `/metrics` | `otel/exporters/prometheus` | OTLP-only metrics | Prometheus scraping is the dominant collection pattern in Kubernetes environments |
| `parentbased_traceidratio` sampler | Parent-based + ratio | Always-on; Probability-only | Respects upstream sampling decisions; ratio controls local cost |
| 3 implementation phases | Incremental | Big bang | Each phase is independently valuable and testable |
| Always-on SDK (no master toggle) | Per-exporter `none` option | `OTEL_ENABLED=true/false` master switch | Avoids conditional instrumentation code; `none` exporter has near-zero cost |
