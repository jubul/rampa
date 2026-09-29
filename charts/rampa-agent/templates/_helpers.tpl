{{/*
Nombre base del chart.
*/}}
{{- define "rampa-agent.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Nombre completo de los recursos. Si el release ya se llama como el chart, no se duplica.
*/}}
{{- define "rampa-agent.fullname" -}}
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
Tag de la imagen: image.tag o, si está vacío, appVersion del chart.
*/}}
{{- define "rampa-agent.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag }}
{{- printf "%s:%s" .Values.image.repository $tag }}
{{- end }}

{{/*
Labels estándar de todos los recursos.
*/}}
{{- define "rampa-agent.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | quote }}
app.kubernetes.io/name: {{ include "rampa-agent.name" . | quote }}
app.kubernetes.io/instance: {{ .Release.Name | quote }}
app.kubernetes.io/version: {{ default .Chart.AppVersion .Values.image.tag | quote }}
app.kubernetes.io/component: agent
app.kubernetes.io/part-of: rampa
app.kubernetes.io/managed-by: {{ .Release.Service | quote }}
rampa.dev/component: agent
{{- end }}

{{/*
Labels del selector. No cambian entre versiones del chart (el selector de un Deployment es inmutable).
*/}}
{{- define "rampa-agent.selectorLabels" -}}
app.kubernetes.io/name: {{ include "rampa-agent.name" . | quote }}
app.kubernetes.io/instance: {{ .Release.Name | quote }}
{{- end }}

{{/*
Nombre del ServiceAccount que usa el agente.
*/}}
{{- define "rampa-agent.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default "rampa-agent" .Values.serviceAccount.name }}
{{- else }}
{{- required "serviceAccount.name es requerido cuando serviceAccount.create es false" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Nombre del Secret que contiene el token del cluster: el existente o el que crea el chart.
*/}}
{{- define "rampa-agent.tokenSecretName" -}}
{{- if .Values.cluster.existingSecret }}
{{- .Values.cluster.existingSecret }}
{{- else }}
{{- printf "%s-token" (include "rampa-agent.fullname" .) }}
{{- end }}
{{- end }}

{{/*
Validaciones con mensaje claro. El schema JSON también las exige, pero si alguien
instala con --skip-schema-validation el error tiene que seguir siendo legible.
*/}}
{{- define "rampa-agent.validate" -}}
{{- $_ := required "api.url es requerido: la URL del plano de control de Rampa, por ejemplo https://api.rampa.dev" .Values.api.url }}
{{- if and (not .Values.cluster.token) (not .Values.cluster.existingSecret) }}
{{- fail "hace falta cluster.token (el token que entrega la web al crear el cluster) o cluster.existingSecret (un Secret con la clave token)" }}
{{- end }}
{{- if ne (int .Values.replicaCount) 1 }}
{{- fail "replicaCount tiene que ser 1: hay exactamente un agente por cluster" }}
{{- end }}
{{- end }}
