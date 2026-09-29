variable "app" {
  description = "Nombre de la application (rampa.yaml metadata.name)"
  type        = string
}

variable "service" {
  description = "Nombre del service dentro de la app"
  type        = string
}

variable "scope" {
  description = "Scope: dev, staging o prod"
  type        = string
  validation {
    condition     = contains(["dev", "staging", "prod"], var.scope)
    error_message = "Los services solo existen en scopes permanentes: dev, staging, prod."
  }
}

variable "team" {
  description = "Equipo owner, para tags y costos"
  type        = string
}

variable "size" {
  description = "Tamaño remera: S, M, L, XL"
  type        = string
  default     = "S"
  validation {
    condition     = contains(["S", "M", "L", "XL"], var.size)
    error_message = "size debe ser S, M, L o XL."
  }
}

variable "engine_version" {
  description = "Versión mayor de PostgreSQL"
  type        = string
  default     = "16"
}

variable "vpc_id" {
  type = string
}

variable "subnet_ids" {
  description = "Subnets privadas para el subnet group"
  type        = list(string)
}

variable "allowed_security_group_ids" {
  description = "Security groups que pueden conectarse (los nodos de EKS)"
  type        = list(string)
}

variable "tags" {
  description = "Tags adicionales"
  type        = map(string)
  default     = {}
}
