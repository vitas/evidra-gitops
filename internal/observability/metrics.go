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
	ArgoPollsTotal        metric.Int64Counter
	ArgoPollDuration      metric.Float64Histogram
	ArgoEventsCollected   metric.Int64Counter
	ArgoNormalizeErrors   metric.Int64Counter
	ArgoDuplicatesSkipped metric.Int64Counter
	ArgoCheckpointSaves   metric.Int64Counter
	ArgoLagSeconds        metric.Float64Gauge
)

// Store metrics
var (
	StoreOperationDuration metric.Float64Histogram
	StoreOperationErrors   metric.Int64Counter
)

// Webhook metrics
var (
	WebhookReceivedTotal  metric.Int64Counter
	WebhookAuthFailures   metric.Int64Counter
	WebhookParseErrors    metric.Int64Counter
	WebhookEventsProduced metric.Int64Counter
)

// Change metrics
var (
	ChangesQueryDuration      metric.Float64Histogram
	ChangesProjectionDuration metric.Float64Histogram
	ChangesEventCount         metric.Int64Histogram
	ChangesCount              metric.Int64Histogram
	ChangeCacheHits           metric.Int64Counter
	ChangeCacheMisses         metric.Int64Counter
)

// Export metrics
var (
	ExportJobsTotal     metric.Int64Counter
	ExportDuration      metric.Float64Histogram
	ExportArtifactBytes metric.Int64Histogram
	ExportEventsPerPack metric.Int64Histogram
)

// Auth metrics
var (
	AuthDecisionsTotal metric.Int64Counter
	AuthRateLimitHits  metric.Int64Counter
)

// Runtime metrics
var (
	EvidraInfo metric.Int64Gauge
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

	// Changes
	ChangesQueryDuration, _ = meter.Float64Histogram("evidra.changes.query_duration_seconds",
		metric.WithUnit("s"))
	ChangesProjectionDuration, _ = meter.Float64Histogram("evidra.changes.projection_duration_seconds",
		metric.WithUnit("s"))
	ChangesEventCount, _ = meter.Int64Histogram("evidra.changes.event_count")
	ChangesCount, _ = meter.Int64Histogram("evidra.changes.count")
	ChangeCacheHits, _ = meter.Int64Counter("evidra.changes.cache_hits_total",
		metric.WithDescription("Change query cache hits"))
	ChangeCacheMisses, _ = meter.Int64Counter("evidra.changes.cache_misses_total",
		metric.WithDescription("Change query cache misses"))

	// Export
	ExportJobsTotal, _ = meter.Int64Counter("evidra.export.jobs_total")
	ExportDuration, _ = meter.Float64Histogram("evidra.export.duration_seconds",
		metric.WithUnit("s"))
	ExportArtifactBytes, _ = meter.Int64Histogram("evidra.export.artifact_bytes",
		metric.WithUnit("By"))
	ExportEventsPerPack, _ = meter.Int64Histogram("evidra.export.events_per_pack")

	// Auth
	AuthDecisionsTotal, _ = meter.Int64Counter("evidra.auth.decisions_total",
		metric.WithDescription("Auth decision rates"))
	AuthRateLimitHits, _ = meter.Int64Counter("evidra.auth.rate_limit_hits_total",
		metric.WithDescription("Rate limiter activations"))

	// Runtime
	EvidraInfo, _ = meter.Int64Gauge("evidra.info",
		metric.WithDescription("Build info, always 1"))
}
