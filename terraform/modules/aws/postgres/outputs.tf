output "endpoint" {
  value = aws_db_instance.this.address
}

output "secret_name" {
  description = "Ruta en Secrets Manager que consume el chart vía External Secrets"
  value       = aws_secretsmanager_secret.this.name
}

output "secret_arn" {
  value = aws_secretsmanager_secret.this.arn
}

output "security_group_id" {
  value = aws_security_group.this.id
}
