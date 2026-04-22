{{/*
Expand the name of the chart.
*/}}
{{- define "cluster-api-provider-evroc.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "cluster-api-provider-evroc.fullname" -}}
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

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "cluster-api-provider-evroc.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "cluster-api-provider-evroc.labels" -}}
helm.sh/chart: {{ include "cluster-api-provider-evroc.chart" . }}
{{ include "cluster-api-provider-evroc.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
cluster.x-k8s.io/provider: infrastructure-evroc
{{- end }}

{{/*
Selector labels
*/}}
{{- define "cluster-api-provider-evroc.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cluster-api-provider-evroc.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
control-plane: controller-manager
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "cluster-api-provider-evroc.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "cluster-api-provider-evroc.fullname" . | printf "%s-controller-manager") .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the config secret
*/}}
{{- define "cluster-api-provider-evroc.configSecretName" -}}
{{- required "evroc.existingConfigSecret is required" .Values.evroc.existingConfigSecret }}
{{- end }}
