Part of the Evidra OSS toolset by SameBits.

# Kubernetes Deployment (Kustomize)

## Layout

- Base: `deploy/k8s/base`
- Overlays:
  - `deploy/k8s/overlays/trial`
  - `deploy/k8s/overlays/prod`
  - `deploy/k8s/overlays/openshift`
- Optional addon:
  - `deploy/k8s/addons/postgres`

## Secret workflow

All sensitive runtime values are generated into `Secret/evidra-gitops-secrets` from:

- `deploy/k8s/overlays/<overlay>/secrets.env` (local, not committed)
- template: `deploy/k8s/overlays/<overlay>/secrets.env.example`

Setup:

```bash
cp deploy/k8s/overlays/trial/secrets.env.example deploy/k8s/overlays/trial/secrets.env
# edit deploy/k8s/overlays/trial/secrets.env
```

Default required keys:
- `EVIDRA_DB_HOST`
- `EVIDRA_DB_PORT`
- `EVIDRA_DB_NAME`
- `EVIDRA_DB_USER`
- `EVIDRA_DB_PASSWORD`
- `EVIDRA_READ_TOKEN`

Optional keys:
- `EVIDRA_ARGO_API_TOKEN` (collector token auth)
- `EVIDRA_INGEST_TOKEN` (ingest auth)

## Trial install

```bash
# One-command local path:
make trial-apply

# Manual path:
kubectl apply -k deploy/k8s/addons/postgres
kubectl apply -k deploy/k8s/overlays/trial
kubectl -n evidra-gitops get pods
kubectl -n evidra-gitops logs deploy/evidra-gitops-trial
```

## Prod install

```bash
kubectl apply -k deploy/k8s/overlays/prod
kubectl -n evidra-gitops get pods
kubectl -n evidra-gitops logs deploy/evidra-gitops-prod
```

## OpenShift install

```bash
oc apply -k deploy/k8s/overlays/openshift
oc -n evidra-gitops get pods
oc -n evidra-gitops get route
```

## Argo CD Applications

- `deploy/argocd/application-dev.yaml` -> trial overlay
- `deploy/argocd/application-staging.yaml` -> prod overlay
- `deploy/argocd/application-prod.yaml` -> prod overlay

Update `repoURL` before applying.

## Operations reference

For minimum operational guidance in real Argo CD environments (read-only permissions, token rotation, polling safety, observability, troubleshooting), see:

- `docs/setup/ops-minimum.md`
