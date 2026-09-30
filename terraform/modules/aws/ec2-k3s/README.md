# aws/ec2-k3s

Una instancia EC2 con k3s (un nodo) y el agente de Rampa, en la VPC default de la cuenta, sin NAT gateway. Para Rampa es un cluster de `kind: vm` con `secret_store: aws-secrets-manager`: el cloud-init instala External Secrets Operator y el agente usa la identidad del instance profile, que puede leer y escribir secretos bajo `rampa/*` y hacer pull de ECR.

Seguridad: bootstrap token de un solo uso en el cloud-init; IMDSv2 obligatorio con **un solo salto**, así los pods no pueden pedir credenciales del instance profile; además `rampa-imds-guard` bloquea 169.254.169.254 desde la red de pods con iptables. El agente y External Secrets corren con la red del host (`hostNetwork`) para usar la identidad de la máquina. Disco cifrado; 80 y 443 abiertos, 22 solo desde `admin_cidrs`; SSM Session Manager opcional (`enable_ssm`, por defecto activo: es acceso de shell para quien tenga `ssm:StartSession` en la cuenta); SSH por contraseña deshabilitado; actualizaciones de seguridad automáticas. La API de k3s no se expone. Todo lo que se instala tiene versión fija y verificada: k3s, Helm con sha256, cert-manager y External Secrets.

| Variable | Default | Qué es |
|---|---|---|
| `k3s_version` | `v1.36.4+k3s1` | Versión fija de k3s |
| `helm_version`, `helm_sha256` | `3.19.0` y sus sha256 por arquitectura | Helm para el instalador |
| `cert_manager_version` | `v1.21.2` | Chart de cert-manager, si hay `tls_email` |
| `eso_chart_version` | `2.11.0` | Chart de External Secrets Operator |
| `pod_cidr` | `10.42.0.0/16` | Red de pods; desde ahí se bloquea el metadata |
| `enable_ssm` | `true` | Adjuntar `AmazonSSMManagedInstanceCore` al rol del nodo |

Costo aproximado: `t3.small` unos USD 15 por mes, `t4g.small` (ARM) unos USD 12, más el disco gp3 (USD 2,40 por 30 GB). La Elastic IP asociada a una instancia encendida cuesta USD 3,60 por mes desde 2024. Sin NAT gateway ni load balancer.
