Part of the Evidra OSS toolset by SameBits.

# Customer Adoption Guide (v1)

Evidra-GitOps v1 is Argo CD-first.

Primary integration surface:
- Argo CD read-only API and application history
- Argo sync/health/operation transitions
- Argo revision metadata

Out of scope in v1 core behavior:
- standalone Kubernetes event collection
- multi-provider ingestion abstractions

Implementation note:
- Use Argo collector data and Argo-derived evidence for investigation timelines in v1.

## Customer Integration Instructions: `evidra.rest/*` annotations

Use Argo `Application` annotations as the primary correlation mechanism.

Recommended keys:
- `evidra.rest/change-id`
- `evidra.rest/ticket`
- `evidra.rest/approvals-ref`
- `evidra.rest/approvals-json` (optional compact approval summary)

Easiest automation path:
1. Standardize one annotation block in all `Application` manifests.
2. If you use `ApplicationSet`, place the same annotation block in `spec.template.metadata.annotations` so generated Applications inherit it.
3. Keep annotation values in Git templates (Helm values, Kustomize patches, or templated manifests) so correlation data is versioned with the change.
4. Add a CI/PR check that fails production-targeted changes when required keys are missing.

Practical boundary:
- Use `Application` (or generated `Application`) as authoritative carrier of `evidra.rest/*` keys.
- Do not use workload-level annotations (`Deployment`, `Pod`) as primary evidence linkage.

Suggested bootstrap pattern for customer repos:
- Add a small contract file (example: `.evidra-gitops/change-context.yaml`) containing change/ticket/approval reference values.
- Render that contract into Argo `Application` annotations during manifest generation.

### Helm snippet

Values:

```yaml
evidra:
  changeId: CHG123456
  ticket: JIRA-42
  approvalsRef: APR-7
```

Application template:

```yaml
metadata:
  annotations:
    evidra.rest/change-id: {{ .Values.evidra.changeId | quote }}
    evidra.rest/ticket: {{ .Values.evidra.ticket | quote }}
    evidra.rest/approvals-ref: {{ .Values.evidra.approvalsRef | quote }}
```

### Kustomize snippet

`kustomization.yaml`:

```yaml
commonAnnotations:
  evidra.rest/change-id: CHG123456
  evidra.rest/ticket: JIRA-42
  evidra.rest/approvals-ref: APR-7
```

### ApplicationSet snippet

```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
spec:
  template:
    metadata:
      annotations:
        evidra.rest/change-id: CHG123456
        evidra.rest/ticket: JIRA-42
        evidra.rest/approvals-ref: APR-7
```

### CI validation example (required keys)

Fail pipeline if any production `Application` misses required keys:

```bash
#!/usr/bin/env bash
set -euo pipefail

required_keys=("evidra.rest/change-id" "evidra.rest/ticket")

for f in $(git diff --name-only origin/main...HEAD | rg 'application.*\.ya?ml$'); do
  for k in "${required_keys[@]}"; do
    yq -e ".metadata.annotations.\"${k}\"" "$f" >/dev/null
  done
done
```
