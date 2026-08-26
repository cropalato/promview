{{- define "promview.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "promview.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "promview.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "promview.selectorLabels" -}}
app.kubernetes.io/name: {{ include "promview.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "promview.labels" -}}
helm.sh/chart: {{ include "promview.chart" . }}
{{ include "promview.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "promview.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "promview.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "promview.image" -}}
{{- if .Values.image.digest }}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest }}
{{- else }}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}
{{- end }}

{{- define "promview.validateValues" -}}
{{- $databaseSecret := required "database.existingSecret is required" .Values.database.existingSecret -}}
{{- $databaseKey := required "database.urlKey is required" .Values.database.urlKey -}}
{{- if and .Values.image.tag .Values.image.digest -}}
{{- fail "image.tag and image.digest are mutually exclusive" -}}
{{- end -}}
{{- if eq .Values.auth.mode "oidc" -}}
{{- $issuer := required "oidc.issuerURL is required when auth.mode=oidc" .Values.oidc.issuerURL -}}
{{- $clientID := required "oidc.clientID is required when auth.mode=oidc" .Values.oidc.clientID -}}
{{- $oidcSecret := required "oidc.existingSecret is required when auth.mode=oidc" .Values.oidc.existingSecret -}}
{{- $clientSecretKey := required "oidc.clientSecretKey is required when auth.mode=oidc" .Values.oidc.clientSecretKey -}}
{{- $redirectURL := required "oidc.redirectURL is required when auth.mode=oidc" .Values.oidc.redirectURL -}}
{{- end -}}
{{- if .Values.bootstrapSource.enabled -}}
{{- $sourceSlug := required "bootstrapSource.slug is required when bootstrapSource.enabled=true" .Values.bootstrapSource.slug -}}
{{- $sourceSecret := required "bootstrapSource.existingSecret is required when bootstrapSource.enabled=true" .Values.bootstrapSource.existingSecret -}}
{{- $sourceTokenKey := required "bootstrapSource.tokenKey is required when bootstrapSource.enabled=true" .Values.bootstrapSource.tokenKey -}}
{{- end -}}
{{- if .Values.roleBindings -}}
{{- if ne .Values.auth.mode "oidc" -}}
{{- fail "roleBindings require auth.mode=oidc" -}}
{{- end -}}
{{- range .Values.roleBindings -}}
{{- if and (eq .role "administrator") .selectors -}}
{{- fail "administrator roleBindings cannot include selectors" -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
Whether a ServiceMonitor will actually be rendered.

Gated on the CRD existing, not only on the value: a cluster without the
Prometheus Operator would otherwise be handed a manifest its API server rejects.
The Deployment asks the same question so that a cluster falling back to scrape
annotations gets them, and a cluster with the operator does not get scraped
twice under two different job names.

Note that `helm template` reports no API versions, so this renders nothing
locally. `--api-versions monitoring.coreos.com/v1` shows what a cluster with the
operator would get.
*/}}
{{- define "promview.serviceMonitorEnabled" -}}
{{- if and .Values.metrics.enabled .Values.metrics.serviceMonitor.enabled (.Capabilities.APIVersions.Has "monitoring.coreos.com/v1") -}}
true
{{- end -}}
{{- end -}}

{{/*
Whether a PrometheusRule will actually be rendered. Gated on the same CRD as the
ServiceMonitor, for the same reason: a cluster without the operator would
otherwise be handed a manifest its API server rejects.
*/}}
{{- define "promview.prometheusRuleEnabled" -}}
{{- if and .Values.metrics.enabled .Values.metrics.prometheusRule.enabled (.Capabilities.APIVersions.Has "monitoring.coreos.com/v1") -}}
true
{{- end -}}
{{- end -}}
