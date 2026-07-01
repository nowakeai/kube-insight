{{- define "kube-insight-kagent-agent.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kube-insight-kagent-agent.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kube-insight-kagent-agent.labels" -}}
helm.sh/chart: {{ include "kube-insight-kagent-agent.chart" . }}
app.kubernetes.io/name: {{ include "kube-insight-kagent-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: kube-insight
{{- end -}}

{{- define "kube-insight-kagent-agent.systemMessageBase" -}}
{{`{{ include "kagent/kubernetes-context" }}`}}

{{`{{ include "kagent/tool-usage-best-practices" }}`}}

{{`{{ include "kagent/safety-guardrails" }}`}}

{{`{{ include "kubeinsight/retained-evidence.md" }}`}}

{{`{{ include "kubeinsight/sql-and-js.md" }}`}}

{{`{{ include "kubeinsight/tool-routing.md" }}`}}

{{`{{ include "kubeinsight/answer-contract.md" }}`}}
{{- end -}}

{{- define "kube-insight-kagent-agent.systemMessageFullstack" -}}
{{ include "kube-insight-kagent-agent.systemMessageBase" . }}

Investigation workflow:
1. Triage scope, time window, cluster/context, and user intent.
2. Check kube-insight collector health and retained evidence coverage.
3. Pull live Kubernetes state only where current validation matters.
4. Query Prometheus for metrics and time series with explicit windows.
5. Use Loki tools for time-bounded log evidence when they are installed; pass
   explicit RFC3339 start/end timestamps, not relative strings such as "15m".
6. Use Helm, Argo, Cilium, Istio, and Kubescape only when relevant.
7. Use shell only for bounded glue work or checks that no direct tool covers.
   Never expose secrets from logs, manifests, env, or shell.
8. Answer with evidence, confidence, gaps, and the next safe action.
{{- end -}}
