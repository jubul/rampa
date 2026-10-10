variable "name" {
  type = string
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

variable "instance_type" {
  description = "t3.small (2 GB) es el mínimo razonable para k3s. t4g.* son ARM (Graviton), más baratas."
  type        = string
  default     = "t3.small"
  nullable    = false
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

variable "key_name" {
  description = "Nombre de un key pair de EC2 para SSH. Vacío: sin clave."
  type        = string
  default     = ""
  nullable    = false
}

variable "admin_cidrs" {
  description = "CIDRs con acceso SSH (22). Vacío: cerrado."
  type        = list(string)
  default     = []
  nullable    = false
}

variable "domain" {
  type     = string
  default  = ""
  nullable = false
}

variable "route53_zone_id" {
  description = "Zona de Route 53 donde crear el registro comodín de domain. Vacío: no se crea."
  type        = string
  default     = ""
  nullable    = false
}

variable "tls_email" {
  type     = string
  default  = ""
  nullable = false
}

variable "volume_size_gb" {
  type     = number
  default  = 30
  nullable = false
}

variable "api_url" {
  type = string
}

variable "bootstrap_token" {
  type      = string
  default   = ""
  sensitive = true
  nullable  = false
}

variable "agent_chart_url" {
  type = string
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
  type     = map(string)
  default  = {}
  nullable = false
}

variable "eso_chart_version" {
  description = "Versión del chart de External Secrets Operator."
  type        = string
  default     = "2.11.0"
  nullable    = false
}

variable "enable_ssm" {
  description = "Permitir SSM Session Manager (shell sin puerto 22 para quien tenga ssm:StartSession en la cuenta)."
  type        = bool
  default     = true
  nullable    = false
}
