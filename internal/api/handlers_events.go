package api

import (
	"errors"
	"net/http"
	"time"

	ce "evidra/internal/cloudevents"
	"evidra/internal/observability"
	"evidra/internal/store"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var apiTracer = otel.Tracer("evidra/api")

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"service": "evidra-gitops",
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil, false)
		return
	}
	body, err := readBodyLimited(w, r, maxIngestBodyBytes)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body is too large", nil, false)
			return
		}
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "unable to read body", nil, false)
		return
	}

	ctx, span := apiTracer.Start(r.Context(), "evidra.ingest",
		trace.WithAttributes(attribute.Int("payload_bytes", len(body))),
	)
	defer span.End()

	observability.IngestPayloadBytes.Record(ctx, int64(len(body)))

	if err := s.authorizeIngest(r, body); err != nil {
		span.SetStatus(codes.Error, "unauthorized")
		if errors.Is(err, errRateLimited) {
			writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", err.Error(), nil, true)
			return
		}
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", err.Error(), nil, false)
		return
	}

	events, err := ce.ParseRequest(r, body)
	if err != nil {
		span.SetStatus(codes.Error, "parse_failed")
		writeError(w, http.StatusBadRequest, "INVALID_CLOUDEVENT", err.Error(), nil, false)
		return
	}

	span.SetAttributes(attribute.Int("batch_size", len(events)))
	observability.IngestBatchSize.Record(ctx, int64(len(events)))

	results := make([]map[string]interface{}, 0, len(events))
	lastStatus := http.StatusAccepted
	var acceptedCount, duplicateCount int

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
		httpStatus := http.StatusAccepted
		if status == store.IngestDuplicate {
			httpStatus = http.StatusOK
			lastStatus = http.StatusOK
			duplicateCount++
		} else {
			acceptedCount++
		}
		_ = httpStatus

		observability.IngestEventsTotal.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("status", string(status)),
				attribute.String("source", event.Source),
				attribute.String("type", event.Type),
			))

		results = append(results, map[string]interface{}{
			"id":          event.ID,
			"status":      status,
			"ingested_at": ingestedAt.Format(time.RFC3339),
		})
	}

	span.SetAttributes(
		attribute.Int("accepted_count", acceptedCount),
		attribute.Int("duplicate_count", duplicateCount),
	)

	if len(results) == 1 {
		writeJSON(w, lastStatus, results[0])
	} else {
		writeJSON(w, http.StatusAccepted, results)
	}
}
