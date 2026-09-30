# digitalocean/droplet-k3s

Un Droplet con k3s (un nodo, secretos cifrados en reposo) y el agente de Rampa instalado desde el cloud-init. Para Rampa es un cluster de `kind: vm` con `secret_store: kubernetes`. Sin alta disponibilidad: si necesitás más de un nodo, usá `digitalocean/doks-cluster`.

Crea: IP reservada, Droplet (Ubuntu 24.04), firewall (80 y 443 abiertos; 22 solo desde `admin_cidrs`), opcionalmente un DigitalOcean Container Registry configurado para pull en k3s, un registro DNS comodín si la zona vive en DigitalOcean, y cert-manager con Let's Encrypt si se pasa `tls_email`.

Seguridad: el cloud-init lleva un **bootstrap token** de un solo uso que vence en 2 horas, nunca el token del cluster; el agente lo canjea y guarda el real en un Secret. El firewall existe antes que el Droplet (se asocia por tag). Los pods no llegan al metadata de DigitalOcean (169.254.169.254): lo bloquea `rampa-imds-guard` con iptables, así ningún contenedor lee el user-data. La credencial de pull del registry es de solo lectura y vence al año (se rota con un nuevo apply). SSH por contraseña deshabilitado. La API de k3s (6443) no se expone. Todo lo que se instala tiene versión fija y verificada: k3s (`k3s_version`, el instalador se baja del tag y verifica el binario), Helm (`helm_version` con `helm_sha256`), cert-manager (`cert_manager_version`). Actualizaciones automáticas de seguridad del sistema operativo.

| Variable | Default | Qué es |
|---|---|---|
| `k3s_version` | `v1.36.4+k3s1` | Versión fija de k3s |
| `helm_version`, `helm_sha256` | `3.19.0` y sus sha256 por arquitectura | Helm para el instalador |
| `cert_manager_version` | `v1.21.2` | Chart de cert-manager, si hay `tls_email` |
| `pod_cidr` | `10.42.0.0/16` | Red de pods de k3s; desde ahí se bloquea el metadata |

Costo aproximado: el Droplet (USD 12 por mes el `s-2vcpu-2gb`), la IP reservada asignada es gratis, el registry `basic` USD 5 por mes si se crea.
