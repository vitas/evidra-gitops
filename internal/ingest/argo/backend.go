package argo

import (
	"context"
	"fmt"
	"strings"
)

// FetchFunc is the polling-based fetch interface, kept for compatibility with
// client_fetch_argocd.go. The primary ingestion path uses the Kubernetes
// dynamic informer in Collector.
type FetchFunc func(ctx context.Context) ([]SourceEvent, error)

type BackendOptions struct {
	Backend string
	URL     string
	Token   string
}

func NewFetchFunc(opts BackendOptions) (FetchFunc, error) {
	backend := strings.ToLower(strings.TrimSpace(opts.Backend))
	if backend == "" || backend == "argocd-client" {
		return NewArgoCDClientFetcher(opts)
	}
	return nil, fmt.Errorf("unsupported argo backend: %s", opts.Backend)
}
