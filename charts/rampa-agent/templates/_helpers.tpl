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
Nombre del Secret que contiene el token del cluster: el existente, el que crea
el chart con cluster.token o, con bootstrapToken, el que escribe el agente al
canjearlo (nombre fijo rampa-agent-token, ver internal/agent/bootstrap).
*/}}
{{- define "rampa-agent.tokenSecretName" -}}
{{- if .Values.cluster.existingSecret }}
{{- .Values.cluster.existingSecret }}
{{- else if .Values.cluster.bootstrapToken }}
{{- include "rampa-agent.agentTokenSecretName" . }}
{{- else }}
{{- printf "%s-token" (include "rampa-agent.fullname" .) }}
{{- end }}
{{- end }}

{{/*
Nombre fijo del Secret donde el agente guarda el token que obtiene por bootstrap.
Tiene que coincidir con bootstrap.SecretName del agente y con el resourceNames del Role.
*/}}
{{- define "rampa-agent.agentTokenSecretName" -}}
rampa-agent-token
{{- end }}

{{/*
Nombre del Secret con el bootstrap token que crea el chart.
*/}}
{{- define "rampa-agent.bootstrapSecretName" -}}
{{- printf "%s-bootstrap" (include "rampa-agent.fullname" .) }}
{{- end }}

{{/*
Validaciones con mensaje claro. El schema JSON también las exige, pero si alguien
instala con --skip-schema-validation el error tiene que seguir siendo legible.
*/}}
{{- define "rampa-agent.validate" -}}
{{- $_ := required "api.url es requerido: la URL del plano de control de Rampa, por ejemplo https://api.rampa.dev" .Values.api.url }}
{{- $sources := 0 }}
{{- if .Values.cluster.token }}{{ $sources = add1 $sources }}{{ end }}
{{- if .Values.cluster.existingSecret }}{{ $sources = add1 $sources }}{{ end }}
{{- if .Values.cluster.bootstrapToken }}{{ $sources = add1 $sources }}{{ end }}
{{- if eq (int $sources) 0 }}
{{- fail "hace falta exactamente uno de cluster.token (el token que entrega la web al crear el cluster), cluster.existingSecret (un Secret con la clave token) o cluster.bootstrapToken (token de bootstrap de un solo uso)" }}
{{- else if gt (int $sources) 1 }}
{{- fail "cluster.token, cluster.existingSecret y cluster.bootstrapToken son mutuamente excluyentes: dejá solo uno" }}
{{- end }}
{{- if ne (int .Values.replicaCount) 1 }}
{{- fail "replicaCount tiene que ser 1: hay exactamente un agente por cluster" }}
{{- end }}
{{- end }}
