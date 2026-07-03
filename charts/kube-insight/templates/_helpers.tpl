{{- define "kube-insight.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kube-insight.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "kube-insight.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kube-insight.labels" -}}
helm.sh/chart: {{ include "kube-insight.chart" . }}
app.kubernetes.io/name: {{ include "kube-insight.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "kube-insight.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kube-insight.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "kube-insight.serviceAccountName" -}}
{{- if .Values.rbac.serviceAccount.create -}}
{{- default (include "kube-insight.fullname" .) .Values.rbac.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.rbac.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "kube-insight.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}

{{- define "kube-insight.role" -}}
{{- if eq .Values.mode "writer" -}}writer{{- else if eq .Values.mode "api" -}}api{{- else -}}all{{- end -}}
{{- end -}}

{{- define "kube-insight.collectionEnabled" -}}
{{- if eq .Values.mode "api" -}}false{{- else -}}true{{- end -}}
{{- end -}}

{{- define "kube-insight.readSurfacesEnabled" -}}
{{- if eq .Values.mode "writer" -}}false{{- else -}}true{{- end -}}
{{- end -}}

{{- define "kube-insight.commandArgs" -}}
{{- if eq .Values.mode "writer" -}}
- --config
- /etc/kube-insight/config.yaml
- serve
- --watch
{{- else if eq .Values.mode "api" -}}
- --config
- /etc/kube-insight/config.yaml
- serve
- --app
{{- if ne .Values.storage.driver "chdb" }}
- --metrics
{{- end }}
- --listen
- {{ .Values.server.listen | quote }}
{{- else -}}
- --config
- /etc/kube-insight/config.yaml
- serve
- --watch
- --app
{{- if ne .Values.storage.driver "chdb" }}
- --metrics
{{- end }}
- --listen
- {{ .Values.server.listen | quote }}
{{- end -}}
{{- end -}}

{{- define "kube-insight.mcpURL" -}}
{{- if .Values.kagent.remoteMCPServer.url -}}
{{- .Values.kagent.remoteMCPServer.url -}}
{{- else -}}
{{- printf "http://%s.%s.svc.cluster.local:%v/mcp" (include "kube-insight.fullname" .) .Release.Namespace .Values.service.port -}}
{{- end -}}
{{- end -}}
