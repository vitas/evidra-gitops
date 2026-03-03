package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"evidra/internal/api"
	ce "evidra/internal/cloudevents"
	"evidra/internal/config"
	"evidra/internal/export"
	"evidra/internal/ingest"
	"evidra/internal/ingest/argo"
	"evidra/internal/migrate"
	"evidra/internal/observability"
	"evidra/internal/store"

	"github.com/XSAM/otelsql"
	"github.com/go-logr/logr"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	_ "github.com/jackc/pgx/v5/stdlib"

	_ "modernc.org/sqlite"
)

type Runtime struct {
	Handler http.Handler
	Cleanup func()
}

func NewRuntime(ctx context.Context, cfg config.Config, logger logr.Logger, telemetry *observability.TelemetryProviders) *Runtime {
	repo, cleanup := buildRepository(ctx, cfg, logger)
	startArgoCollector(ctx, logger, cfg, repo)
	if cfg.K8s.CollectorEnabled {
		logger.Info("kubernetes collector configuration ignored: out of scope in Argo CD-first v1")
	}

	exporter := export.NewFilesystemExporter(cfg.ExportDir)
	server := api.NewServerWithOptions(repo, exporter, api.ServerOptions{
		Auth: api.AuthConfig{
			Read: api.BearerPolicy{
				Token: cfg.Auth.Read.Token,
			},
			Ingest: api.IngestPolicy{
				Bearer: api.BearerPolicy{
					Token: cfg.Auth.Ingest.Bearer.Token,
				},
				GenericWebhook: api.HMACPolicy{
					Secret: cfg.Auth.Ingest.GenericWebhook.Secret,
				},
			},
			OIDC: api.OIDCPolicy{
				Enabled:     cfg.Auth.OIDC.Enabled,
				RolesHeader: cfg.Auth.OIDC.RolesHeader,
			},
			JWT: api.JWTPolicy{
				Enabled:           cfg.Auth.JWT.Enabled,
				Issuer:            cfg.Auth.JWT.Issuer,
				Audience:          cfg.Auth.JWT.Audience,
				RolesClaim:        cfg.Auth.JWT.RolesClaim,
				HS256Secret:       cfg.Auth.JWT.HS256Secret,
				RS256PublicKeyPEM: cfg.Auth.JWT.RS256PublicKeyPEM,
				JWKSURL:           cfg.Auth.JWT.JWKSURL,
				JWKSRefresh:       cfg.Auth.JWT.JWKSRefresh.String(),
			},
			Audit: api.AuditPolicy{
				LogFile: cfg.Auth.Audit.LogFile,
			},
			Rate: api.RateLimitPolicy{
				Enabled:         cfg.Auth.RateLimit.Enabled,
				ReadPerMinute:   cfg.Auth.RateLimit.ReadPerMinute,
				ExportPerMinute: cfg.Auth.RateLimit.ExportPerMinute,
				IngestPerMinute: cfg.Auth.RateLimit.IngestPerMinute,
			},
		},
		WebhookRegistry: buildWebhookRegistry(cfg),
		ArgoOnlyMode:    true,
		Logger:          logger.WithName("api"),
	})

	handler := otelhttp.NewHandler(server.Routes(), "evidra",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
	)

	rootMux := http.NewServeMux()
	if telemetry != nil && telemetry.MetricsHandler != nil {
		rootMux.Handle("/metrics", telemetry.MetricsHandler)
	}
	rootMux.Handle("/", handler)

	return &Runtime{
		Handler: rootMux,
		Cleanup: cleanup,
	}
}

func buildWebhookRegistry(cfg config.Config) *ingest.Registry {
	_ = cfg
	return ingest.NewRegistry()
}

