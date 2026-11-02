{{/* Common labels for one engine. */}}
{{- define "dbpantry.labels" -}}
app.kubernetes.io/name: dbpantry
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
app.kubernetes.io/component: {{ .name }}
helm.sh/chart: {{ printf "%s-%s" .root.Chart.Name .root.Chart.Version }}
{{- end }}

{{- define "dbpantry.selector" -}}
app.kubernetes.io/name: dbpantry
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .name }}
{{- end }}

{{/* StatefulSet name: <release>-<engine>, at most 52 characters so pod and PVC names fit. */}}
{{- define "dbpantry.fullname" -}}
{{- printf "%s-%s" .root.Release.Name .name | trunc 52 | trimSuffix "-" -}}
{{- end }}

{{/* Client Service name: the Compose service name unless prefixed. */}}
{{- define "dbpantry.svcname" -}}
{{- if .root.Values.service.prefixWithRelease -}}
{{- include "dbpantry.fullname" . -}}
{{- else -}}
{{- .name -}}
{{- end -}}
{{- end }}

{{- define "dbpantry.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (printf "%s-dbpantry" .Release.Name) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end }}

{{/* Compose-style sizes (4g, 512m) to Kubernetes quantities (4Gi, 512Mi). */}}
{{- define "dbpantry.qty" -}}
{{- $v := toString . -}}
{{- if regexMatch "^[0-9.]+[gG]$" $v -}}{{ trimSuffix "G" (trimSuffix "g" $v) }}Gi
{{- else if regexMatch "^[0-9.]+[mM]$" $v -}}{{ trimSuffix "M" (trimSuffix "m" $v) }}Mi
{{- else if regexMatch "^[0-9.]+[kK]$" $v -}}{{ trimSuffix "K" (trimSuffix "k" $v) }}Ki
{{- else -}}{{ $v }}
{{- end -}}
{{- end }}

{{- define "dbpantry.image" -}}
{{- if .engine.imageRef -}}
{{- .engine.imageRef -}}
{{- else -}}
{{- printf "%s/%s:%s" .root.Values.global.imageRegistry .engine.image (toString .engine.tag) -}}
{{- end -}}
{{- end }}
