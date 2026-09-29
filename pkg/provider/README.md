# pkg/provider

Lo que difiere entre nubes, detrás de una interfaz. El resto de Rampa (API, agente, render) no sabe en qué nube está: pide un `Provider` por `Kind` y le pregunta cómo conectar una cuenta, dónde está el registry, qué `ClusterSecretStore` instalar, con qué anotación dar identidad a los pods y qué módulo OpenTofu provisiona un service.

Los subpaquetes se registran solos en su `init`; la API los importa (`_ "rampa.dev/pkg/provider/aws"`, etc.) y los busca con `provider.Get` o los lista con `provider.All`.

| Provider | `Available` | Conexión | Registry | Secretos | Identidad de pods |
|---|---|---|---|---|---|
| `aws` | sí | rol IAM con external ID (CloudFormation quick-create) | ECR `<cuenta>.dkr.ecr.<región>.amazonaws.com` | Secrets Manager vía ESO con IRSA | `eks.amazonaws.com/role-arn` |
| `gcp` | no | Workload Identity Federation hacia nuestro issuer | Artifact Registry `<región>-docker.pkg.dev/<proyecto>` | Secret Manager vía ESO (`gcpsm`) | `iam.gke.io/gcp-service-account` |
| `azure` | no | App registration con credencial federada | ACR `<acr_name>.azurecr.io` | Key Vault vía ESO (`azurekv`) | `azure.workload.identity/client-id` |

## AWS (`pkg/provider/aws`)

Configuración por entorno:

| Variable | Qué es |
|---|---|
| `RAMPA_AWS_TRUSTED_PRINCIPAL_ARN` | Identidad IAM con la que corre la API (rol de la task, del pod vía IRSA, o usuario). Es el principal que la trust policy del cliente autoriza a asumir el rol. Si falta, el provider se registra igual pero `Verify` devuelve un error diciendo qué configurar. |
| `RAMPA_AWS_DEFAULT_REGION` | Región para el link de CloudFormation y fallback del cliente STS. Default `us-east-1`. |

Además usa la cadena de credenciales estándar del SDK (variables `AWS_*`, perfil, IRSA, IMDS) para hablar con STS como plano de control.

Qué hace cada método:

- `ConnectInstructions` arma el link de quick-create de CloudFormation con `templateURL` apuntando a `<callbackURL>/public/cloudformation/rampa-connect.yaml`, los parámetros `ExternalId` y `TrustedPrincipalArn` ya cargados, y el comando `aws cloudformation create-stack` equivalente. Pide un solo campo: `external_ref`, el ARN del rol.
- `CloudFormationTemplate` (`var []byte`, embebida desde `cloudformation/rampa-connect.yaml`) crea el rol `RampaConnect-<stack>` con trust policy `sts:AssumeRole` desde el principal de Rampa condicionada a `sts:ExternalId`, y una política de mínimo privilegio: ECR sobre `repository/rampa/*` (más `GetAuthorizationToken` y `DescribeRegistry`, que no admiten recurso), EKS solo lectura, Secrets Manager sobre `secret:rampa/*`, `sts:GetCallerIdentity`. Output `RoleArn`. La API la sirve pública.
- `Verify` asume `acc.ExternalRef` con `acc.Settings["external_id"]` usando `stscreds.NewAssumeRoleProvider` y llama `GetCallerIdentity` con esas credenciales. Timeout de 15 segundos. Devuelve `error` solo cuando no pudimos ni intentarlo (falta nuestro principal, ARN inválido, sin external ID); si AWS rechaza el `AssumeRole`, devuelve `Verification{OK: false, Message}` sin error, con una pista de qué revisar. Nunca loguea credenciales.
- `Registry` deriva la cuenta del ARN del rol y arma el host de ECR con `acc.Region`. Falla con mensaje claro si el ARN no es de un rol o no hay región.
- `SecretStoreSpec` arma un `ClusterSecretStore` `external-secrets.io/v1` con `provider.aws`, `service: SecretsManager`, la región del cluster (o de la cuenta) y auth `jwt` con la ServiceAccount `rampa-agent` del namespace `rampa-system` (IRSA del agente). Nombre `aws-secrets-manager`.
- `WorkloadIdentityAnnotations` devuelve `eks.amazonaws.com/role-arn: <identity>`.
- `ServiceModule` apunta a `terraform/modules/aws/<kind>`; solo `postgres` existe hoy, el resto devuelve `ok=false`.

Para tests, `aws.New(aws.Config{STSClient, NewAssumedClient})` acepta una interfaz mínima de STS (`AssumeRole` y `GetCallerIdentity`) que se mockea sin red.

### Flujo de conexión de AWS, visto por el cliente

