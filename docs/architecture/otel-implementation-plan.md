# OpenTelemetry Integration — Implementation Plan

**Ready-to-go instructions for developers.**
Read the [design document](otel-integration.md) first for rationale and metric definitions. This document tells you *exactly what to change, in what order, and how to verify each step*.

---

## Prerequisites

```bash
# Verify you can build and test before starting
go build -o evidra-gitops ./cmd/evidra-gitops
go test ./...
make boundary-check
```

Keep these passing after every step. Do not batch multiple steps without verifying.

---

## Phase 1: Foundation — SDK + HTTP + DB + Logging

**Goal:** OTel SDK initializes at startup. HTTP requests produce traces and metrics via `otelhttp`. DB queries produce child spans via `otelsql`. All `log.Printf` calls replaced with structured logger. Old Prometheus code deleted.

---

### Step 1.1: Add OTel config struct and env vars

**File:** `internal/config/config.go`

1. Add a new struct after `TLSConfig` (after line 127):

```go
type OTelConfig struct {
	ServiceName    string        `mapstructure:"service_name"`
	ServiceVersion string        `mapstructure:"service_version"`
	Environment    string        `mapstructure:"environment"`
	TracesExporter string        `mapstructure:"traces_exporter"` // otlp | stdout | none
	MetricsExporter string       `mapstructure:"metrics_exporter"` // prometheus | otlp | stdout | none
	LogsExporter   string        `mapstructure:"logs_exporter"`   // otlp | stdout | none
	ExporterEndpoint string      `mapstructure:"exporter_endpoint"`
	ExporterProtocol string      `mapstructure:"exporter_protocol"` // grpc | http/protobuf
	ExporterInsecure bool        `mapstructure:"exporter_insecure"`
	SamplerType    string        `mapstructure:"sampler_type"`
	SamplerArg     float64       `mapstructure:"sampler_arg"`
	LogLevel       string        `mapstructure:"log_level"`
}
```

2. Add field to `Config` struct (line 33, after `TLS`):

```go
OTel OTelConfig `mapstructure:"otel"`
```

3. In `LoadFromEnv()`, add defaults after the TLS defaults (after line 159):

```go
v.SetDefault("otel.service_name", "evidra")
v.SetDefault("otel.service_version", "")
v.SetDefault("otel.environment", "")
v.SetDefault("otel.traces_exporter", "none")
v.SetDefault("otel.metrics_exporter", "prometheus")
v.SetDefault("otel.logs_exporter", "none")
v.SetDefault("otel.exporter_endpoint", "localhost:4317")
v.SetDefault("otel.exporter_protocol", "grpc")
v.SetDefault("otel.exporter_insecure", false)
v.SetDefault("otel.sampler_type", "parentbased_traceidratio")
v.SetDefault("otel.sampler_arg", 1.0)
v.SetDefault("otel.log_level", "info")
```

4. Add explicit viper bindings (after line 183):

```go
viper.BindEnv("otel.service_name", "EVIDRA_OTEL_SERVICE_NAME")
viper.BindEnv("otel.service_version", "EVIDRA_OTEL_SERVICE_VERSION")
viper.BindEnv("otel.environment", "EVIDRA_OTEL_ENVIRONMENT")
viper.BindEnv("otel.traces_exporter", "EVIDRA_OTEL_TRACES_EXPORTER")
viper.BindEnv("otel.metrics_exporter", "EVIDRA_OTEL_METRICS_EXPORTER")
viper.BindEnv("otel.logs_exporter", "EVIDRA_OTEL_LOGS_EXPORTER")
viper.BindEnv("otel.exporter_endpoint", "EVIDRA_OTEL_EXPORTER_OTLP_ENDPOINT")
viper.BindEnv("otel.exporter_protocol", "EVIDRA_OTEL_EXPORTER_OTLP_PROTOCOL")
viper.BindEnv("otel.exporter_insecure", "EVIDRA_OTEL_EXPORTER_OTLP_INSECURE")
viper.BindEnv("otel.sampler_type", "EVIDRA_OTEL_TRACES_SAMPLER")
viper.BindEnv("otel.sampler_arg", "EVIDRA_OTEL_TRACES_SAMPLER_ARG")
viper.BindEnv("otel.log_level", "EVIDRA_LOG_LEVEL")
```

5. Add the `OTel` field to the `Summary()` output in `StartupSummary` struct:

```go
OTelTracesExporter  string
OTelMetricsExporter string
```

And populate in `Summary()`:

```go
OTelTracesExporter:  c.OTel.TracesExporter,
OTelMetricsExporter: c.OTel.MetricsExporter,
```

**Verify:**
```bash
go build ./internal/config/...
go test ./internal/config/...
```

---

### Step 1.2: Create OTel SDK initialization module

**New file:** `internal/observability/otel.go`

This is the core OTel bootstrap. It creates the `TracerProvider`, `MeterProvider`, sets global propagators, and returns a shutdown function.

```go
package observability

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"evidra/internal/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// TelemetryProviders holds initialized OTel providers and their HTTP handler
// for the /metrics endpoint.
type TelemetryProviders struct {
	MetricsHandler http.Handler // serves /metrics if prometheus exporter is used
	shutdown       func(context.Context) error
}

// Shutdown gracefully flushes and shuts down all providers.
func (tp *TelemetryProviders) Shutdown(ctx context.Context) error {
	if tp.shutdown != nil {
		return tp.shutdown(ctx)
	}
	return nil
}

// InitTelemetry initializes the OTel SDK. Must be called before any other
// component initialization. Returns TelemetryProviders with a Shutdown method.
func InitTelemetry(ctx context.Context, cfg config.OTelConfig) (*TelemetryProviders, error) {
	// ... build resource, tracer provider, meter provider
	// ... set otel.SetTracerProvider, otel.SetMeterProvider
	// ... set propagator: W3C TraceContext + Baggage
	// ... return TelemetryProviders with MetricsHandler and shutdown func
}
```

**Implementation details for `InitTelemetry`:**

1. **Build resource:**
```go
res, err := resource.Merge(
	resource.Default(),
	resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.ServiceVersion),
		semconv.DeploymentEnvironmentName(cfg.Environment),
	),
)
```

2. **Build trace exporter** based on `cfg.TracesExporter`:
   - `"otlp"` → `otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(cfg.ExporterEndpoint), ...)`. If `cfg.ExporterInsecure`, add `otlptracegrpc.WithInsecure()`.
   - `"stdout"` → `stdouttrace.New(stdouttrace.WithPrettyPrint())`
   - `"none"` → nil (no exporter, no-op tracer)

3. **Build sampler** based on `cfg.SamplerType`:
   - `"always_on"` → `sdktrace.AlwaysSample()`
   - `"always_off"` → `sdktrace.NeverSample()`
   - `"traceidratio"` → `sdktrace.TraceIDRatioBased(cfg.SamplerArg)`
   - `"parentbased_traceidratio"` (default) → `sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SamplerArg))`

4. **Build TracerProvider:**
```go
tp := sdktrace.NewTracerProvider(
	sdktrace.WithResource(res),
	sdktrace.WithBatcher(traceExporter), // or WithSyncer for stdout
	sdktrace.WithSampler(sampler),
)
otel.SetTracerProvider(tp)
```

