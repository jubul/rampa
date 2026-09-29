output "cluster_name" {
  description = "Nombre del cluster. Es el que queda registrado en Rampa."
  value       = aws_eks_cluster.this.name
}

output "cluster_arn" {
  value = aws_eks_cluster.this.arn
}

output "cluster_endpoint" {
  value = aws_eks_cluster.this.endpoint
}

output "kubernetes_version" {
  value = aws_eks_cluster.this.version
}

output "oidc_issuer" {
  description = "Issuer OIDC del cluster, para IRSA."
  value       = aws_eks_cluster.this.identity[0].oidc[0].issuer
}

output "agent_role_arn" {
  description = "Rol IAM del agente. Rampa lo guarda en el cluster."
  value       = aws_iam_role.agent.arn
}

output "agent_installed" {
  value = var.install_agent
}

output "vpc_id" {
  value = aws_vpc.this.id
}

output "private_subnet_ids" {
  description = "Subredes privadas, para services (RDS, ElastiCache) de Rampa."
  value       = aws_subnet.private[*].id
}

output "public_subnet_ids" {
  value = aws_subnet.public[*].id
}

output "kubeconfig_command" {
  description = "Cómo conectarse desde tu máquina."
  value       = "aws eks update-kubeconfig --region ${data.aws_region.current.region} --name ${aws_eks_cluster.this.name}"
}
