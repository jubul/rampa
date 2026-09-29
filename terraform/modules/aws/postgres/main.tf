locals {
  name    = "rampa-${var.app}-${var.service}-${var.scope}"
  is_prod = var.scope == "prod"
  db_name = replace(var.service, "-", "_")

  sizes = {
    S  = { class = "db.t4g.micro", storage = 20 }
    M  = { class = "db.t4g.medium", storage = 50 }
    L  = { class = "db.r6g.large", storage = 100 }
    XL = { class = "db.r6g.xlarge", storage = 200 }
  }
  size = local.sizes[var.size]

  tags = merge(var.tags, {
    "rampa:app"     = var.app
    "rampa:service" = var.service
    "rampa:scope"   = var.scope
    "rampa:team"    = var.team
    "rampa:type"    = "postgres"
  })
}

resource "random_password" "master" {
  length  = 32
  special = false
}

resource "aws_db_subnet_group" "this" {
  name       = local.name
  subnet_ids = var.subnet_ids
  tags       = local.tags
}

resource "aws_security_group" "this" {
  name        = local.name
  description = "Acceso a ${local.name} desde los nodos del cluster"
  vpc_id      = var.vpc_id
  tags        = local.tags
}

resource "aws_vpc_security_group_ingress_rule" "from_cluster" {
  for_each                     = toset(var.allowed_security_group_ids)
  security_group_id            = aws_security_group.this.id
  referenced_security_group_id = each.value
  from_port                    = 5432
  to_port                      = 5432
  ip_protocol                  = "tcp"
}

resource "aws_db_instance" "this" {
  identifier     = local.name
  engine         = "postgres"
  engine_version = var.engine_version
  instance_class = local.size.class

  allocated_storage     = local.size.storage
  max_allocated_storage = local.size.storage * 4
  storage_type          = "gp3"
  storage_encrypted     = true

  db_name  = local.db_name
  username = "app"
  password = random_password.master.result
  port     = 5432

  db_subnet_group_name   = aws_db_subnet_group.this.name
  vpc_security_group_ids = [aws_security_group.this.id]
  publicly_accessible    = false

  multi_az                     = local.is_prod
  backup_retention_period      = local.is_prod ? 14 : 3
  deletion_protection          = local.is_prod
  skip_final_snapshot          = !local.is_prod
  final_snapshot_identifier    = local.is_prod ? "${local.name}-final" : null
  apply_immediately            = !local.is_prod
  auto_minor_version_upgrade   = true
  performance_insights_enabled = local.is_prod
  copy_tags_to_snapshot        = true

  tags = local.tags
}

# El contrato con el chart: JSON en rampa/services/<app>/<service>/<scope>.
resource "aws_secretsmanager_secret" "this" {
  name                    = "rampa/services/${var.app}/${var.service}/${var.scope}"
  description             = "Credenciales de ${local.name}, gestionadas por Rampa"
  recovery_window_in_days = local.is_prod ? 30 : 0
  tags                    = local.tags
}

resource "aws_secretsmanager_secret_version" "this" {
  secret_id = aws_secretsmanager_secret.this.id
  secret_string = jsonencode({
    url      = "postgres://app:${random_password.master.result}@${aws_db_instance.this.address}:5432/${local.db_name}?sslmode=require"
    host     = aws_db_instance.this.address
    port     = "5432"
    dbname   = local.db_name
    user     = "app"
    password = random_password.master.result
  })
}