5. **Build MeterProvider** based on `cfg.MetricsExporter`:
   - `"prometheus"` → use `promexporter.New()`. Store the returned `http.Handler` in `TelemetryProviders.MetricsHandler`.
   - `"otlp"` → use OTLP metric exporter with `metric.NewPeriodicReader`.
   - `"none"` → default no-op meter provider.

```go
promExp, err := promexporter.New()
mp := metric.NewMeterProvider(
	metric.WithResource(res),
	metric.WithReader(promExp),
)
otel.SetMeterProvider(mp)
// promExp implements http.Handler for the /metrics endpoint
```

6. **Set propagator:**
```go
otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
	propagation.TraceContext{},
	propagation.Baggage{},
))
```

7. **Return shutdown function** that calls `tp.Shutdown(ctx)` then `mp.Shutdown(ctx)`.

**Verify:**
```bash
go build ./internal/observability/...
```

---

### Step 1.3: Delete old Prometheus metrics code

**File to delete:** `internal/observability/httpmetrics.go`

Delete the entire file. It contains `HTTPMetrics`, `NewHTTPMetrics()`, `statusWriter` — all replaced by `otelhttp`.

**Verify:** Build will fail at this point (expected — `runtime.go` still references it). Proceed to step 1.4.

---

### Step 1.4: Rewire bootstrap/runtime.go

**File:** `internal/bootstrap/runtime.go`

This is the most significant single-file change. The current file:
- Line 20: imports `"evidra/internal/observability"`
- Line 24: imports `"github.com/prometheus/client_golang/prometheus/promhttp"`
- Line 88: `metrics := observability.NewHTTPMetrics()`
- Line 90: `rootMux.Handle("/metrics", promhttp.Handler())`
- Line 91: `rootMux.Handle("/", metrics.Wrap(server.Routes()))`

**Changes:**

1. **Update imports.** Remove:
```go
"github.com/prometheus/client_golang/prometheus/promhttp"
```
Add:
```go
"evidra/internal/observability"
"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
```
(The `observability` import stays but usage changes.)

2. **Change `NewRuntime` signature** to accept `*observability.TelemetryProviders`:

```go
func NewRuntime(ctx context.Context, cfg config.Config, logger logr.Logger, telemetry *observability.TelemetryProviders) *Runtime {
```

3. **Replace the HTTP wiring section** (lines 88-91). Remove:
```go
metrics := observability.NewHTTPMetrics()
rootMux := http.NewServeMux()
rootMux.Handle("/metrics", promhttp.Handler())
rootMux.Handle("/", metrics.Wrap(server.Routes()))
```
Replace with:
```go
handler := otelhttp.NewHandler(server.Routes(), "evidra",
	otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
		return r.Method + " " + r.URL.Path
	}),
)

rootMux := http.NewServeMux()
if telemetry.MetricsHandler != nil {
	rootMux.Handle("/metrics", telemetry.MetricsHandler)
}
rootMux.Handle("/", handler)
```

4. **Wrap `*sql.DB` with `otelsql`** in `buildRepository()`. Change line 120:

Before:
```go
db, err := sql.Open(cfg.DBDriver, dsn)
```

After:
```go
db, err := otelsql.Open(cfg.DBDriver, dsn,
	otelsql.WithAttributes(semconv.DBSystemKey.String(dbSystem(cfg.DBDialect))),
	otelsql.WithDBName("evidra"),
)
```

Add import:
```go
"go.opentelemetry.io/contrib/instrumentation/database/sql/otelsql"
semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
```

Add helper:
```go
func dbSystem(dialect string) string {
	switch strings.ToLower(dialect) {
	case "postgres", "pgx":
		return "postgresql"
	case "sqlite":
		return "sqlite"
	default:
		return dialect
	}
}
```

5. **Replace `log.Printf` with structured logger.** `buildRepository` currently uses `log.Printf` (lines 106, 113, 149). It does not receive a logger. Change `buildRepository` signature:

```go
func buildRepository(ctx context.Context, cfg config.Config, logger logr.Logger) (store.Repository, func()) {
```

Replace:
- `log.Printf("running with in-memory repository")` → `logger.Info("running with in-memory repository")`
- `log.Printf("WARNING: ..."` → `logger.Info("WARNING: ...", "mode", "fallback")`
- `log.Fatalf(...)` → `logger.Error(fmt.Errorf(format, args...), "fatal: db init")` then `os.Exit(1)`
- `log.Printf("running with SQL repository: dialect=%s", cfg.DBDialect)` → `logger.Info("running with SQL repository", "dialect", cfg.DBDialect)`

Update `NewRuntime` call:
```go
repo, cleanup := buildRepository(ctx, cfg, logger)
```

6. **Remove the `log` import** from runtime.go (currently used at line 7). After all `log.Printf` calls are replaced, the `"log"` import is no longer needed.

**Verify:**
```bash
go build ./internal/bootstrap/...
go build -o evidra-gitops ./cmd/evidra-gitops
```

---

### Step 1.5: Update main.go to init OTel and pass to bootstrap

**File:** `cmd/evidra/main.go`

1. **Add imports:**
```go
"evidra/internal/observability"
```

2. **After logger creation (line 29), before `bootstrap.NewRuntime` (line 31), add:**
```go
telemetry, err := observability.InitTelemetry(ctx, cfg.OTel)
if err != nil {
	logger.Error(err, "failed to initialize telemetry")
	log.Fatal(err)
}
defer func() {
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := telemetry.Shutdown(shutdownCtx); err != nil {
		logger.Error(err, "telemetry shutdown error")
	}
}()
```

3. **Update the NewRuntime call (line 31):**
```go
rt := bootstrap.NewRuntime(ctx, cfg, logger, telemetry)
```

4. **Add OTel info to startup log** (after line 47):
```go
"otel_traces", cfg.OTel.TracesExporter,
"otel_metrics", cfg.OTel.MetricsExporter,
```

**Verify:**
```bash
go build -o evidra-gitops ./cmd/evidra-gitops
go test ./...
```

---

### Step 1.6: Replace log.Printf in audit.go

**File:** `internal/api/audit.go`

The `auditAuth` method (line 47) and `writeAuditLine` (line 73) use `log.Printf` / `log.Print`. The `Server` struct does not carry a logger.

1. **Add a logger field to `Server` struct** in `internal/api/server.go` (line 71):
```go
type Server struct {
	service         *app.Service
	auth            AuthConfig
	webhookRegistry *ingest.Registry
	argoOnlyMode    bool
	rateLimiter     *authRateLimiter
	jwksCache       *jwksKeyCache
	jwksMu          sync.Mutex
	logger          logr.Logger
}
```

2. **Add `Logger` to `ServerOptions`** (line 65):
```go
type ServerOptions struct {
	Auth            AuthConfig
	WebhookRegistry *ingest.Registry
	ArgoOnlyMode    bool
	Logger          logr.Logger
}
```

3. **Wire in `NewServerWithOptions`** (line 85):
```go
return &Server{
	...
	logger: opts.Logger,
}
```

4. **Pass logger from `runtime.go`** — in the `NewServerWithOptions` call (runtime.go line 47), add:
```go
server := api.NewServerWithOptions(repo, exporter, api.ServerOptions{
	...
	Logger: logger.WithName("api"),
})
```

5. **In `audit.go`**, replace `log.Print(line)` (line 47) with:
```go
s.logger.Info("audit_auth", "event", string(b))
```

