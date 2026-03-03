package app

import (
	"context"
	"errors"
	"time"

	ce "evidra/internal/cloudevents"
	"evidra/internal/model"
	"evidra/internal/store"

	expirable "github.com/hashicorp/golang-lru/v2/expirable"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("evidra/app")

type Exporter interface {
	CreateEvidencePack(ctx context.Context, jobID string, events []ce.StoredEvent) (string, error)
	ReadArtifact(path string) ([]byte, error)
}

type Service struct {
	repo        store.Repository
	exporter    Exporter
	changeCache *expirable.LRU[string, []ce.StoredEvent]
}

func NewService(repo store.Repository, exporter Exporter) *Service {
	return &Service{
		repo:        repo,
		exporter:    exporter,
		changeCache: expirable.NewLRU[string, []ce.StoredEvent](256, nil, 10*time.Second),
	}
}

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
	} else {
		s.changeCache.Purge()
	}
	return status, t, err
}

func (s *Service) QueryTimeline(ctx context.Context, q store.TimelineQuery) (store.TimelineResult, error) {
	ctx, span := tracer.Start(ctx, "Service.QueryTimeline",
		trace.WithAttributes(
			attribute.String("subject", q.Subject),
			attribute.String("namespace", q.Namespace),
			attribute.String("cluster", q.Cluster),
		),
	)
	defer span.End()
	result, err := s.repo.QueryTimeline(ctx, q)
	if err != nil {
		span.RecordError(err)
	}
	return result, err
}

func (s *Service) GetEvent(ctx context.Context, id string) (ce.StoredEvent, error) {
	ctx, span := tracer.Start(ctx, "Service.GetEvent",
		trace.WithAttributes(attribute.String("event_id", id)),
	)
	defer span.End()
	event, err := s.repo.GetEvent(ctx, id)
	if err != nil {
		span.RecordError(err)
	}
	return event, err
}

func (s *Service) ListSubjects(ctx context.Context) ([]store.SubjectInfo, error) {
	ctx, span := tracer.Start(ctx, "Service.ListSubjects")
	defer span.End()
	subjects, err := s.repo.ListSubjects(ctx)
	if err != nil {
		span.RecordError(err)
	}
	return subjects, err
}

func (s *Service) EventsByExtension(ctx context.Context, key, value string, limit int) ([]ce.StoredEvent, error) {
	ctx, span := tracer.Start(ctx, "Service.EventsByExtension",
		trace.WithAttributes(
			attribute.String("key", key),
			attribute.String("value", value),
		),
	)
	defer span.End()
	events, err := s.repo.EventsByExtension(ctx, key, value, limit)
	if err != nil {
		span.RecordError(err)
	}
	return events, err
}

func (s *Service) GetExport(ctx context.Context, id string) (model.ExportJob, error) {
	ctx, span := tracer.Start(ctx, "Service.GetExport",
		trace.WithAttributes(attribute.String("export_id", id)),
	)
	defer span.End()
	job, err := s.repo.GetExport(ctx, id)
	if err != nil {
		span.RecordError(err)
	}
	return job, err
}

func (s *Service) ReadArtifact(path string) ([]byte, error) {
	return s.exporter.ReadArtifact(path)
}

func (s *Service) CreateExport(ctx context.Context, format string, filter map[string]interface{}) (model.ExportJob, error) {
	ctx, span := tracer.Start(ctx, "Service.CreateExport",
		trace.WithAttributes(attribute.String("format", format)),
	)
	defer span.End()

	job, err := s.repo.CreateExport(ctx, format, filter)
	if err != nil {
		span.RecordError(err)
		return model.ExportJob{}, err
	}
	items, err := s.exportItemsForJob(ctx, job.Filter)
	if err != nil {
		span.RecordError(err)
		_ = s.repo.SetExportFailed(ctx, job.ID, err.Error())
		return model.ExportJob{}, err
	}
	path, err := s.exporter.CreateEvidencePack(ctx, job.ID, items)
	if err != nil {
		span.RecordError(err)
		_ = s.repo.SetExportFailed(ctx, job.ID, err.Error())
		return model.ExportJob{}, err
	}
	if err := s.repo.SetExportCompleted(ctx, job.ID, path); err != nil {
		span.RecordError(err)
		return model.ExportJob{}, err
	}
	return s.repo.GetExport(ctx, job.ID)
}

func (s *Service) exportItemsForJob(ctx context.Context, filter map[string]interface{}) ([]ce.StoredEvent, error) {
	if filter == nil {
		return []ce.StoredEvent{}, nil
	}
	fromRaw, _ := filter["from"].(string)
	toRaw, _ := filter["to"].(string)
	subjectRaw, _ := filter["subject"].(string)
	if fromRaw == "" || toRaw == "" || subjectRaw == "" {
		return []ce.StoredEvent{}, nil
	}
	from, err := time.Parse(time.RFC3339, fromRaw)
	if err != nil {
		return nil, err
	}
	to, err := time.Parse(time.RFC3339, toRaw)
	if err != nil {
		return nil, err
	}
	subject, err := ParseSubject(subjectRaw)
	if err != nil {
		return nil, err
	}
	res, err := s.repo.QueryTimeline(ctx, store.TimelineQuery{
		Subject:           subject.App,
		Namespace:         subject.Environment,
		Cluster:           subject.Cluster,
		From:              from.UTC(),
		To:                to.UTC(),
		IncludeSupporting: true,
		Limit:             500,
	})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

type ParsedSubject struct {
	App         string
	Environment string
	Cluster     string
}

func ParseSubject(subject string) (ParsedSubject, error) {
	parts := splitN(subject, ':', 3)
	if len(parts) < 3 {
		return ParsedSubject{}, errors.New("invalid subject")
	}
	return ParsedSubject{
		App:         parts[0],
		Environment: parts[1],
		Cluster:     parts[2],
	}, nil
}

func splitN(input string, sep rune, n int) []string {
	out := make([]string, 0, n)
	last := 0
	for i, ch := range input {
		if ch == sep && len(out) < n-1 {
			out = append(out, input[last:i])
			last = i + 1
		}
	}
	out = append(out, input[last:])
	return out
}
