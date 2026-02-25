package observability

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"evidra/internal/config"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TelemetryProviders holds initialized OTel providers and their HTTP handler
// for the /metrics endpoint.
type TelemetryProviders struct {
	MetricsHandler http.Handler
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
	res, err := buildResource(cfg)
	if err != nil {
		return nil, err
	}

	tp, err := buildTracerProvider(ctx, cfg, res)
	if err != nil {
		return nil, err
	}
	if tp != nil {
		otel.SetTracerProvider(tp)
	}

	mp, metricsHandler, err := buildMeterProvider(cfg, res)
	if err != nil {
		if tp != nil {
			_ = tp.Shutdown(ctx)
		}
		return nil, err
	}
	if mp != nil {
		otel.SetMeterProvider(mp)
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	shutdown := func(ctx context.Context) error {
		var errs []error
		if tp != nil {
			errs = append(errs, tp.Shutdown(ctx))
		}
		if mp != nil {
			errs = append(errs, mp.Shutdown(ctx))
		}
		return errors.Join(errs...)
	}

	return &TelemetryProviders{
		MetricsHandler: metricsHandler,
		shutdown:       shutdown,
	}, nil
}

func buildResource(cfg config.OTelConfig) (*resource.Resource, error) {
	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "evidra"
	}
	attrs := []resource.Option{
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, resource.WithAttributes(semconv.ServiceVersion(cfg.ServiceVersion)))
	}
	return resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(serviceName)))
}

func buildTracerProvider(ctx context.Context, cfg config.OTelConfig, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	exporter := strings.ToLower(strings.TrimSpace(cfg.TracesExporter))
	if exporter == "" || exporter == "none" {
		return nil, nil
	}

	sampler := buildSampler(cfg)

	switch exporter {
	case "otlp":
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.ExporterEndpoint),
		}
		if cfg.ExporterInsecure {
			opts = append(opts, otlptracegrpc.WithDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())))
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		exp, err := otlptracegrpc.New(ctx, opts...)
		if err != nil {
			return nil, err
		}
		return sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(exp),
			sdktrace.WithSampler(sampler),
		), nil

	case "stdout":
		exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, err
		}
		return sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithSyncer(exp),
			sdktrace.WithSampler(sampler),
		), nil

	default:
		return nil, nil
	}
}

func buildSampler(cfg config.OTelConfig) sdktrace.Sampler {
	samplerType := strings.ToLower(strings.TrimSpace(cfg.SamplerType))
	arg := cfg.SamplerArg
	if arg <= 0 {
		arg = 1.0
	}
	switch samplerType {
	case "always_on":
		return sdktrace.AlwaysSample()
	case "always_off":
		return sdktrace.NeverSample()
	case "traceidratio":
		return sdktrace.TraceIDRatioBased(arg)
	default: // parentbased_traceidratio
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(arg))
	}
}

func buildMeterProvider(cfg config.OTelConfig, res *resource.Resource) (*sdkmetric.MeterProvider, http.Handler, error) {
	exporter := strings.ToLower(strings.TrimSpace(cfg.MetricsExporter))
	if exporter == "" || exporter == "none" {
		return nil, nil, nil
	}

	switch exporter {
	case "prometheus":
		registry := prometheus.NewRegistry()
		promExp, err := promexporter.New(promexporter.WithRegisterer(registry))
		if err != nil {
			return nil, nil, err
		}
		mp := sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(promExp),
		)
		handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
		return mp, handler, nil

	default:
		return nil, nil, nil
	}
}