Replace `log.Printf("audit_auth ...")` (line 43) with:
```go
s.logger.Info("audit_auth", "decision", ev.Decision, "mechanism", ev.Mechanism, "path", ev.Path, "reason", ev.Reason)
```

Replace `log.Printf("audit_auth_file_error ...")` (line 73) with:
```go
s.logger.Error(err, "audit file write failed", "path", path)
```

6. **Remove `"log"` import** from audit.go.

**Verify:**
```bash
go build ./internal/api/...
go test ./internal/api/...
```

---

### Step 1.7: Add dependencies to go.mod

**Run:**
```bash
go get go.opentelemetry.io/otel@v1.36.0
go get go.opentelemetry.io/otel/sdk@v1.36.0
go get go.opentelemetry.io/otel/sdk/metric@v1.36.0
go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc
go get go.opentelemetry.io/otel/exporters/prometheus
go get go.opentelemetry.io/otel/exporters/stdout/stdouttrace
go get go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@v0.61.0
go get go.opentelemetry.io/contrib/instrumentation/database/sql/otelsql
go get go.opentelemetry.io/contrib/bridges/otelzap
go mod tidy
```

**Verify:**
```bash
go build -o evidra-gitops ./cmd/evidra-gitops
go test ./...
make boundary-check
```

---

### Step 1.8: Add boundary test for OTel SDK imports

**File:** `internal/architecture/boundaries_test.go`

Add a new test function after the existing `TestProviderImportsStayInAllowedLayers`:

```go
func TestOTelSDKImportsStayInAllowedLayers(t *testing.T) {
	root := filepath.Join("..", "..")
	var violations []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "vendor" || base == "__internal" || base == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		normalized := filepath.ToSlash(path)
		// Only observability and bootstrap may import OTel SDK packages
		allowed := strings.HasPrefix(normalized, "../../internal/observability/") ||
			strings.HasPrefix(normalized, "../../internal/bootstrap/") ||
			strings.HasPrefix(normalized, "../../cmd/")
		if allowed {
			return nil
		}

		fset := token.NewFileSet()
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imp := range f.Imports {
			pkg := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(pkg, "go.opentelemetry.io/otel/sdk") ||
				strings.HasPrefix(pkg, "go.opentelemetry.io/otel/exporters") {
				violations = append(violations, normalized+" imports "+pkg)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk failed: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("OTel SDK boundary violations (only observability/bootstrap/cmd may import SDK):\n%s", strings.Join(violations, "\n"))
	}
}
```

**Verify:**
```bash
make boundary-check
```

---

### Step 1.9: Phase 1 integration test

Run the full suite:

```bash
go build -o evidra-gitops ./cmd/evidra-gitops
go test ./...
make boundary-check
```

**Manual smoke test:**

```bash
# Start with defaults (traces=none, metrics=prometheus)
EVIDRA_DEV_INSECURE=true ./evidra-gitops &

# Verify /metrics returns OTel-format metrics
curl -s http://localhost:8080/metrics | grep http_server

# Verify /healthz works (produces a trace if exporter is not none)
curl http://localhost:8080/healthz

# Verify no old metric names
curl -s http://localhost:8080/metrics | grep evidra_http_requests_total
# Should return nothing

kill %1
```

**With stdout trace exporter (dev mode):**
```bash
EVIDRA_DEV_INSECURE=true EVIDRA_OTEL_TRACES_EXPORTER=stdout ./evidra-gitops &
curl http://localhost:8080/healthz
# Should see trace JSON output on stdout
kill %1
```

---

## Phase 2: Pipeline Instrumentation — Ingest + Argo + Webhooks

**Goal:** Custom spans and metrics in the ingest pipeline, Argo collector, and webhook processing. After this phase, every event's journey from source to storage is fully traceable.

---

### Step 2.1: Define custom metrics in observability package

**New file:** `internal/observability/metrics.go`

Define all custom OTel meters and instruments. This is a central registry — other packages obtain instruments by calling functions here. This keeps the OTel API import in one place within the observability layer.

```go
package observability

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

var meter = otel.Meter("evidra")

// Ingest metrics
var (
	IngestEventsTotal        metric.Int64Counter
	IngestDuration           metric.Float64Histogram
	IngestBatchSize          metric.Int64Histogram
	IngestPayloadBytes       metric.Int64Histogram
	IngestIntegrityConflicts metric.Int64Counter
)

// Argo collector metrics
var (
	ArgoPollsTotal           metric.Int64Counter
	ArgoPollDuration         metric.Float64Histogram
	ArgoEventsCollected      metric.Int64Counter
	ArgoNormalizeErrors      metric.Int64Counter
	ArgoDuplicatesSkipped    metric.Int64Counter
	ArgoCheckpointSaves      metric.Int64Counter
	ArgoLagSeconds           metric.Float64Gauge
)

// Store metrics
var (
	StoreOperationDuration metric.Float64Histogram
	StoreOperationErrors   metric.Int64Counter
)

// Webhook metrics
var (
	WebhookReceivedTotal      metric.Int64Counter
	WebhookAuthFailures       metric.Int64Counter
	WebhookParseErrors        metric.Int64Counter
	WebhookEventsProduced     metric.Int64Counter
)

func init() {
	// Ingest
	IngestEventsTotal, _ = meter.Int64Counter("evidra.ingest.events_total",
		metric.WithDescription("Total ingested events by status"))
	IngestDuration, _ = meter.Float64Histogram("evidra.ingest.duration_seconds",
		metric.WithDescription("End-to-end ingest latency"),
		metric.WithUnit("s"))
	IngestBatchSize, _ = meter.Int64Histogram("evidra.ingest.batch_size",
		metric.WithDescription("Batch size distribution"))
	IngestPayloadBytes, _ = meter.Int64Histogram("evidra.ingest.payload_bytes",
		metric.WithDescription("Request payload size"),
		metric.WithUnit("By"))
	IngestIntegrityConflicts, _ = meter.Int64Counter("evidra.ingest.integrity_conflicts_total",
		metric.WithDescription("Same ID, different content"))

	// Argo
	ArgoPollsTotal, _ = meter.Int64Counter("evidra.argo.polls_total",
		metric.WithDescription("Collector poll cycles"))
	ArgoPollDuration, _ = meter.Float64Histogram("evidra.argo.poll_duration_seconds",
		metric.WithDescription("Time per collection cycle"),
		metric.WithUnit("s"))
	ArgoEventsCollected, _ = meter.Int64Counter("evidra.argo.events_collected_total",
		metric.WithDescription("Events collected from Argo CD"))
	ArgoNormalizeErrors, _ = meter.Int64Counter("evidra.argo.normalize_errors_total",
		metric.WithDescription("Normalization failures"))
	ArgoDuplicatesSkipped, _ = meter.Int64Counter("evidra.argo.duplicates_skipped_total",
		metric.WithDescription("Checkpoint-based skips"))
	ArgoCheckpointSaves, _ = meter.Int64Counter("evidra.argo.checkpoint_saves_total",
		metric.WithDescription("Checkpoint save operations"))
	ArgoLagSeconds, _ = meter.Float64Gauge("evidra.argo.lag_seconds",
		metric.WithDescription("Time since last successful ingest per app"),
		metric.WithUnit("s"))

	// Store
	StoreOperationDuration, _ = meter.Float64Histogram("evidra.store.operation.duration",
		metric.WithDescription("Duration of store operations"),
		metric.WithUnit("s"))
	StoreOperationErrors, _ = meter.Int64Counter("evidra.store.operation.errors_total",
		metric.WithDescription("Total store operation errors"))

	// Webhook
	WebhookReceivedTotal, _ = meter.Int64Counter("evidra.webhook.received_total",
		metric.WithDescription("Webhook traffic by provider"))
	WebhookAuthFailures, _ = meter.Int64Counter("evidra.webhook.auth_failures_total",
		metric.WithDescription("Webhook auth failures"))
	WebhookParseErrors, _ = meter.Int64Counter("evidra.webhook.parse_errors_total",
		metric.WithDescription("Webhook parse failures"))
	WebhookEventsProduced, _ = meter.Int64Counter("evidra.webhook.events_produced_total",
		metric.WithDescription("Events generated per webhook"))
}
```

