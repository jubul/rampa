# ADR 0001: decisiones fundacionales

Fecha: 2026-09-29. Estado: aceptado.

## Contexto

Grupo de 10 a 30 developers en AWS con EKS. Se decidió construir la plataforma en vez de comprarla (ver `docs/plan.md`, opción B), con la intención de que sea propia y reconocible.

## Decisiones

1. **Nombre: Rampa.** CLI `rampa`, labels `rampa.dev/*`, chart `rampa-app`.
2. **Go para el CLI y el código de plataforma.** Binario único, ecosistema de Kubernetes, Argo, Helm y Terraform.
3. **GitHub y GitHub Actions.** Workflows reusables en este repo; las apps los llaman con dos líneas.
4. **GitOps con Argo CD y un repo `rampa-deploys`.** El estado deseado es git. Rollback es revert. Sin `kubectl apply` desde CI.
5. **Un Helm chart único (`rampa-app`).** Las apps no tienen charts propios. Aportan values generados desde `rampa.yaml`. Si el chart no cubre un caso, se extiende el chart, no se bifurca.
6. **Scope = namespace.** Cada scope es un namespace `<app>-<scope>`. Cuotas, RBAC, NetworkPolicy y costos por namespace salen gratis.
7. **Terraform/OpenTofu para services.** Módulos por tipo. Contrato: credenciales en Secrets Manager en ruta convencional. El motor de ejecución (CI o tf-controller) se decide en la Fase 3.
8. **External Secrets Operator + AWS Secrets Manager.** Ningún secreto en git ni en values.
9. **`rampa.yaml` es la fuente de verdad de la app.** El catálogo se deriva de él. No hay un segundo lugar donde declarar owner, tier o dependencias.

## Consecuencias

- El equipo de plataforma es dueño del chart y de los workflows. Los cambios ahí afectan a todas las apps: se versionan y se prueban con las apps piloto antes de promover.
- Para casos que el chart no cubre se documenta la salida de emergencia (un `kustomization` extra en el directorio del scope), pero se trata como deuda a absorber en el chart.
- Los scopes efímeros no reciben services gestionados en la Fase 2. Se revisa en la Fase 3.
