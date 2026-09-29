{{- define "rampa.name" -}}
{{ required "app.name es requerido" .Values.app.name }}
{{- end }}

{{- define "rampa.namespace" -}}
{{ printf "%s-%s" (include "rampa.name" .) (required "scope.name es requerido" .Values.scope.name) }}
{{- end }}

{{- define "rampa.image" -}}
{{ printf "%s:%s" (required "image.repository es requerido" .Values.image.repository) (required "image.tag es requerido" .Values.image.tag) }}
{{- end }}

{{- define "rampa.isWeb" -}}
{{- if or (eq .Values.app.type "api") (eq .Values.app.type "static") }}true{{ end -}}
{{- end }}

{{- define "rampa.isWorkload" -}}
{{- if ne .Values.app.type "cron" }}true{{ end -}}
{{- end }}

{{- define "rampa.useRollout" -}}
{{- if and (include "rampa.isWorkload" .) (ne .Values.strategy "rolling") }}true{{ end -}}
{{- end }}

{{- define "rampa.host" -}}
{{- if .Values.ingress.host }}{{ .Values.ingress.host }}{{ else }}{{ printf "%s.%s" (include "rampa.namespace" .) .Values.cluster.domain }}{{ end -}}
{{- end }}

{{- define "rampa.healthcheck" -}}
{{- if eq .Values.app.type "static" }}{{ .Values.healthcheck | default "/" }}{{ else }}{{ .Values.healthcheck | default "/health" }}{{ end -}}
{{- end }}

{{- define "rampa.labels" -}}
rampa.dev/app: {{ include "rampa.name" . | quote }}
rampa.dev/scope: {{ .Values.scope.name | quote }}
rampa.dev/team: {{ required "app.team es requerido" .Values.app.team | quote }}
rampa.dev/tier: {{ .Values.app.tier | quote }}
rampa.dev/ephemeral: {{ .Values.scope.ephemeral | quote }}
app.kubernetes.io/name: {{ include "rampa.name" . | quote }}
app.kubernetes.io/instance: {{ include "rampa.namespace" . | quote }}
app.kubernetes.io/managed-by: rampa
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
{{- end }}

{{- define "rampa.selectorLabels" -}}
app.kubernetes.io/name: {{ include "rampa.name" . | quote }}
app.kubernetes.io/instance: {{ include "rampa.namespace" . | quote }}
{{- end }}

{{- define "rampa.resources" -}}
{{- $size := .Values.size | default "S" -}}
{{- $r := get .Values.sizes $size -}}
{{- if not $r }}{{ fail (printf "size %q no existe. Opciones: %s" $size (keys .Values.sizes | sortAlpha | join ", ")) }}{{ end -}}
{{ toYaml $r }}
{{- end }}

{{/* Pod template compartido por Deployment, Rollout y CronJob */}}
{{- define "rampa.podTemplate" -}}
metadata:
  labels:
    {{- include "rampa.labels" . | nindent 4 }}
  annotations:
    rampa.dev/config-hash: {{ list .Values.env .Values.secrets | toJson | sha256sum | trunc 16 | quote }}
    {{- with .Values.podAnnotations }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
spec:
  serviceAccountName: {{ include "rampa.name" . }}
  terminationGracePeriodSeconds: {{ .Values.terminationGracePeriodSeconds }}
  {{- if eq .Values.app.type "cron" }}
  restartPolicy: OnFailure
  {{- end }}
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: app
      image: {{ include "rampa.image" . | quote }}
      imagePullPolicy: {{ .Values.image.pullPolicy }}
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: [ALL]
      {{- if include "rampa.isWeb" . }}
      ports:
        - name: http
          containerPort: {{ .Values.port }}
          protocol: TCP
      readinessProbe:
        httpGet:
          path: {{ include "rampa.healthcheck" . }}
          port: http
        initialDelaySeconds: 2
        periodSeconds: 5
      livenessProbe:
        httpGet:
          path: {{ include "rampa.healthcheck" . }}
          port: http
        initialDelaySeconds: 10
        periodSeconds: 15
      {{- end }}
      env:
        - name: RAMPA_APP
          value: {{ include "rampa.name" . | quote }}
        - name: RAMPA_SCOPE
          value: {{ .Values.scope.name | quote }}
        - name: RAMPA_TEAM
          value: {{ .Values.app.team | quote }}
        {{- if include "rampa.isWeb" . }}
        - name: PORT
          value: {{ .Values.port | quote }}
        {{- end }}
        {{- range .Values.env }}
        - name: {{ .name | quote }}
          value: {{ .value | quote }}
        {{- end }}
        {{- with .Values.extraEnv }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
      {{- if .Values.secrets }}
      envFrom:
        - secretRef:
            name: {{ include "rampa.name" . }}-params
      {{- end }}
      resources:
        {{- include "rampa.resources" . | nindent 8 }}
      {{- with .Values.extraVolumeMounts }}
      volumeMounts:
        {{- toYaml . | nindent 8 }}
      {{- end }}
  {{- with .Values.extraVolumes }}
  volumes:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end }}