**Note:** Using `init()` with the global meter is safe — if OTel is not initialized, the global meter returns no-op instruments. No `go.opentelemetry.io/otel/sdk` is imported here — only the API package.

**Verify:**
```bash
go build ./internal/observability/...
make boundary-check
```

---

### Step 2.2: Instrument the ingest handler

**File:** `internal/api/handlers_events.go`

1. **Add imports:**
```go
"go.opentelemetry.io/otel"
"go.opentelemetry.io/otel/attribute"
"go.opentelemetry.io/otel/codes"
"go.opentelemetry.io/otel/trace"
"evidra/internal/observability"
)
```

2. **Add tracer var at package level** (or at top of file):
```go
var tracer = otel.Tracer("evidra/api")
```

3. **Instrument `handleEvents`** (the `POST /v1/events` handler, line 20). Wrap the body in a span after method check and body read:

```go
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil, false)
		return
	}
	body, err := readBodyLimited(w, r, maxIngestBodyBytes)
	if err != nil {
		// ... existing error handling unchanged ...
		return
	}

	ctx, span := tracer.Start(r.Context(), "evidra.ingest",
		trace.WithAttributes(attribute.Int("payload_bytes", len(body))),
	)
	defer span.End()

	observability.IngestPayloadBytes.Record(ctx, int64(len(body)))

	if err := s.authorizeIngest(r, body); err != nil {
		span.SetStatus(codes.Error, "unauthorized")
		// ... existing error handling ...
		return
	}

	events, err := ce.ParseRequest(r, body)
	if err != nil {
		span.SetStatus(codes.Error, "parse_failed")
		// ... existing error handling ...
		return
	}

	span.SetAttributes(attribute.Int("batch_size", len(events)))
	observability.IngestBatchSize.Record(ctx, int64(len(events)))

	results := make([]map[string]interface{}, 0, len(events))
	var acceptedCount, duplicateCount int
	lastStatus := http.StatusAccepted

	for _, event := range events {
		status, ingestedAt, err := s.service.IngestEvent(ctx, event)
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				span.SetStatus(codes.Error, "integrity_conflict")
				observability.IngestIntegrityConflicts.Add(ctx, 1,
					metric.WithAttributes(attribute.String("source", event.Source)))
				writeError(w, http.StatusConflict, "EVENT_ID_CONFLICT", "event id already exists with different payload", nil, false)
				return
			}
			span.RecordError(err)
			handleStoreErr(w, err)
			return
		}
		switch status {
		case store.IngestAccepted:
			acceptedCount++
		case store.IngestDuplicate:
			duplicateCount++
		}
		observability.IngestEventsTotal.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("status", string(status)),
				attribute.String("source", event.Source),
				attribute.String("type", event.Type),
			))
		// ... rest of result building unchanged ...
	}

	span.SetAttributes(
		attribute.Int("accepted_count", acceptedCount),
		attribute.Int("duplicate_count", duplicateCount),
	)

	// ... existing response writing unchanged ...
}
```

Also add `"go.opentelemetry.io/otel/metric"` to the imports.

**Verify:**
```bash
go build ./internal/api/...
go test ./internal/api/...
```

---

### Step 2.3: Instrument the service layer

**File:** `internal/app/service.go`

1. **Add imports:**
```go
"go.opentelemetry.io/otel"
"go.opentelemetry.io/otel/attribute"
"go.opentelemetry.io/otel/trace"
```

2. **Add tracer:**
```go
var tracer = otel.Tracer("evidra/app")
```

3. **Add spans to key methods.** For `IngestEvent` (line 30):

```go
func (s *Service) IngestEvent(ctx context.Context, event ce.StoredEvent) (store.IngestStatus, time.Time, error) {
	ctx, span := tracer.Start(ctx, "Service.IngestEvent",
		trace.WithAttributes(
			attribute.String("event_id", event.ID),
			attribute.String("event_type", event.Type),
			attribute.String("subject", event.Subject),
		),
	)
	defer span.End()
	event.Time = event.Time.UTC()
	status, t, err := s.repo.IngestEvent(ctx, event)
	if err != nil {
		span.RecordError(err)
	}
	return status, t, err
}
```

Apply same pattern to: `QueryTimeline`, `GetEvent`, `ListSubjects`, `EventsByExtension`, `CreateExport`. Each gets a span with method name and relevant attributes.

For `CreateExport` (line 59), add child spans for each phase:

```go
func (s *Service) CreateExport(ctx context.Context, format string, filter map[string]interface{}) (model.ExportJob, error) {
	ctx, span := tracer.Start(ctx, "Service.CreateExport",
		trace.WithAttributes(attribute.String("format", format)),
	)
	defer span.End()
	// ... existing code, the ctx with span propagates to repo and exporter calls ...
}
```

**Verify:**
```bash
go test ./internal/app/...
```

---

### Step 2.4: Instrument the Argo collector

**File:** `internal/ingest/argo/collector.go`

This is the most complex instrumentation because the collector runs as a background goroutine with no incoming HTTP context. It must create root spans.

1. **Add imports:**
```go
"go.opentelemetry.io/otel"
"go.opentelemetry.io/otel/attribute"
"go.opentelemetry.io/otel/codes"
"go.opentelemetry.io/otel/metric"
"go.opentelemetry.io/otel/trace"
"evidra/internal/observability"
```

2. **Add tracer:**
```go
var tracer = otel.Tracer("evidra/argo-collector")
```

3. **Instrument `startPollingFallback`** (line 95). Inside the `wait.UntilWithContext` callback:

```go
wait.UntilWithContext(ctx, func(ctx context.Context) {
	ctx, span := tracer.Start(ctx, "evidra.argo.poll",
		trace.WithAttributes(attribute.String("mechanism", "rest")),
	)
	defer span.End()
	pollStart := time.Now()

	events, err := c.Fetch(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "fetch_failed")
		observability.ArgoPollsTotal.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("mechanism", "rest"),
				attribute.String("status", "error"),
			))
		c.Logger.Error(err, "argo collector poll error")
		return
	}

	var collected, skipped int
	for _, se := range events {
		if !c.shouldProcess(se) {
			skipped++
			continue
		}
		// ... existing normalize + ingest code ...
		// After successful ingest:
		collected++
		observability.ArgoEventsCollected.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("app", se.App),
				attribute.String("cluster", se.Cluster),
			))
	}

	c.saveCheckpoint()

	span.SetAttributes(
		attribute.Int("events_fetched", len(events)),
		attribute.Int("events_collected", collected),
		attribute.Int("events_skipped", skipped),
	)
	observability.ArgoPollsTotal.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("mechanism", "rest"),
			attribute.String("status", "success"),
		))
	observability.ArgoPollDuration.Record(ctx, time.Since(pollStart).Seconds(),
		metric.WithAttributes(attribute.String("mechanism", "rest")))
}, interval)
```

