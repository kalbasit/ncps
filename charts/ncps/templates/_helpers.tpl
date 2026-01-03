{{/*
Expand the name of the chart.
*/}}
{{- define "ncps.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "ncps.fullname" -}}
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
{{- define "ncps.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "ncps.labels" -}}
helm.sh/chart: {{ include "ncps.chart" . }}
{{ include "ncps.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "ncps.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ncps.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "ncps.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "ncps.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Return the proper ncps image name
*/}}
{{- define "ncps.image" -}}
{{- $registryName := .Values.image.registry -}}
{{- $repositoryName := .Values.image.repository -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- if .Values.global.imageRegistry }}
  {{- $registryName = .Values.global.imageRegistry -}}
{{- end -}}
{{- printf "%s/%s:%s" $registryName $repositoryName $tag -}}
{{- end -}}

{{/*
Return the proper init image name for directory creation
*/}}
{{- define "ncps.initImage" -}}
{{- $registryName := .Values.initImage.registry -}}
{{- $repositoryName := .Values.initImage.repository -}}
{{- $tag := .Values.initImage.tag -}}
{{- if .Values.global.imageRegistry }}
  {{- $registryName = .Values.global.imageRegistry -}}
{{- end -}}
{{- printf "%s/%s:%s" $registryName $repositoryName $tag -}}
{{- end -}}

{{/*
Build database URL from configuration
*/}}
{{- define "ncps.databaseURL" -}}
{{- if eq .Values.config.database.type "sqlite" -}}
sqlite:{{ .Values.config.database.sqlite.path }}
{{- else if eq .Values.config.database.type "postgresql" -}}
{{- $pass := .Values.config.database.postgresql.password -}}
{{- if .Values.config.database.postgresql.existingSecret -}}
{{- $pass = printf "${POSTGRES_PASSWORD}" -}}
{{- end -}}
postgresql://{{ .Values.config.database.postgresql.username | urlquery }}:{{ $pass | urlquery }}@{{ .Values.config.database.postgresql.host }}:{{ .Values.config.database.postgresql.port }}/{{ .Values.config.database.postgresql.database }}?sslmode={{ .Values.config.database.postgresql.sslMode }}{{ if .Values.config.database.postgresql.extraParams }}&{{ .Values.config.database.postgresql.extraParams }}{{ end }}
{{- else if eq .Values.config.database.type "mysql" -}}
{{- $pass := .Values.config.database.mysql.password -}}
{{- if .Values.config.database.mysql.existingSecret -}}
{{- $pass = printf "${MYSQL_PASSWORD}" -}}
{{- end -}}
mysql://{{ .Values.config.database.mysql.username | urlquery }}:{{ $pass | urlquery }}@{{ .Values.config.database.mysql.host }}:{{ .Values.config.database.mysql.port }}/{{ .Values.config.database.mysql.database }}{{ if .Values.config.database.mysql.extraParams }}?{{ .Values.config.database.mysql.extraParams }}{{ end }}
{{- end -}}
{{- end -}}

{{/*
Cache database URL environment variable
Returns the CACHE_DATABASE_URL env var config - either from value or secretKeyRef
*/}}
{{- define "ncps.cacheDatabaseURLEnv" -}}
- name: CACHE_DATABASE_URL
{{- if eq .Values.config.database.type "sqlite" }}
  value: {{ include "ncps.databaseURL" . | quote }}
{{- else if or (and (eq .Values.config.database.type "postgresql") .Values.config.database.postgresql.password) (and (eq .Values.config.database.type "mysql") .Values.config.database.mysql.password) }}
  valueFrom:
    secretKeyRef:
      name: {{ include "ncps.fullname" . }}
      key: database-url
{{- else if and (eq .Values.config.database.type "postgresql") .Values.config.database.postgresql.existingSecret }}
  valueFrom:
    secretKeyRef:
      name: {{ .Values.config.database.postgresql.existingSecret }}
      key: database-url
{{- else if and (eq .Values.config.database.type "mysql") .Values.config.database.mysql.existingSecret }}
  valueFrom:
    secretKeyRef:
      name: {{ .Values.config.database.mysql.existingSecret }}
      key: database-url
{{- end }}
{{- end -}}

{{/*
Database URL environment variable for migration
Returns the DATABASE_URL env var config - either from value or secretKeyRef
*/}}
{{- define "ncps.migrationDatabaseURLEnv" -}}
- name: DATABASE_URL
{{- if eq .Values.config.database.type "sqlite" }}
  value: {{ include "ncps.databaseURL" . | quote }}
{{- else if or (and (eq .Values.config.database.type "postgresql") .Values.config.database.postgresql.password) (and (eq .Values.config.database.type "mysql") .Values.config.database.mysql.password) }}
  valueFrom:
    secretKeyRef:
      name: {{ include "ncps.fullname" . }}
      key: database-url
{{- else if and (eq .Values.config.database.type "postgresql") .Values.config.database.postgresql.existingSecret }}
  valueFrom:
    secretKeyRef:
      name: {{ .Values.config.database.postgresql.existingSecret }}
      key: database-url
{{- else if and (eq .Values.config.database.type "mysql") .Values.config.database.mysql.existingSecret }}
  valueFrom:
    secretKeyRef:
      name: {{ .Values.config.database.mysql.existingSecret }}
      key: database-url
{{- end }}
{{- end -}}

{{/*
Validate configuration for incompatible settings
This function will fail the template rendering if invalid configurations are detected
*/}}
{{- define "ncps.validate" -}}

{{- /* HA mode validation */ -}}
{{- if gt (int .Values.replicaCount) 1 -}}
  {{- /* HA requires Redis */ -}}
  {{- if not .Values.config.redis.enabled -}}
    {{- fail "High availability mode (replicaCount > 1) requires Redis to be enabled (config.redis.enabled=true)" -}}
  {{- end -}}

  {{- /* HA cannot use SQLite */ -}}
  {{- if eq .Values.config.database.type "sqlite" -}}
    {{- fail "High availability mode (replicaCount > 1) is not compatible with SQLite. Use PostgreSQL or MySQL instead (config.database.type)" -}}
  {{- end -}}

  {{- /* HA with Deployment should use S3 or shared storage */ -}}
  {{- if and (eq .Values.config.storage.type "local") (eq .Values.mode "deployment") -}}
    {{- /* Allow if using existingClaim (user-managed) or ReadWriteMany access mode */ -}}
    {{- if not .Values.config.storage.local.persistence.existingClaim -}}
      {{- if not (has "ReadWriteMany" .Values.config.storage.local.persistence.accessModes) -}}
        {{- fail "High availability mode with Deployment requires S3 storage (config.storage.type='s3'), existing shared PVC (config.storage.local.persistence.existingClaim), or ReadWriteMany access mode, or use StatefulSet mode" -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{- /* Storage validation */ -}}
{{- if eq .Values.config.storage.type "s3" -}}
  {{- if not .Values.config.storage.s3.bucket -}}
    {{- fail "S3 storage requires bucket name (config.storage.s3.bucket)" -}}
  {{- end -}}
  {{- if not .Values.config.storage.s3.endpoint -}}
    {{- fail "S3 storage requires endpoint (config.storage.s3.endpoint)" -}}
  {{- end -}}
  {{- if and (not .Values.config.storage.s3.existingSecret) (or (not .Values.config.storage.s3.accessKeyId) (not .Values.config.storage.s3.secretAccessKey)) -}}
    {{- fail "S3 storage requires credentials (config.storage.s3.accessKeyId/secretAccessKey or config.storage.s3.existingSecret)" -}}
  {{- end -}}
{{- end -}}

{{- /* Database validation */ -}}
{{- if eq .Values.config.database.type "postgresql" -}}
  {{- if not .Values.config.database.postgresql.host -}}
    {{- fail "PostgreSQL requires host (config.database.postgresql.host)" -}}
  {{- end -}}
  {{- /* Prevent setting both password and existingSecret */ -}}
  {{- if and .Values.config.database.postgresql.password .Values.config.database.postgresql.existingSecret -}}
    {{- fail "PostgreSQL: cannot set both 'password' and 'existingSecret'. Use either password (stored in chart-managed secret) or existingSecret (your secret with 'database-url' key)" -}}
  {{- end -}}
{{- else if eq .Values.config.database.type "mysql" -}}
  {{- if not .Values.config.database.mysql.host -}}
    {{- fail "MySQL requires host (config.database.mysql.host)" -}}
  {{- end -}}
  {{- /* Prevent setting both password and existingSecret */ -}}
  {{- if and .Values.config.database.mysql.password .Values.config.database.mysql.existingSecret -}}
    {{- fail "MySQL: cannot set both 'password' and 'existingSecret'. Use either password (stored in chart-managed secret) or existingSecret (your secret with 'database-url' key)" -}}
  {{- end -}}
{{- end -}}

{{- /* LRU schedule requires max size */ -}}
{{- if and .Values.config.cache.lruSchedule (not .Values.config.cache.maxSize) -}}
  {{- fail "LRU schedule (config.cache.lruSchedule) requires max cache size (config.cache.maxSize)" -}}
{{- end -}}

{{- /* Redis validation */ -}}
{{- if .Values.config.redis.enabled -}}
  {{- if not .Values.config.redis.addresses -}}
    {{- fail "Redis enabled but no addresses provided (config.redis.addresses)" -}}
  {{- end -}}
{{- end -}}

{{- /* Upstream validation */ -}}
{{- if not .Values.config.upstream.caches -}}
  {{- fail "At least one upstream cache is required (config.upstream.caches)" -}}
{{- end -}}

{{- /* Mode validation */ -}}
{{- if and (ne .Values.mode "deployment") (ne .Values.mode "statefulset") -}}
  {{- fail "mode must be either 'deployment' or 'statefulset'" -}}
{{- end -}}

{{- end -}}
