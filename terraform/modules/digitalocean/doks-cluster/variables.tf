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
}

variable "region" {
  type = string
}

variable "kubernetes_version" {
  description = "Slug de versión de DOKS (vacío = la última disponible)."
  type        = string
  default     = ""
}

variable "node_size" {
  type    = string
  default = "s-2vcpu-4gb"
}

variable "node_count" {
  type    = number
  default = 2
  validation {
    condition     = var.node_count >= 1 && var.node_count <= 20
    error_message = "Entre 1 y 20 nodos."
  }
}

variable "registry_name" {
  description = "DOCR a crear o usar e integrar al cluster (pull sin credenciales)."
  type        = string
  default     = ""
}

variable "api_url" {
  type = string
}

variable "bootstrap_token" {
  type      = string
  sensitive = true
}

variable "agent_chart_url" {
  type = string
}

variable "agent_image_repository" {
  type    = string
  default = ""
}

variable "agent_image_tag" {
  type    = string
  default = ""
}

variable "tags" {
  type    = list(string)
  default = []
}
