variable "name" {
  description = "Nombre del Droplet y del cluster en Rampa."
  type        = string
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{0,39}$", var.name))
    error_message = "Minúsculas, números y guiones, empieza con letra, máximo 40."
  }
}

variable "env" {
  type    = string
  default = "nonprod"
  validation {
    condition     = contains(["nonprod", "prod"], var.env)
    error_message = "env debe ser nonprod o prod."
  }
  nullable = false
}

variable "region" {
  description = "Región de DigitalOcean, por ejemplo nyc3 o sfo3."
  type        = string
}

variable "size" {
  description = "Tamaño del Droplet. k3s necesita al menos 2 GB."
  type        = string
  default     = "s-2vcpu-2gb"
  nullable    = false
}

variable "image" {
  type     = string
  default  = "ubuntu-24-04-x64"
  nullable = false
}

variable "k3s_version" {
  description = "Versión de k3s, fija. El instalador se baja del tag correspondiente y verifica el sha256 del binario."
  type        = string
  default     = "v1.36.4+k3s1"
  validation {
    condition     = can(regex("^v[0-9]+\\.[0-9]+\\.[0-9]+\\+k3s[0-9]+$", var.k3s_version))
    error_message = "Formato vX.Y.Z+k3sN, por ejemplo v1.36.4+k3s1."
  }
  nullable = false
}

variable "ssh_key_fingerprints" {
  description = "Fingerprints de claves SSH ya cargadas en DigitalOcean. Vacío: sin SSH."
  type        = list(string)
  default     = []
  nullable    = false
}

variable "admin_cidrs" {
  description = "CIDRs con acceso SSH (22). Vacío: el puerto queda cerrado."
  type        = list(string)
  default     = []
  nullable    = false
}

variable "domain" {
  description = "Dominio de los scopes, por ejemplo apps.dev.acme.com. Si dns_zone se administra en DigitalOcean, se crea el registro comodín."
  type        = string
  default     = ""
  nullable    = false
}

variable "dns_zone" {
  description = "Zona DNS administrada en DigitalOcean que contiene a domain (por ejemplo acme.com)."
  type        = string
  default     = ""
  nullable    = false
}

variable "tls_email" {
  description = "Email para Let's Encrypt. Si se setea se instala cert-manager con un ClusterIssuer letsencrypt."
  type        = string
  default     = ""
  nullable    = false
}

variable "registry_name" {
  description = "Nombre del DigitalOcean Container Registry (DOCR). Si se setea, se crea o usa y k3s queda configurado para pull."
  type        = string
  default     = ""
  nullable    = false
}

variable "api_url" {
  type = string
}

variable "bootstrap_token" {
  description = "Bootstrap token de un solo uso generado por Rampa."
  type        = string
  default     = ""
  sensitive   = true
  nullable    = false
}

variable "agent_chart_url" {
  description = "URL del chart rampa-agent empaquetado (la API lo sirve en /public/charts/rampa-agent.tgz)."
  type        = string
}

variable "agent_image_repository" {
  type     = string
  default  = ""
  nullable = false
}

variable "agent_image_tag" {
  type     = string
  default  = ""
  nullable = false
}

variable "helm_version" {
  description = "Versión de Helm 3 para el instalador (se verifica con sha256)."
  type        = string
  default     = "3.19.0"
  nullable    = false
}

variable "helm_sha256" {
  description = "sha256 del tarball de Helm por arquitectura, para helm_version."
  type        = map(string)
  default = {
    amd64 = "a7f81ce08007091b86d8bd696eb4d86b8d0f2e1b9f6c714be62f82f96a594496"
    arm64 = "440cf7add0aee27ebc93fada965523c1dc2e0ab340d4348da2215737fc0d76ad"
  }
  nullable = false
}

variable "cert_manager_version" {
  description = "Versión del chart de cert-manager, si se instala (tls_email)."
  type        = string
  default     = "v1.21.2"
  nullable    = false
}

variable "pod_cidr" {
  description = "CIDR de los pods de k3s. Desde este rango se bloquea el metadata del proveedor."
  type        = string
  default     = "10.42.0.0/16"
  nullable    = false
}

variable "tags" {
  type     = list(string)
  default  = []
  nullable = false
}
