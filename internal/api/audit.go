package api

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"evidra/internal/observability"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type auditEvent struct {
	Time          string   `json:"time"`
	Decision      string   `json:"decision"`
	Mechanism     string   `json:"mechanism"`
	Actor         string   `json:"actor,omitempty"`
	Roles         []string `json:"roles,omitempty"`
	Method        string   `json:"method"`
	Path          string   `json:"path"`
	RemoteIP      string   `json:"remote_ip,omitempty"`
	RequestID     string   `json:"request_id,omitempty"`
	CorrelationID string   `json:"correlation_id,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

func (s *Server) auditAuth(r *http.Request, decision, mechanism, actor string, roles []string, reason string) {
	ev := auditEvent{
		Time:          time.Now().UTC().Format(time.RFC3339),
		Decision:      strings.TrimSpace(decision),
		Mechanism:     strings.TrimSpace(mechanism),
		Actor:         strings.TrimSpace(actor),
		Roles:         roles,
		Method:        r.Method,
		Path:          r.URL.Path,
		RemoteIP:      requestRemoteIP(r),
		RequestID:     strings.TrimSpace(r.Header.Get("X-Request-Id")),
		CorrelationID: strings.TrimSpace(r.Header.Get("X-Correlation-Id")),
		Reason:        strings.TrimSpace(reason),
	}
	observability.AuthDecisionsTotal.Add(r.Context(), 1,
		metric.WithAttributes(
			attribute.String("decision", ev.Decision),
			attribute.String("mechanism", ev.Mechanism),
		))
	if ev.Mechanism == "rate_limit" {
		observability.AuthRateLimitHits.Add(r.Context(), 1)
	}
	b, err := json.Marshal(ev)
	if err != nil {
		s.logger.Info("audit_auth", "decision", ev.Decision, "mechanism", ev.Mechanism, "path", ev.Path, "reason", ev.Reason)
		return
	}
	line := "audit_auth " + string(b)
	s.logger.Info("audit_auth", "event", string(b))
	s.writeAuditLine(line)
}

func requestRemoteIP(r *http.Request) string {
	xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (s *Server) writeAuditLine(line string) {
	path := strings.TrimSpace(s.auth.Audit.LogFile)
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		s.logger.Error(err, "audit file write failed", "path", path)
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line + "\n")
}