1. En la web, en "Nubes", elegís AWS, ponés un nombre (`acme-prod`) y una región. La API crea la cuenta en `pending`, genera un `external_id` único y te muestra las instrucciones.
2. Hacés clic en "Abrir CloudFormation". Se abre tu consola de AWS (ya logueado, en la región elegida) con la pantalla de quick-create: plantilla cargada desde Rampa, stack `RampaConnect`, parámetros `ExternalId` y `TrustedPrincipalArn` completos. Podés leer la plantilla antes de aceptar: un solo recurso, un rol IAM, sin usuarios ni access keys.
3. Marcás la casilla de "acepto que se creen recursos IAM con nombre" y creás el stack. Tarda menos de un minuto. Si preferís la terminal, copiás el comando `aws cloudformation create-stack` que hace lo mismo.
4. Cuando el stack está en `CREATE_COMPLETE`, vas a la pestaña Outputs y copiás `RoleArn` (algo como `arn:aws:iam::123456789012:role/RampaConnect-RampaConnect`).
5. Volvés a Rampa y pegás el ARN en el campo "ARN del rol creado". Guardás.
6. Apretás "Verificar". Rampa asume el rol con el external ID y pregunta a STS quién es. Si funciona, la cuenta pasa a `connected` y muestra la identidad asumida. Si no, queda en `failed` con el motivo (stack incompleto, ARN equivocado, external ID cambiado) y podés reintentar.
7. Desde ahí podés crear clusters en esa cuenta; el `helm install` del agente usa el registry de ECR de la cuenta y el `ClusterSecretStore` hacia Secrets Manager.

Lo que Rampa nunca tiene: access keys, tu kubeconfig, tus secretos. Si querés cortar el acceso, borrás el stack y el rol desaparece.

## GCP (`pkg/provider/gcp`)

Lo que hay: instrucciones para crear el Workload Identity Pool con un provider OIDC apuntando a nuestro issuer (la URL pública de la API) con el `external_id` como audience y subject, una service account con roles de Artifact Registry, GKE y Secret Manager, y el binding `roles/iam.workloadIdentityUser`; formato del registry; `ClusterSecretStore` `gcpsm` con Workload Identity del agente; anotación de GKE; rutas de módulos con `ok=false`.

Qué le falta para ser `Available`:

1. Que el plano de control emita tokens OIDC propios (issuer público con JWKS en `RAMPA_PUBLIC_URL`) con `sub` igual al `external_id` de la cuenta. Hoy la API no es un issuer.
2. `Verify`: canjear ese token en `sts.googleapis.com/v1/token` (federated token), luego `iamcredentials.googleapis.com/.../generateAccessToken` para impersonar la service account, y llamar `cloudresourcemanager.projects.get` para confirmar el acceso. Requiere agregar dependencias (`golang.org/x/oauth2/google` o `cloud.google.com/go`).
3. Una cuenta de GCP para probar contra la nube real.
4. Módulos OpenTofu en `terraform/modules/gcp/`.

## Azure (`pkg/provider/azure`)

Lo que hay: instrucciones para crear la App registration, el service principal y la credencial federada (issuer nuestro, subject `external_id`, audience `api://AzureADTokenExchange`) más las asignaciones de roles `AcrPush`, `Azure Kubernetes Service Cluster User Role` y `Key Vault Secrets Officer`; registry `<acr_name>.azurecr.io` leyendo `settings.acr_name` (ACR es global, no regional, por eso hace falta el nombre); `ClusterSecretStore` `azurekv` con `authType: WorkloadIdentity` hacia `https://<key_vault_name>.vault.azure.net`; anotación de Azure Workload Identity; rutas de módulos con `ok=false`.

Qué le falta para ser `Available`:

1. El mismo issuer OIDC propio que GCP.
2. `Verify`: pedir un token a `login.microsoftonline.com/<tenant>/oauth2/v2.0/token` con `client_assertion` (nuestro JWT) y `client_id`, y leer la suscripción en ARM para confirmar acceso. Requiere `github.com/Azure/azure-sdk-for-go/sdk/azidentity` o hacer el flujo a mano con `net/http`.
3. Una suscripción para probar.
4. Módulos OpenTofu en `terraform/modules/azure/`.

## Notas sobre la interfaz

- `Field.Name == "external_ref"` va a `CloudAccount.ExternalRef`; el resto va a `Settings`. Por eso GCP pide el proyecto y Azure la suscripción como `external_ref` (así lo documenta `provider.go`); `Registry` acepta además `settings.project_id` / `settings.subscription_id` como fallback.
- `ConnectInstructions` recibe `provider.ConnectRequest` con la región elegida al crear la cuenta; el link de CloudFormation la usa y cae a `RAMPA_AWS_DEFAULT_REGION` si viene vacía. El rol IAM es global, así que la región solo decide dónde vive el stack.
- `ClusterInfo` no trae el nombre del `ClusterSecretStore` que eligió el cluster (`clusters.secret_store`), así que cada provider usa su default (`aws-secrets-manager`, `gcp-secret-manager`, `azure-key-vault`). La API debería pasarlo o renombrar `SecretStoreSpec.Object.metadata.name` al valor del cluster.
