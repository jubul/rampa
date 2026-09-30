output "cluster_name" {
  value = var.name
}

output "agent_installed" {
  description = "El agente se instala desde el cloud-init; el cluster aparece online en Rampa cuando termina (5 a 10 minutos)."
  value       = true
}

output "public_ip" {
  value = digitalocean_reserved_ip.this.ip_address
}

output "droplet_id" {
  value = digitalocean_droplet.this.id
}

output "ssh_hint" {
  value = length(var.admin_cidrs) > 0 ? "ssh root@${digitalocean_reserved_ip.this.ip_address}   # log de instalación: /var/log/rampa-install.log" : "SSH cerrado (admin_cidrs vacío)."
}

output "dns_hint" {
  value = var.dns_zone != "" && var.domain != "" ? "Registro *.${var.domain} creado en la zona ${var.dns_zone}." : "Apuntá un registro A comodín *.${var.domain != "" ? var.domain : "<dominio>"} a ${digitalocean_reserved_ip.this.ip_address}."
}

output "registry" {
  value = var.registry_name != "" ? "registry.digitalocean.com/${var.registry_name}" : ""
}
