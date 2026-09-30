# digitalocean/doks-cluster

Cluster DOKS (Kubernetes administrado por DigitalOcean) con un node pool, actualización automática en la ventana de mantenimiento, DOCR integrado si se pide (pull sin credenciales) y el agente de Rampa instalado por Helm en el mismo apply con un bootstrap token de un solo uso. Para Rampa es un cluster de `kind: kubernetes` con `secret_store: kubernetes` (DigitalOcean no tiene gestor de secretos).

Costo aproximado: el plano de control de DOKS es gratis; los nodos `s-2vcpu-4gb` cuestan USD 24 por mes cada uno; el registry `basic` USD 5 por mes. Un load balancer para el ingress se crea al desplegar la primera API (USD 12 por mes).