4. **Instrument `handleAppEvent`** (line 129). Similar pattern — create a span at the top:

```go
func (c *Collector) handleAppEvent(ctx context.Context, obj interface{}) {
	ctx, span := tracer.Start(ctx, "evidra.argo.handleAppEvent",
		trace.WithAttributes(attribute.String("mechanism", "informer")),
	)
	defer span.End()
	// ... existing code ...
	// After successful normalize:
	span.SetAttributes(attribute.String("app", name), attribute.String("event_type", se.EventType))
	// After successful ingest:
	observability.ArgoEventsCollected.Add(ctx, 1, ...)
}
```

5. **Instrument `saveCheckpoint`** (line 365):

```go
func (c *Collector) saveCheckpoint() {
	if c.Checkpoint == nil {
		return
	}
	if err := c.Checkpoint.Save(Checkpoint{Apps: c.appCursors}); err != nil {
		c.logError(err, "argo collector checkpoint save error")
		observability.ArgoCheckpointSaves.Add(context.Background(), 1,
			metric.WithAttributes(attribute.String("status", "error")))
		return
	}
	observability.ArgoCheckpointSaves.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("status", "success")))
}
```

6. **Record normalize errors** in `startPollingFallback` (line 116) and `handleAppEvent` (line 226):

```go
// Where normalize error is logged:
observability.ArgoNormalizeErrors.Add(ctx, 1,
	metric.WithAttributes(attribute.String("app", se.App)))
```

**Verify:**
```bash
go build ./internal/ingest/...
go test ./internal/ingest/...
```

---

### Step 2.5: Phase 2 integration test

```bash
go build -o evidra-gitops ./cmd/evidra-gitops
go test ./...
make boundary-check
```

**Manual smoke test:**
```bash
EVIDRA_DEV_INSECURE=true EVIDRA_OTEL_TRACES_EXPORTER=stdout ./evidra-gitops &

# Ingest an event — should see trace spans on stdout
curl -X POST http://localhost:8080/v1/events \
  -H "Content-Type: application/cloudevents+json" \
  -d '{
    "specversion": "1.0",
    "id": "test-001",
    "type": "test.event",
    "source": "manual",
    "time": "2026-02-25T00:00:00Z",
    "subject": "myapp",
    "data": {"status": "ok"}
  }'

# Verify metrics include custom ingest metrics
curl -s http://localhost:8080/metrics | grep evidra_ingest

kill %1
```

---

## Phase 3: Query & Export Instrumentation + Auth Metrics

**Goal:** Change projection tracing, export pipeline tracing, auth decision metrics. Complete coverage.

---

### Step 3.1: Instrument change projections

**File:** `internal/app/changes.go`

The tracer var is already defined in `service.go` (same package). Use it directly.

1. **`ListChanges`** (line 96):
```go
func (s *Service) ListChanges(ctx context.Context, q ChangeQuery) ([]Change, string, error) {
	ctx, span := tracer.Start(ctx, "Service.ListChanges",
		trace.WithAttributes(
			attribute.String("subject", q.Subject.App+":"+q.Subject.Environment+":"+q.Subject.Cluster),
		),
	)
	defer span.End()
	// ... existing code ...
	span.SetAttributes(
		attribute.Int("event_count", len(events)),
		attribute.Int("change_count", len(changes)),
	)
	return changes, cursor, nil
}
```

2. **Same for `GetChange`, `GetChangeTimeline`, `GetChangeEvidence`** — add a span with change ID as attribute.

3. **`eventsForChangeQuery`** (line 184) — add a span:
```go
func (s *Service) eventsForChangeQuery(ctx context.Context, q ChangeQuery) ([]ce.StoredEvent, error) {
	ctx, span := tracer.Start(ctx, "Service.eventsForChangeQuery")
	defer span.End()
	// ... existing code ...
}
```

**Verify:**
```bash
go test ./internal/app/...
```

---

### Step 3.2: Instrument the export pipeline

**File:** `internal/export/fs.go`

1. **Add imports:**
```go
"go.opentelemetry.io/otel"
"go.opentelemetry.io/otel/attribute"
```

2. **Add tracer:**
```go
var tracer = otel.Tracer("evidra/export")
```

3. **Instrument `CreateEvidencePack`** (line 26). The method currently ignores its context parameter (`_ context.Context`). Change to use it:

```go
func (f *FilesystemExporter) CreateEvidencePack(ctx context.Context, jobID string, events []ce.StoredEvent) (string, error) {
	ctx, span := tracer.Start(ctx, "evidra.export.create",
		trace.WithAttributes(
			attribute.String("job_id", jobID),
			attribute.Int("event_count", len(events)),
		),
	)
	defer span.End()

	// ... existing code ...

	// After writing the file (before return):
	span.SetAttributes(attribute.Int("artifact_bytes", len(b)))

	return path, nil
}
```

**Verify:**
```bash
go test ./internal/export/...
```

---

### Step 3.3: Add auth decision metrics

**File:** `internal/api/audit.go`

Add metrics recording inside `auditAuth` (line 27):

1. **Add imports:**
```go
"go.opentelemetry.io/otel/attribute"
"go.opentelemetry.io/otel/metric"
"evidra/internal/observability"
```

2. **Define auth metrics** — add to `internal/observability/metrics.go`:
```go
// Auth metrics
var (
	AuthDecisionsTotal  metric.Int64Counter
	AuthRateLimitHits   metric.Int64Counter
)

// In init():
AuthDecisionsTotal, _ = meter.Int64Counter("evidra.auth.decisions_total",
	metric.WithDescription("Auth decision rates"))
AuthRateLimitHits, _ = meter.Int64Counter("evidra.auth.rate_limit_hits_total",
	metric.WithDescription("Rate limiter activations"))
```

3. **In `auditAuth`**, after building the `auditEvent` struct (line 40), add:

```go
observability.AuthDecisionsTotal.Add(r.Context(), 1,
	metric.WithAttributes(
		attribute.String("decision", ev.Decision),
		attribute.String("mechanism", ev.Mechanism),
		attribute.String("action", actionFromPath(ev.Path)),
	))
```

Add helper:
```go
func actionFromPath(path string) string {
	if strings.HasPrefix(path, "/v1/events") && strings.Count(path, "/") == 2 {
		return "ingest" // POST /v1/events
	}
	if strings.HasPrefix(path, "/v1/exports") {
		return "export"
	}
	return "read"
}
```

**Verify:**
```bash
go test ./internal/api/...
```

---

### Step 3.4: Add change and export metrics

**File:** `internal/observability/metrics.go`

Add the remaining metrics:

