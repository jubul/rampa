# ADR 0004: runtimes, VMs con k3s, más nubes, seguridad del bootstrap e idiomas

Fecha: 2026-09-29. Estado: aceptado. Extiende ADR 0002 y 0003.

## Contexto

Queremos desplegar también en máquinas virtuales (EC2, Droplets, instancias de OCI y OVH), sumar proveedores más allá de AWS, ofrecer serverless en AWS, usar la terminología de cada nube y tener la plataforma en español e inglés. Fury empezó sobre instancias y migró a Kubernetes; Nullplatform tiene "scope providers" pluggables (Kubernetes, Lambda, EC2). La lección de ambos: el modelo del developer no cambia, cambia el motor debajo.

## Decisiones

1. **Un scope tiene un `compute`: `kubernetes` (default) o `serverless`.** (No se llama `runtime` porque ese campo ya es el lenguaje de la app en el `rampa.yaml`.) El resto del modelo (application, scope, deployment, parameters, services) no cambia. `kubernetes` es lo que existe. `serverless` se implementa en la ola dos con AWS Lambda.
2. **Las VMs son clusters de un nodo con k3s.** Un módulo OpenTofu por nube crea la máquina con un cloud-init que instala k3s (con cifrado de secretos en reposo) y el agente de Rampa. Para Rampa esa máquina es un `Cluster` con `kind: vm`; chart, agente, scopes, rollouts y entornos por PR funcionan sin cambios. Un runtime de VM nativo sin Kubernetes queda para cuando un segmento lo pida.
3. **Token de bootstrap de un solo uso para instalar el agente en VMs.** El cloud-init es legible por cualquier proceso de la máquina (metadata del proveedor), así que nunca lleva el token del cluster. Lleva un token de bootstrap que vence en dos horas y se canjea una sola vez contra `POST /v1/agent/bootstrap`; el agente guarda el token real en un Secret de Kubernetes de su namespace. Los módulos de EKS, DOKS y demás usan el mismo mecanismo para no depender de pasar el token a Helm.
4. **Nubes sin federación de identidad guardan un token del cliente, cifrado.** DigitalOcean, OCI y OVH no ofrecen federación como AWS, GCP y Azure. Su token de API se guarda en `cloud_accounts.credentials_ciphertext`, cifrado con AES-256-GCM con la clave del servidor (`RAMPA_STATE_KEY`), con el id de la cuenta como dato autenticado, y nunca se devuelve por la API. Es una excepción documentada al principio de no tener credenciales de larga duración; la instrucción de conexión pide un token con el mínimo alcance y explica cómo rotarlo.
5. **Gestor de secretos `kubernetes` para nubes que no tienen uno.** Un cluster puede declarar `secret_store: kubernetes`. El chart no renderiza ExternalSecret; el agente mantiene el Secret `<app>-params` con lo que la API le manda en un comando `write_secret`. El valor viaja cifrado en tránsito, no se persiste en el plano de control y no aparece en eventos ni logs. En nubes con gestor de secretos, el mismo comando hace que el agente escriba en el gestor de la nube con su identidad de workload.
6. **Terminología por proveedor.** `Provider` expone `Terminology()` con los nombres que usa cada nube (cuenta, proyecto o suscripción; cluster EKS, GKE, AKS, DOKS, OKE; instancia EC2, Droplet, máquina virtual; registry; gestor de secretos), en español e inglés. La web usa esos términos en todos los textos; la API los devuelve en `GET /providers`.
7. **Serverless en AWS con Lambda y el Lambda Web Adapter.** Un scope `compute: serverless` despliega la misma imagen de contenedor que usa Kubernetes como función Lambda con el adapter, que traduce eventos HTTP al `PORT` de la app. Sin cambios en el código de la app. El deploy es un run de OpenTofu (módulo `aws/lambda-scope`) que actualiza la función y hace canary con alias y pesos. Ola dos.
8. **Español e inglés.** La web con `react-i18next`, detección del navegador y preferencia por usuario (`PATCH /v1/me {"locale"}`). La API responde mensajes según `Accept-Language` (`es` default, `en`), con un catálogo por código de error y las validaciones de `pkg/manifest` localizadas por clave. El CLI toma `RAMPA_LANG` o `LANG`. Los documentos se traducen empezando por el README y la guía de inicio.
9. **Orden de proveedores:** DigitalOcean (Droplet con k3s, DOKS, DOCR), OCI (instancias Always Free con k3s, OKE), GCP (GKE Autopilot, Compute Engine), Azure (AKS, VM), OVH. Cada uno es: implementación de `Provider`, módulo de cluster, módulo de VM con k3s, y después services.

## Consecuencias

- Aparecen tres piezas de seguridad nuevas que hay que revisar con el mismo rigor que el agente: el canje de tokens de bootstrap, el cifrado de credenciales de nubes con token, y el tránsito de secretos hacia el agente. Se documentan en `docs/seguridad.md` y entran en la revisión adversarial.
- Las VMs con k3s exponen solo 80 y 443 (y 22 opcional, acotado por CIDR). La API de k3s no se expone: el agente está adentro.
- Una VM es un solo nodo: sin alta disponibilidad. La web lo dice al crearla. Escalar es crear un cluster administrado.
- El registry en DigitalOcean (DOCR) y OCI (OCIR) requiere credenciales para pull; el módulo configura `registries.yaml` de k3s con un token acotado a lectura.
- La i18n de mensajes de la API es un refactor transversal: los errores pasan a tener código estable y parámetros; el texto sale de un catálogo. Los tests que comparaban strings pasan a comparar códigos.
