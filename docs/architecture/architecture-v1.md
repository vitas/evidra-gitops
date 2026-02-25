# Architecture v1

Evidra-GitOps v1 is an Argo CD-first investigation and evidence layer. It derives lifecycle evidence from Argo CD operation history, revision metadata, and sync/health transitions, stores immutable records, and serves timeline/export APIs. The unified data model leverages the CNCF CloudEvents framework native integration.

## Scope
- Primary source: Argo CD operation/history and revision metadata.
- Supporting correlation: Argo Application annotations.
- Out of scope in v1: generic multi-provider ingestion, direct cluster-wide collectors as primary source, governance workflow execution.

## Component view
```text
Argo CD API/events -> Argo collector -> normalize/correlate -> append-only store
                                              |                  |
                                              +-> query API      +-> export builder
                                                           |
                                                     Explorer UI
```

## Core rules
- Change timeline is built from real lifecycle transitions.
- Event records are append-only.
- Event structure maps directly to the CNCF CloudEvents SDK for cross-system standard interoperability.
- Deterministic identifiers are required for Change and export references.
- UI is read-only; operational control stays in Argo CD.

## Observability

Evidra-GitOps uses OpenTelemetry as its single observability stack. All telemetry (traces, metrics, structured logs) flows through the OTel SDK.

### Architecture

```text
                              +-----------------+
                              | OTel Collector  |  (external, optional)
                              |  or Prometheus  |
                              +--------^--------+
                                       |
cmd/evidra (init) -> observability.InitTelemetry()
                         |
                         +-> TracerProvider (otlp | stdout | none)
                         +-> MeterProvider  (prometheus | none)
                         +-> W3C Propagator (TraceContext + Baggage)
                         |
                    otel.SetTracerProvider / otel.SetMeterProvider (global)
                         |
           +-------------+-------------------+
           |              |                  |
     otelhttp        application         otelsql
    (HTTP spans)     spans/metrics      (DB spans)
```

### Initialization

`observability.InitTelemetry()` is called in `cmd/evidra/main.go` before any other component starts. It builds the OTel `TracerProvider` and `MeterProvider` from `EVIDRA_OTEL_*` environment variables and registers them globally. Shutdown is deferred with a 5-second timeout.

### Tracing

Distributed tracing covers the full request lifecycle:

| Layer | Mechanism | Span examples |
|-------|-----------|---------------|
| HTTP | `otelhttp.NewHandler` wrapper in `bootstrap/runtime.go` | Auto: `GET /v1/changes`, `POST /v1/events` |
| API | Manual spans in `handlers_events.go` | `evidra.ingest` |
| Service | Manual spans in `app/service.go`, `app/changes.go` | `Service.IngestEvent`, `Service.ListChanges`, `Service.GetChange` |
| Argo collector | Manual spans in `ingest/argo/collector.go` | `evidra.argo.poll`, `evidra.argo.handleAppEvent` |
| Export | Manual spans in `export/fs.go` | `FilesystemExporter.CreateEvidencePack` |
| Database | `github.com/XSAM/otelsql` wrapping `database/sql` | Auto: `db.query`, `db.exec` |

W3C Trace Context propagation (`traceparent` / `tracestate` headers) is enabled by default, allowing correlation with upstream callers.

Sampling is configurable: `always_on`, `always_off`, `traceidratio`, `parentbased_traceidratio` (default).

### Metrics

Custom application metrics are defined in `internal/observability/metrics.go` using the OTel Meter API. All names use the `evidra.` prefix.

Categories:

- **Ingest**: `events_total`, `duration_seconds`, `batch_size`, `payload_bytes`, `integrity_conflicts_total`
- **Argo**: `polls_total`, `poll_duration_seconds`, `events_collected_total`, `normalize_errors_total`, `duplicates_skipped_total`, `checkpoint_saves_total`, `lag_seconds`
- **Store**: `operation.duration`, `operation.errors_total`
- **Webhook**: `received_total`, `auth_failures_total`, `parse_errors_total`, `events_produced_total`
- **Changes**: `query_duration_seconds`, `projection_duration_seconds`, `event_count`, `count`
- **Export**: `jobs_total`, `duration_seconds`, `artifact_bytes`, `events_per_pack`
- **Auth**: `decisions_total` (by decision + mechanism), `rate_limit_hits_total`
- **Runtime**: `info` (build info gauge, always 1)

HTTP-level metrics (`http.server.request.duration`, `http.server.active_requests`) and DB-level metrics (`db.sql.latency`, `db.sql.connection.*`) are provided automatically by `otelhttp` and `otelsql`.

Metrics are exposed at `GET /metrics` via the OTel Prometheus exporter.

### Structured logging

All server components use `go-logr/zapr` (zap production JSON backend). The logger is initialized in `cmd/evidra/main.go` and propagated through `bootstrap.NewRuntime` to the API server and other components. Audit auth events include structured fields: decision, mechanism, actor, roles, remote IP, request ID, and correlation ID.

### Import boundary rule

OTel SDK packages (`go.opentelemetry.io/otel/sdk`, `go.opentelemetry.io/otel/exporters`) are only imported in:

- `internal/observability` (SDK initialization)
- `internal/bootstrap` (wiring: `otelhttp`, `otelsql`)
- `cmd/` (entry point)

All other packages use only the OTel API (`go.opentelemetry.io/otel`, `.../trace`, `.../metric`). This is enforced by `TestOTelSDKImportsStayInAllowedLayers` in `internal/architecture`.

### Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `EVIDRA_OTEL_SERVICE_NAME` | `evidra` | OTel resource service name |
| `EVIDRA_OTEL_SERVICE_VERSION` | _(empty)_ | Reported service version |
| `EVIDRA_OTEL_ENVIRONMENT` | _(empty)_ | Deployment environment label |
| `EVIDRA_OTEL_TRACES_EXPORTER` | `none` | `none`, `otlp`, `stdout` |
| `EVIDRA_OTEL_METRICS_EXPORTER` | `prometheus` | `none`, `prometheus` |
| `EVIDRA_OTEL_EXPORTER_ENDPOINT` | `localhost:4317` | OTLP collector address |
| `EVIDRA_OTEL_EXPORTER_PROTOCOL` | `grpc` | OTLP transport protocol |
| `EVIDRA_OTEL_EXPORTER_INSECURE` | `false` | Skip TLS for OTLP exporter |
| `EVIDRA_OTEL_SAMPLER_TYPE` | `parentbased_traceidratio` | Trace sampling strategy |
| `EVIDRA_OTEL_SAMPLER_ARG` | `1.0` | Sampling ratio (0.0-1.0) |
