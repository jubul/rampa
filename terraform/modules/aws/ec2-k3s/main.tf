# Instancia EC2 de un nodo con k3s y el agente de Rampa, en la VPC default de la
# cuenta (sin NAT gateway: costo cero de red). Identidad del agente por instance
# profile: External Secrets lee Secrets Manager y el nodo puede hacer pull de ECR.

data "aws_partition" "current" {}
data "aws_region" "current" {}
data "aws_caller_identity" "current" {}

data "aws_vpc" "default" {
  default = true
}

data "aws_subnets" "default" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.default.id]
  }
  filter {
    name   = "default-for-az"
    values = ["true"]
  }
}

locals {
  arch = can(regex("^(t4g|m7g|c7g|r7g|m6g|c6g)", var.instance_type)) ? "arm64" : "amd64"
  tags = merge(var.tags, {
    "rampa:cluster" = var.name
    "rampa:env"     = var.env
  })
  secret_store_values = <<-YAML
    secretStore:
      enabled: true
      name: aws-secrets-manager
      provider: aws
      aws:
        region: ${data.aws_region.current.region}
  YAML
}

data "aws_ssm_parameter" "ubuntu_ami" {
  name = "/aws/service/canonical/ubuntu/server/24.04/stable/current/${local.arch}/hvm/ebs-gp3/ami-id"
}

resource "aws_security_group" "this" {
  name        = "${var.name}-rampa"
  description = "Rampa k3s ${var.name}: 80 y 443 publicos, 22 acotado"
  vpc_id      = data.aws_vpc.default.id
  tags        = merge(local.tags, { Name = "${var.name}-rampa" })
}

resource "aws_vpc_security_group_ingress_rule" "http" {
  security_group_id = aws_security_group.this.id
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 80
  to_port           = 80
  ip_protocol       = "tcp"
}

resource "aws_vpc_security_group_ingress_rule" "https" {
  security_group_id = aws_security_group.this.id
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 443
  to_port           = 443
  ip_protocol       = "tcp"
}

resource "aws_vpc_security_group_ingress_rule" "ssh" {
  for_each          = toset(var.admin_cidrs)
  security_group_id = aws_security_group.this.id
  cidr_ipv4         = each.value
  from_port         = 22
  to_port           = 22
  ip_protocol       = "tcp"
}

resource "aws_vpc_security_group_egress_rule" "all" {
  security_group_id = aws_security_group.this.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}

# Identidad del nodo (y del agente, via instance profile).
data "aws_iam_policy_document" "assume" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

data "aws_iam_policy_document" "node" {
  statement {
    sid    = "SecretsRampa"
    effect = "Allow"
    actions = [
      "secretsmanager:GetSecretValue",
      "secretsmanager:DescribeSecret",
      "secretsmanager:CreateSecret",
      "secretsmanager:PutSecretValue",
      "secretsmanager:TagResource",
    ]
    resources = ["arn:${data.aws_partition.current.partition}:secretsmanager:*:${data.aws_caller_identity.current.account_id}:secret:rampa/*"]
  }
  statement {
    sid       = "SecretsList"
    effect    = "Allow"
    actions   = ["secretsmanager:ListSecrets"]
    resources = ["*"]
  }
  statement {
    sid    = "EcrPull"
    effect = "Allow"
    actions = [
      "ecr:GetAuthorizationToken",
      "ecr:BatchGetImage",
      "ecr:GetDownloadUrlForLayer",
      "ecr:BatchCheckLayerAvailability",
      "ecr:DescribeRepositories",
      "ecr:DescribeImages",
    ]
    resources = ["*"]
  }
}

resource "aws_iam_role" "node" {
  name               = "${var.name}-rampa-agent"
  assume_role_policy = data.aws_iam_policy_document.assume.json
  tags               = local.tags
}

resource "aws_iam_role_policy" "node" {
  name   = "rampa-k3s-node"
  role   = aws_iam_role.node.id
  policy = data.aws_iam_policy_document.node.json
}

# Acceso por SSM Session Manager (shell sin puerto 22). Opcional: es una via de
# entrada para quien tenga ssm:StartSession en la cuenta.
resource "aws_iam_role_policy_attachment" "ssm" {
  count      = var.enable_ssm ? 1 : 0
  role       = aws_iam_role.node.name
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "node" {
  name = "${var.name}-rampa-agent"
  role = aws_iam_role.node.name
  tags = local.tags
}

resource "aws_eip" "this" {
  domain = "vpc"
  tags   = merge(local.tags, { Name = "${var.name}-rampa" })
}

resource "aws_instance" "this" {
  ami                         = data.aws_ssm_parameter.ubuntu_ami.value
  instance_type               = var.instance_type
  subnet_id                   = data.aws_subnets.default.ids[0]
  vpc_security_group_ids      = [aws_security_group.this.id]
  iam_instance_profile        = aws_iam_instance_profile.node.name
  key_name                    = var.key_name != "" ? var.key_name : null
  associate_public_ip_address = true

  # IMDSv2 con un solo salto: los pods (detras del bridge de k3s) no pueden pedir
  # el token del metadata. El agente y External Secrets corren con la red del host.
  metadata_options {
    http_tokens                 = "required"
    http_put_response_hop_limit = 1
  }

  root_block_device {
    volume_type = "gp3"
    volume_size = var.volume_size_gb
    encrypted   = true
  }

  user_data_replace_on_change = true
  user_data = templatefile("${path.module}/cloud-init.yaml.tftpl", {
    public_ip              = aws_eip.this.public_ip
    k3s_version            = var.k3s_version
    helm_version           = var.helm_version
    api_url                = var.api_url
    bootstrap_token        = var.bootstrap_token
    agent_chart_url        = var.agent_chart_url
    agent_image_repository = var.agent_image_repository
    agent_image_tag        = var.agent_image_tag
    tls_email              = var.tls_email
    install_eso            = true
    secret_store_values    = local.secret_store_values
    registries_yaml        = ""
    agent_host_network     = true
    pod_cidr               = var.pod_cidr
    k3s_version_url        = replace(var.k3s_version, "+", "%2B")
    helm_sha256_amd64      = var.helm_sha256["amd64"]
    helm_sha256_arm64      = var.helm_sha256["arm64"]
    cert_manager_version   = var.cert_manager_version
    eso_chart_version      = var.eso_chart_version
  })

  tags = merge(local.tags, { Name = var.name })
}

resource "aws_eip_association" "this" {
  instance_id   = aws_instance.this.id
  allocation_id = aws_eip.this.id
}

resource "aws_route53_record" "wildcard" {
  count   = var.route53_zone_id != "" && var.domain != "" ? 1 : 0
  zone_id = var.route53_zone_id
  name    = "*.${var.domain}"
  type    = "A"
  ttl     = 300
  records = [aws_eip.this.public_ip]
}