```go
// Change metrics
var (
	ChangesQueryDuration      metric.Float64Histogram
	ChangesProjectionDuration metric.Float64Histogram
	ChangesEventCount         metric.Int64Histogram
	ChangesCount              metric.Int64Histogram
)

// Export metrics
var (
	ExportJobsTotal    metric.Int64Counter
	ExportDuration     metric.Float64Histogram
	ExportArtifactBytes metric.Int64Histogram
	ExportEventsPerPack metric.Int64Histogram
)

// Runtime metrics
var (
	EvidraInfo metric.Int64Gauge
)

// In init():
ChangesQueryDuration, _ = meter.Float64Histogram("evidra.changes.query_duration_seconds",
	metric.WithUnit("s"))
ChangesProjectionDuration, _ = meter.Float64Histogram("evidra.changes.projection_duration_seconds",
	metric.WithUnit("s"))
ChangesEventCount, _ = meter.Int64Histogram("evidra.changes.event_count")
ChangesCount, _ = meter.Int64Histogram("evidra.changes.count")

ExportJobsTotal, _ = meter.Int64Counter("evidra.export.jobs_total")
ExportDuration, _ = meter.Float64Histogram("evidra.export.duration_seconds",
	metric.WithUnit("s"))
ExportArtifactBytes, _ = meter.Int64Histogram("evidra.export.artifact_bytes",
	metric.WithUnit("By"))
ExportEventsPerPack, _ = meter.Int64Histogram("evidra.export.events_per_pack")

EvidraInfo, _ = meter.Int64Gauge("evidra.info",
	metric.WithDescription("Build info, always 1"))
```

Then use these metrics in the corresponding instrumented code from steps 3.1 and 3.2. For example, in `Service.ListChanges`:

```go
start := time.Now()
events, err := s.eventsForChangeQuery(ctx, q)
queryDur := time.Since(start)

projStart := time.Now()
// ... projection logic ...
projDur := time.Since(projStart)

observability.ChangesQueryDuration.Record(ctx, queryDur.Seconds(),
	metric.WithAttributes(attribute.String("endpoint", "list")))
observability.ChangesProjectionDuration.Record(ctx, projDur.Seconds())
observability.ChangesEventCount.Record(ctx, int64(len(events)))
observability.ChangesCount.Record(ctx, int64(len(changes)))
```

**Verify:**
```bash
go build ./internal/observability/...
go test ./...
```

---

### Step 3.5: Set `evidra.info` gauge at startup

**File:** `cmd/evidra/main.go`

After telemetry init, record the info gauge:

```go
observability.EvidraInfo.Record(ctx, 1,
	metric.WithAttributes(
		attribute.String("version", version), // use ldflags or runtime/debug
		attribute.String("go_version", runtime.Version()),
		attribute.String("db_dialect", cfg.DBDialect),
	))
```

Add `"runtime"` to imports.

**Verify:**
```bash
go build -o evidra-gitops ./cmd/evidra-gitops
```

---

### Step 3.6: Phase 3 integration test

```bash
go build -o evidra-gitops ./cmd/evidra-gitops
go test ./...
make boundary-check
```

**Full smoke test with all metrics:**
```bash
EVIDRA_DEV_INSECURE=true EVIDRA_OTEL_TRACES_EXPORTER=stdout ./evidra-gitops &

# Ingest
curl -X POST http://localhost:8080/v1/events \
  -H "Content-Type: application/cloudevents+json" \
  -d '{
    "specversion":"1.0","id":"test-002","type":"argo.sync.finished",
    "source":"argocd","time":"2026-02-25T00:00:00Z",
    "subject":"myapp",
    "extensions":{"cluster":"prod","namespace":"default"},
    "data":{"status":"Succeeded"}
  }'

# Query changes
curl "http://localhost:8080/v1/changes?from=2026-02-24T00:00:00Z&to=2026-02-26T00:00:00Z&subject=myapp:default:prod"

# Check all metric families exist
curl -s http://localhost:8080/metrics | grep -c evidra_
# Should be > 20 metric families

# Verify specific metrics
curl -s http://localhost:8080/metrics | grep evidra_ingest_events_total
curl -s http://localhost:8080/metrics | grep evidra_auth_decisions_total
curl -s http://localhost:8080/metrics | grep http_server_request_duration_seconds

kill %1
```

---

## Phase 4: Cleanup Unused Code + Update All Documentation

**Goal:** Remove dead code and stale references. Update every documentation file, Kubernetes manifest, Docker config, and CLAUDE.md to reflect the new OTel-based observability stack. After this phase, no documentation mentions old Prometheus metric names, `promauto`, or `promhttp`, and all operational guides reference the new OTel configuration.

---

### Step 4.1: Verify and remove dead Prometheus code

After Phases 1–3, the old Prometheus code should already be deleted. This step is a final sweep.

**Run these checks:**

```bash
# 1. No direct Prometheus imports anywhere in Evidra-GitOps source
grep -r "promauto\|promhttp\|\"github.com/prometheus/client_golang" internal/ cmd/
# Expected: no output

# 2. httpmetrics.go is gone
ls internal/observability/httpmetrics.go 2>&1
# Expected: No such file or directory

# 3. No references to old metric names in Go code
grep -r "evidra_http_requests_total\|evidra_http_request_duration_seconds" internal/ cmd/
# Expected: no output

# 4. No stdlib log calls in internal/ (all replaced with logr)
grep -rn "\"log\"" internal/
# Expected: no output (only cmd/evidra/main.go may use log.Fatal before logger init)

# 5. No unused imports
go vet ./...
# Expected: clean
```

**If any of the above fail**, fix the offending files before proceeding.

**Run `go mod tidy`** to remove `prometheus/client_golang` from `go.mod` if it is no longer a direct dependency:

```bash
go mod tidy
```