func buildRepository(ctx context.Context, cfg config.Config, logger logr.Logger) (store.Repository, func()) {
	if cfg.DBDriver == "" || cfg.DBDSN == "" {
		logger.Info("running with in-memory repository")
		return store.NewMemoryRepository(), func() {}
	}

	fatal := func(msg string, err error) {
		if cfg.DevInsecure {
			logger.Info("falling back to in-memory repository (dev mode)", "error", err.Error(), "reason", msg)
		} else {
			logger.Error(err, msg)
			fmt.Fprintf(os.Stderr, "FATAL: %s: %v\n", msg, err)
			os.Exit(1)
		}
	}

	dsn := applyPostgresTLS(cfg)
	db, err := otelsql.Open(cfg.DBDriver, dsn,
		otelsql.WithAttributes(semconv.DBSystemKey.String(dbSystem(cfg.DBDialect))),
	)
	if err != nil {
		fatal("db open failed", err)
		return store.NewMemoryRepository(), func() {}
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		fatal("db ping failed", err)
		return store.NewMemoryRepository(), func() {}
	}

	if cfg.DBMigrate {
		runner := migrate.NewRunner(os.DirFS("."))
		if err := runner.Apply(ctx, db, cfg.DBDialect); err != nil {
			_ = db.Close()
			fatal("migration apply failed", err)
			return store.NewMemoryRepository(), func() {}
		}
	}

	repo, err := store.NewSQLRepository(db, cfg.DBDialect)
	if err != nil {
		_ = db.Close()
		fatal("sql repository init failed", err)
		return store.NewMemoryRepository(), func() {}
	}
	logger.Info("running with SQL repository", "dialect", cfg.DBDialect)
	return repo, func() { _ = db.Close() }
}

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

func applyPostgresTLS(cfg config.Config) string {
	driver := strings.ToLower(strings.TrimSpace(cfg.DBDriver))
	if driver != "pgx" {
		return cfg.DBDSN
	}
	if strings.TrimSpace(cfg.DB.SSLMode) == "" &&
		strings.TrimSpace(cfg.DB.SSLRootCert) == "" &&
		strings.TrimSpace(cfg.DB.SSLCert) == "" &&
		strings.TrimSpace(cfg.DB.SSLKey) == "" {
		return cfg.DBDSN
	}
	u, err := url.Parse(cfg.DBDSN)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return cfg.DBDSN
	}
	q := u.Query()
	if strings.TrimSpace(cfg.DB.SSLMode) != "" {
		q.Set("sslmode", strings.TrimSpace(cfg.DB.SSLMode))
	}
	if strings.TrimSpace(cfg.DB.SSLRootCert) != "" {
		q.Set("sslrootcert", strings.TrimSpace(cfg.DB.SSLRootCert))
	}
	if strings.TrimSpace(cfg.DB.SSLCert) != "" {
		q.Set("sslcert", strings.TrimSpace(cfg.DB.SSLCert))
	}
	if strings.TrimSpace(cfg.DB.SSLKey) != "" {
		q.Set("sslkey", strings.TrimSpace(cfg.DB.SSLKey))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func startArgoCollector(ctx context.Context, logger logr.Logger, cfg config.Config, repo store.Repository) {
	if !cfg.Argo.CollectorEnabled {
		return
	}
	if strings.TrimSpace(cfg.Argo.APIURL) == "" {
		logger.Info("argo collector enabled but EVIDRA_ARGO_API_URL is empty; collector not started")
		return
	}

	var dynClient dynamic.Interface
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		kubeconfigPath := os.Getenv("KUBECONFIG")
		if kubeconfigPath == "" {
			kubeconfigPath = os.ExpandEnv("$HOME/.kube/config")
		}
		kubeConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			logger.Error(err, "argo collector could not construct kubernetes config, dynamic watch will fail")
		}
	}
	if kubeConfig != nil {
		dynClient, err = dynamic.NewForConfig(kubeConfig)
		if err != nil {
			logger.Error(err, "argo collector failed to create dynamic client")
		}
	}

	var fetchFn argo.FetchFunc
	if dynClient == nil && strings.TrimSpace(cfg.Argo.APIToken) != "" {
		fn, ferr := argo.NewFetchFunc(argo.BackendOptions{
			Backend: cfg.Argo.Backend,
			URL:     cfg.Argo.APIURL,
			Token:   cfg.Argo.APIToken,
		})
		if ferr != nil {
			logger.Error(ferr, "argo collector failed to build fetch function; REST polling unavailable")
		} else {
			fetchFn = fn
		}
	}

	collector := &argo.Collector{
		DynamicClient: dynClient,
		Namespace:     "argocd",
		Normalize: func(se argo.SourceEvent) (ce.StoredEvent, error) {
			return argo.NormalizeSourceEvent(se, cfg.Argo.DefaultEnv)
		},
		Sink:   repo,
		Logger: logger.WithName("argo-collector"),
		Checkpoint: argo.FileCheckpointStore{
			Path: cfg.Argo.CheckpointFile,
		},
		Fetch:    fetchFn,
		Interval: cfg.Argo.CollectorInterval,
	}
	go collector.Start(ctx)
	logger.Info("argo collector started", "interval", cfg.Argo.CollectorInterval.String(), "api_url", cfg.Argo.APIURL, "dynamic_client_ready", dynClient != nil)
}
