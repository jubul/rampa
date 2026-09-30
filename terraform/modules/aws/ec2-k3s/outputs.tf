output "cluster_name" {
  value = var.name
}

output "agent_installed" {
  value = true
}

output "instance_id" {
  value = aws_instance.this.id
}

output "public_ip" {
  value = aws_eip.this.public_ip
}

output "agent_role_arn" {
  description = "Rol del nodo (instance profile). Es la identidad del agente y de External Secrets."
  value       = aws_iam_role.node.arn
}

output "ssh_hint" {
  value = length(var.admin_cidrs) > 0 && var.key_name != "" ? "ssh ubuntu@${aws_eip.this.public_ip}   # log: /var/log/rampa-install.log" : var.enable_ssm ? "SSH cerrado. Alternativa sin puerto 22: aws ssm start-session --target ${aws_instance.this.id}" : "SSH cerrado y SSM deshabilitado: sin acceso interactivo a la maquina."
}

output "dns_hint" {
  value = var.route53_zone_id != "" && var.domain != "" ? "Registro *.${var.domain} creado en Route 53." : "Apuntá un registro A comodín *.${var.domain != "" ? var.domain : "<dominio>"} a ${aws_eip.this.public_ip}."
}
