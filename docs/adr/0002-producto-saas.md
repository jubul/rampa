# ADR 0002: Rampa como producto SaaS multi-cloud

Fecha: 2026-09-29. Estado: aceptado. Extiende ADR 0001.

## Contexto

Se decidió convertir Rampa en un producto comercial estilo Nullplatform: una web para gestionar y desplegar, con soporte para AWS, GCP y Azure. Nuestro grupo de developers es el cliente cero.

## Decisiones

1. **Plano de control SaaS + agente en la nube del cliente.** El estado deseado vive en nuestra base. Un agente con conexión saliente lo aplica en el cluster del cliente. Argo CD y el repo `rampa-deploys` dejan de ser requisito.
2. **La API renderiza el chart `rampa-app` (embebido en la API) y el agente aplica los objetos con server-side apply.** El agente no conoce el chart ni el rampa.yaml: recibe JSON de Kubernetes y lo aplica. Así el chart se actualiza centralmente sin redeployar agentes y el agente es un aplicador tonto, más chico y más auditable. No hay Helm releases. Undeploy es borrar el namespace, porque scope = namespace (ADR 0001).
3. **Monorepo con un solo módulo Go (`rampa.dev`).** `cmd/rampa`, `cmd/rampa-api`, `cmd/rampa-agent`; compartidos en `pkg/`; privados en `internal/`.
4. **Postgres** como única base del plano de control. Migraciones SQL en el repo. Cola de comandos al agente en una tabla, no en un broker.
5. **API REST JSON con SSE para eventos.** WebSocket solo entre agente y API.
6. **Web en React + TypeScript + Vite**, SPA servida por la API o por un CDN, que habla con la API.
7. **AWS primero, interfaz `Provider` para los tres.** GCP y Azure se implementan detrás de la misma interfaz cuando haya cuentas para probar.
8. **Los secretos de aplicación y las credenciales de nube nunca pasan por el plano de control.** Conexión de cuenta por rol asumible con external ID (AWS) o federación OIDC (GCP, Azure). Secretos escritos por el agente en el secret manager del cliente.
9. **Multi-tenant desde el primer commit.** `org_id` en cada tabla y cada query.
10. **El build de imágenes sigue en el CI del cliente** durante la Fase A. La GitHub App llega en la Fase B.

## Consecuencias

- El agente es el componente más sensible del producto. Se publica su código, se firma el binario y se documenta cada permiso del ClusterRole.
- Sin Helm releases, el agente tiene que hacer prune propio: etiqueta todo lo que aplica con `rampa.dev/deployment` y borra lo que no está en el último render.
- El CLI pierde `--deploys-repo` y gana `rampa login`. `render` y `validate` siguen funcionando offline.
- Los workflows reusables `deploy` y `undeploy` pasan a llamar a la API. `build` no cambia.
- Operar Postgres y la API es operar producción para terceros. Se dogfoodea con Rampa.