Check the result:
```bash
grep "prometheus/client_golang" go.mod
# Expected: it appears only under `require ( ... // indirect )` or not at all
# (it will remain indirect if the OTel Prometheus exporter depends on it)
```

**Verify:**
```bash
go build -o evidra-gitops ./cmd/evidra-gitops
go test ./...
make boundary-check
```

---

### Step 4.2: Update CLAUDE.md

**File:** `CLAUDE.md`

This is the primary codebase instruction file read by Claude Code. It contains several stale references.

**Change 1 — Bootstrap description (line 65).** Update:

Before:
```
Bootstrap wiring lives in `internal/bootstrap/runtime.go` — it assembles the repository, Argo collector, auth stack, metrics middleware, and webhook registry into a running server.
```

After:
```
Bootstrap wiring lives in `internal/bootstrap/runtime.go` — it assembles the OTel SDK, repository (wrapped with `otelsql`), Argo collector, auth stack, `otelhttp` middleware, and webhook registry into a running server.
```

**Change 2 — API routes list (line 107).** Update the `/metrics` entry:

Before:
```
- `GET /metrics` — Prometheus metrics (`evidra_http_requests_total`, `evidra_http_request_duration_seconds`)
```

After:
```
- `GET /metrics` — Prometheus-format metrics via OTel exporter (HTTP: `http_server_request_duration_seconds`, `http_server_active_requests`; DB: `db_client_connections_*`; custom: `evidra_ingest_*`, `evidra_argo_*`, `evidra_changes_*`, `evidra_export_*`, `evidra_auth_*`)
```

**Change 3 — Configuration section (after line 177).** Add OTel env vars to the key vars block:

```
# --- OpenTelemetry ---
EVIDRA_OTEL_SERVICE_NAME=evidra
EVIDRA_OTEL_TRACES_EXPORTER=none         # otlp | stdout | none
EVIDRA_OTEL_METRICS_EXPORTER=prometheus   # prometheus | otlp | stdout | none
EVIDRA_OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317
EVIDRA_OTEL_EXPORTER_OTLP_PROTOCOL=grpc  # grpc | http/protobuf
EVIDRA_OTEL_TRACES_SAMPLER=parentbased_traceidratio
EVIDRA_OTEL_TRACES_SAMPLER_ARG=1.0
EVIDRA_LOG_LEVEL=info                     # debug | info | warn | error
```

**Change 4 — Add observability section.** After the Configuration section, add a new `### Observability (`internal/observability/`)` section:

```markdown
### Observability (`internal/observability/`)

OTel SDK initialization in `otel.go`, custom metric definitions in `metrics.go`. The old `httpmetrics.go` (Prometheus `client_golang`) has been removed.

HTTP instrumentation uses `otelhttp.NewHandler()` from `go.opentelemetry.io/contrib`. DB instrumentation uses `otelsql.Open()`. Both produce automatic spans and metrics following OTel semantic conventions.

Custom Evidra-GitOps metrics (defined in `metrics.go`) use the OTel Metrics API — only API packages are imported, never SDK packages. SDK initialization is confined to `internal/observability/` and `internal/bootstrap/`.

Boundary test `internal/architecture/boundaries_test.go` enforces that `internal/store`, `internal/app`, `internal/api`, `internal/ingest`, and `internal/export` never import OTel SDK packages.
```

**Change 5 — Key conventions section.** Add:

```
- **Observability**: OTel API only in domain packages (`internal/app`, `internal/store`, etc.). OTel SDK only in `internal/observability` and `internal/bootstrap`. Enforced by boundary tests.
- **Logging**: all log output via `logr/zap` — no `log.Printf`. Logs carry `trace_id`/`span_id` when in a traced context.
```

**Verify:** Read through the full CLAUDE.md after edits to confirm consistency.

---

### Step 4.3: Update ops-minimum.md

**File:** `docs/setup/ops-minimum.md`

**Change 1 — Section "4) Observability minimum" (line 91).** Rewrite:

Before:
```markdown
## 4) Observability minimum

Available endpoints:
- `GET /healthz`
- `GET /metrics` (Prometheus format)

Key log lines:
- Collector start:
  - `argo collector started`
- Connectivity/auth failures:
  - `argo collector fetch error`
  - `argo collector fetch backoff exhausted`

Quick checks:

```bash
# health
curl -fsS http://localhost:8080/healthz

# metrics
curl -fsS http://localhost:8080/metrics | head

# collector logs in Kubernetes
kubectl -n evidra-gitops logs deploy/evidra-gitops-prod | rg -n "argo collector|fetch error|backoff exhausted"
```

How to confirm recent ingest:
- Check latest change timestamps for expected subjects in Explorer/API.
- Confirm collector errors are not repeating continuously.

Current behavior note:
- v0.1 does not emit a dedicated "poll success" log per cycle.
- Use data freshness + absence of recurring collector errors as operational signal.
```

After:
```markdown
## 4) Observability minimum

### Endpoints

- `GET /healthz` — liveness check
- `GET /metrics` — Prometheus-format metrics via OpenTelemetry exporter

### Key metrics to monitor

| Metric | What it tells you |
|--------|-------------------|
| `http_server_request_duration_seconds` | API latency by method/route/status |
| `http_server_active_requests` | Current in-flight requests |
| `db_client_connections_open` | DB connection pool size |
| `evidra_ingest_events_total` | Event throughput by status/source |
| `evidra_argo_polls_total` | Collector poll success/failure rate |
| `evidra_argo_lag_seconds` | Data freshness per Argo application |
| `evidra_auth_decisions_total` | Auth allow/deny rates |

### Distributed tracing

When `EVIDRA_OTEL_TRACES_EXPORTER=otlp` is set, Evidra-GitOps exports OpenTelemetry traces to the configured endpoint. Traces cover the full request lifecycle: HTTP → auth → service → DB, plus Argo collector poll cycles.

### Key log lines

All logs are structured JSON via `logr/zap` and include `trace_id`/`span_id` when in a traced context.

- Collector start: `argo collector started`
- Connectivity/auth failures: `argo collector poll error`, `argo collector normalize error`
- Auth decisions: `audit_auth` (JSON with decision, mechanism, actor, path)

### Quick checks

```bash
# health
curl -fsS http://localhost:8080/healthz

# metrics — verify OTel metrics are present
curl -fsS http://localhost:8080/metrics | grep http_server_request_duration
curl -fsS http://localhost:8080/metrics | grep evidra_ingest_events_total
curl -fsS http://localhost:8080/metrics | grep evidra_argo_polls_total

# collector logs in Kubernetes
kubectl -n evidra-gitops logs deploy/evidra-gitops-prod | rg -n "argo collector|poll error|normalize error"
```

### How to confirm recent ingest

- Check `evidra_argo_lag_seconds` — should be < 300s for active applications.
- Check latest change timestamps for expected subjects in Explorer/API.
- Confirm collector errors are not repeating continuously.
```

---

### Step 4.4: Update API contracts

**File:** `docs/api/contracts-v1.md`

**Change 1 — "Runtime API (v1)" section (line 30).** Add metrics endpoint:

Before:
```markdown
## Runtime API (v1)
Operational:
- `GET /healthz`
```

After:
```markdown
## Runtime API (v1)
Operational:
- `GET /healthz`
- `GET /metrics` — Prometheus-format metrics (OTel semantic conventions)
```

---

### Step 4.5: Update Kubernetes configmap with OTel defaults

**File:** `deploy/k8s/base/configmap.yaml`

Add OTel default configuration entries:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: evidra-gitops-config
data:
  EVIDRA_ADDR: ":8080"
  EVIDRA_EXPORT_DIR: "/var/evidra-gitops/exports"
  EVIDRA_DB_DRIVER: "pgx"
  EVIDRA_DB_DIALECT: "postgres"
  EVIDRA_DB_MIGRATE: "true"
  EVIDRA_ARGO_COLLECTOR_ENABLED: "false"
  EVIDRA_ARGO_COLLECTOR_INTERVAL: "30s"
  EVIDRA_ARGO_API_URL: ""
  EVIDRA_ARGO_DEFAULT_ENV: "unknown"
  EVIDRA_ARGO_CHECKPOINT_FILE: "/var/evidra-gitops/argo_checkpoint.json"
  # --- OpenTelemetry ---
  EVIDRA_OTEL_SERVICE_NAME: "evidra"
  EVIDRA_OTEL_TRACES_EXPORTER: "none"
  EVIDRA_OTEL_METRICS_EXPORTER: "prometheus"
  EVIDRA_LOG_LEVEL: "info"
```

---

### Step 4.6: Update Kubernetes network policy for OTLP egress

**File:** `deploy/k8s/base/networkpolicy.yaml`

When using `EVIDRA_OTEL_TRACES_EXPORTER=otlp`, Evidra-GitOps needs to reach the OTel Collector on port 4317 (gRPC) or 4318 (HTTP). Add an egress rule:

```yaml
    # OTel Collector (OTLP gRPC). Only needed when EVIDRA_OTEL_TRACES_EXPORTER=otlp.
    # Tighten with namespace/pod selectors in stricter overlays.
    - ports:
        - protocol: TCP
          port: 4317
```

Add this after the existing Postgres egress rule (after line 35).

---

### Step 4.7: Update docker-compose.yml with OTel env vars

**File:** `docker-compose.yml`

Add OTel defaults to the `evidra-gitops` service environment section (after line 35):

```yaml
      EVIDRA_OTEL_SERVICE_NAME: "evidra"
      EVIDRA_OTEL_TRACES_EXPORTER: "${EVIDRA_OTEL_TRACES_EXPORTER:-none}"
      EVIDRA_OTEL_METRICS_EXPORTER: "prometheus"
      EVIDRA_LOG_LEVEL: "${EVIDRA_LOG_LEVEL:-info}"
```

---

### Step 4.8: Update Makefile with metrics smoke target

**File:** `Makefile`

Add a new target for validating metrics after deployment:

```makefile
metrics-check:
	@echo "Checking OTel metrics endpoint..."
	@curl -fsS http://localhost:8080/metrics | grep -q "http_server_request_duration_seconds" \
		&& echo "OK: http_server_request_duration_seconds found" \
		|| (echo "FAIL: http_server_request_duration_seconds not found" && exit 1)
	@curl -fsS http://localhost:8080/metrics | grep -q "evidra_ingest_events_total" \
		&& echo "OK: evidra_ingest_events_total found" \
		|| echo "WARN: evidra_ingest_events_total not found (may appear after first ingest)"
	@curl -fsS http://localhost:8080/metrics | grep -q "evidra_info" \
		&& echo "OK: evidra_info found" \
		|| (echo "FAIL: evidra_info not found" && exit 1)
	@echo "Checking no old Prometheus metrics remain..."
	@curl -fsS http://localhost:8080/metrics | grep -q "evidra_http_requests_total" \
		&& (echo "FAIL: old metric evidra_http_requests_total still present" && exit 1) \
		|| echo "OK: old metrics removed"
```

Add `metrics-check` to the `.PHONY` list at the top.

---

### Step 4.9: Clean up go.sum

```bash
go mod tidy
```

This removes any entries from `go.sum` for packages no longer referenced (directly or transitively).

**Verify:**
```bash
go build -o evidra-gitops ./cmd/evidra-gitops
go test ./...
```

---

### Step 4.10: Phase 4 final verification

Run the complete checklist:

```bash
# Build
go build -o evidra-gitops ./cmd/evidra-gitops

# Tests
go test ./...

# Boundary enforcement
make boundary-check

# No old Prometheus code
grep -rn "promauto\|promhttp" internal/ cmd/
# Expected: nothing

# No old metric names in docs
grep -rn "evidra_http_requests_total\|evidra_http_request_duration_seconds" docs/ CLAUDE.md
# Expected: nothing

# No stdlib log in internal/
grep -rn "\"log\"" internal/
# Expected: nothing

# OTel env vars documented
grep -c "EVIDRA_OTEL" CLAUDE.md
# Expected: >= 5

grep -c "EVIDRA_OTEL" deploy/k8s/base/configmap.yaml
# Expected: >= 3

grep -c "EVIDRA_OTEL" docker-compose.yml
# Expected: >= 2

# Metrics smoke (requires running server)
EVIDRA_DEV_INSECURE=true ./evidra-gitops &
sleep 2
make metrics-check
kill %1
```

---

## Post-Implementation Checklist

After all four phases are complete, verify:

### Code

- [ ] `go build -o evidra-gitops ./cmd/evidra-gitops` — succeeds
- [ ] `go test ./...` — all tests pass
- [ ] `make boundary-check` — no violations
- [ ] `go vet ./...` — no issues
- [ ] `grep -r "promauto\|promhttp\|prometheus/client_golang" internal/` — no direct Prometheus imports in Evidra-GitOps code
- [ ] `grep -r "\"log\"" internal/` — no stdlib `log` package imported in `internal/` (only in cmd/ before logger init)
- [ ] `internal/observability/httpmetrics.go` does not exist
- [ ] No file in `internal/store`, `internal/app`, `internal/api`, `internal/ingest`, or `internal/export` imports `go.opentelemetry.io/otel/sdk` (only API packages)

### Runtime

- [ ] `/metrics` endpoint returns OTel-format metrics including `http_server_*`, `db_client_*`, `evidra_ingest_*`, `evidra_argo_*`, `evidra_auth_*`
- [ ] `/metrics` does NOT return `evidra_http_requests_total` or `evidra_http_request_duration_seconds` (old names gone)
- [ ] `EVIDRA_OTEL_TRACES_EXPORTER=stdout` produces JSON trace output for every HTTP request
- [ ] `EVIDRA_OTEL_TRACES_EXPORTER=none` (default) produces no trace output and adds negligible overhead
- [ ] Setting `EVIDRA_OTEL_TRACES_EXPORTER=otlp EVIDRA_OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317` sends traces to an OTel Collector
- [ ] `make metrics-check` passes against a running instance

### Documentation

- [ ] `CLAUDE.md` references OTel (not Prometheus `client_golang`) for metrics
- [ ] `CLAUDE.md` lists `EVIDRA_OTEL_*` environment variables
- [ ] `CLAUDE.md` no longer mentions `evidra_http_requests_total` or `evidra_http_request_duration_seconds`
- [ ] `docs/setup/ops-minimum.md` references OTel metrics and tracing
- [ ] `docs/api/contracts-v1.md` lists `GET /metrics` as an operational endpoint
- [ ] `deploy/k8s/base/configmap.yaml` includes `EVIDRA_OTEL_*` defaults
- [ ] `deploy/k8s/base/networkpolicy.yaml` includes OTLP egress rule (port 4317)
- [ ] `docker-compose.yml` includes `EVIDRA_OTEL_*` environment variables
- [ ] `grep -rn "evidra_http_requests_total" docs/ CLAUDE.md` — returns nothing

---

## File Change Summary

| File | Action | Phase |
|------|--------|-------|
| `internal/config/config.go` | Edit: add `OTelConfig` struct + env vars | 1 |
| `internal/observability/otel.go` | **Create**: SDK init + TelemetryProviders | 1 |
| `internal/observability/httpmetrics.go` | **Delete** | 1 |
| `internal/observability/metrics.go` | **Create**: all custom metric definitions | 2+3 |
| `internal/bootstrap/runtime.go` | Edit: new signature, otelhttp, otelsql, logr | 1 |
| `cmd/evidra/main.go` | Edit: init telemetry, pass to bootstrap, shutdown | 1 |
| `internal/api/server.go` | Edit: add `logger` field, `Logger` in options | 1 |
| `internal/api/audit.go` | Edit: replace `log.Printf`, add auth metrics | 1+3 |
| `internal/api/handlers_events.go` | Edit: ingest spans + metrics | 2 |
| `internal/api/handlers_query.go` | Edit: add spans (optional, traced by otelhttp) | 3 |
| `internal/api/handlers_changes.go` | Edit: add spans (optional, traced by otelhttp) | 3 |
| `internal/api/handlers_exports.go` | Edit: add spans (optional, traced by otelhttp) | 3 |
| `internal/app/service.go` | Edit: add tracer, spans on all methods | 2 |
| `internal/app/changes.go` | Edit: add spans + change metrics | 3 |
| `internal/ingest/argo/collector.go` | Edit: spans + collector metrics | 2 |
| `internal/export/fs.go` | Edit: use context, add span | 3 |
| `internal/architecture/boundaries_test.go` | Edit: add OTel SDK boundary test | 1 |
| `go.mod` | Edit: add OTel direct deps | 1 |
