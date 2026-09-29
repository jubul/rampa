# Instalar el agente de Rampa

El agente es un proceso chico que corre en tu cluster de Kubernetes. Abre una conexión saliente (WebSocket sobre TLS) al plano de control de Rampa y aplica lo que la API le manda: crea el namespace de cada scope, aplica los objetos ya renderizados, observa el rollout y reporta el estado. No abre puertos de entrada y Rampa nunca recibe tu kubeconfig ni credenciales de tu nube.

Esta guía cubre la instalación con el chart `rampa-agent`, cómo darle identidad de nube, cómo verificar, rotar el token, desinstalar y qué permisos pide y por qué. El chart está en `charts/rampa-agent` y es público a propósito: revisalo antes de instalar.

## 1. Prerequisitos

| Qué | Requerido | Notas |
|---|---|---|
| Cluster Kubernetes 1.27 o superior (EKS, GKE, AKS) | sí | Un agente por cluster. |
| `kubectl` con permisos de cluster-admin en ese cluster | sí | Para instalar el ClusterRole. |
| Helm 3.12 o superior (o Helm 4) | sí | El chart se publica como OCI. |
| Salida HTTPS (443) hacia la URL de la API de Rampa | sí | Es la única conexión que hace el agente al exterior. Si tenés proxy, ver `extraEnv`. |
| Una cuenta de nube conectada en Rampa | sí | La web te la pide antes de crear el cluster. |
| [External Secrets Operator](https://external-secrets.io) | opcional | Necesario si las apps usan parameters secretos. El chart puede crear el `ClusterSecretStore`. |
| [Argo Rollouts](https://argoproj.github.io/rollouts/) | opcional | Necesario si alguna app usa strategy `canary` o `bluegreen`. |

## 2. Instalar

En la web, en la organización, entrá a **Clusters > Nuevo cluster**. Elegí la cuenta de nube, el entorno (`nonprod` o `prod`), el dominio wildcard para los scopes y la ingress class. Al crear, la web muestra **una sola vez** el token del cluster y el comando de instalación listo para pegar. Tiene esta forma:

```bash
helm upgrade --install rampa-agent oci://ghcr.io/CHANGEME/charts/rampa-agent \
  --namespace rampa-system --create-namespace \
  --set api.url=https://api.rampa.dev \
  --set cluster.token=rampa_cluster_XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
```

Eso es todo lo que hace falta para que el cluster aparezca online. Los pasos siguientes (identidad de nube, secret store) se pueden hacer después con `helm upgrade`.

### Si preferís no pasar el token por línea de comandos

Guardalo en un Secret propio y apuntá el chart a él. El chart no crea ningún Secret en este caso.

```bash
kubectl -n rampa-system create secret generic rampa-agent-token \
  --from-literal=token=rampa_cluster_XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX

helm upgrade --install rampa-agent oci://ghcr.io/CHANGEME/charts/rampa-agent \
  --namespace rampa-system --create-namespace \
  --set api.url=https://api.rampa.dev \
  --set cluster.existingSecret=rampa-agent-token
```

### Con un archivo de values

Para dejarlo versionado en tu repo de infraestructura:

```yaml
# rampa-agent-values.yaml
api:
  url: https://api.rampa.dev
cluster:
  existingSecret: rampa-agent-token
serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/rampa-agent
secretStore:
  enabled: true
  name: aws-secrets-manager
  provider: aws
  aws:
    region: us-east-1
priorityClassName: system-cluster-critical
```

```bash
helm upgrade --install rampa-agent oci://ghcr.io/CHANGEME/charts/rampa-agent \
  -n rampa-system --create-namespace -f rampa-agent-values.yaml
```

Todos los values están documentados en `charts/rampa-agent/values.yaml`. Si falta `api.url` o no hay ni `cluster.token` ni `cluster.existingSecret`, Helm falla antes de aplicar nada con un mensaje que dice exactamente qué falta.

## 3. Darle identidad de nube al agente

Para desplegar apps el agente solo necesita los permisos de Kubernetes del ClusterRole. Para leer secretos del secret manager de tu nube (vía External Secrets) y, más adelante, provisionar services con OpenTofu, necesita una identidad de nube. Se le da al **ServiceAccount** del agente (`rampa-agent` en el namespace `rampa-system` por defecto) con el mecanismo nativo de cada nube. Nunca hay access keys en el cluster.

Permisos mínimos de la identidad, en las tres nubes: leer secretos cuyo nombre empiece con `rampa/`. El plano de control escribe secretos a través del agente, así que si vas a cargar parameters secretos desde la web, la identidad también necesita crear y actualizar secretos con ese prefijo.

### AWS (EKS): IRSA

1. Asegurate de que el cluster tenga un proveedor OIDC de IAM asociado:

   ```bash
   eksctl utils associate-iam-oidc-provider --cluster <CLUSTER> --region <REGION> --approve
   ```

2. Creá la policy con los permisos sobre Secrets Manager:

   ```bash
   cat > rampa-agent-policy.json <<'JSON'
   {
     "Version": "2012-10-17",
     "Statement": [
       {
         "Effect": "Allow",
         "Action": [
           "secretsmanager:GetSecretValue",
           "secretsmanager:DescribeSecret",
           "secretsmanager:CreateSecret",
           "secretsmanager:PutSecretValue",
           "secretsmanager:TagResource"
         ],
         "Resource": "arn:aws:secretsmanager:*:*:secret:rampa/*"
       },
       {
         "Effect": "Allow",
         "Action": ["secretsmanager:ListSecrets"],
         "Resource": "*"
       }
     ]
   }
   JSON
   aws iam create-policy --policy-name rampa-agent --policy-document file://rampa-agent-policy.json
   ```

3. Creá el rol asociado al ServiceAccount del agente (esto crea el rol con la trust policy correcta y lo pega a la policy):

   ```bash
   eksctl create iamserviceaccount \
     --cluster <CLUSTER> --region <REGION> \
     --namespace rampa-system --name rampa-agent \
     --role-name rampa-agent \
     --attach-policy-arn arn:aws:iam::<ACCOUNT_ID>:policy/rampa-agent \
     --role-only --approve
   ```

   `--role-only` crea solo el rol IAM: el ServiceAccount lo sigue administrando el chart.

4. Anotá el ServiceAccount vía el chart y, si usás ESO, activá el secret store:

   ```bash
   helm upgrade rampa-agent oci://ghcr.io/CHANGEME/charts/rampa-agent -n rampa-system --reuse-values \
     --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::<ACCOUNT_ID>:role/rampa-agent \
     --set secretStore.enabled=true --set secretStore.provider=aws --set secretStore.aws.region=<REGION>
   ```

   El nombre del `ClusterSecretStore` (`secretStore.name`, default `aws-secrets-manager`) tiene que coincidir con el "secret store" que cargaste al crear el cluster en la web.

Si usás EKS Pod Identity en lugar de IRSA, creá la asociación con `aws eks create-pod-identity-association --cluster-name <CLUSTER> --namespace rampa-system --service-account rampa-agent --role-arn <ROLE_ARN>`; en ese caso no hace falta la anotación del SA.

### GCP (GKE): Workload Identity

1. El cluster tiene que tener Workload Identity habilitado (`--workload-pool=<PROJECT_ID>.svc.id.goog`).

2. Creá la service account de Google y dale acceso a Secret Manager:

   ```bash
   gcloud iam service-accounts create rampa-agent --project <PROJECT_ID>

   gcloud projects add-iam-policy-binding <PROJECT_ID> \
     --member "serviceAccount:rampa-agent@<PROJECT_ID>.iam.gserviceaccount.com" \
     --role roles/secretmanager.secretAccessor \
     --condition='expression=resource.name.startsWith("projects/<PROJECT_NUMBER>/secrets/rampa-"),title=rampa-prefix'

   # Solo si vas a escribir secretos desde la web:
   gcloud projects add-iam-policy-binding <PROJECT_ID> \
     --member "serviceAccount:rampa-agent@<PROJECT_ID>.iam.gserviceaccount.com" \
     --role roles/secretmanager.admin \
     --condition='expression=resource.name.startsWith("projects/<PROJECT_NUMBER>/secrets/rampa-"),title=rampa-prefix'
   ```

   En GCP los nombres de secretos no admiten `/`, así que el prefijo es `rampa-`.

3. Permití que el ServiceAccount de Kubernetes se haga pasar por la SA de Google:

   ```bash
   gcloud iam service-accounts add-iam-policy-binding \
     rampa-agent@<PROJECT_ID>.iam.gserviceaccount.com \
     --role roles/iam.workloadIdentityUser \
     --member "serviceAccount:<PROJECT_ID>.svc.id.goog[rampa-system/rampa-agent]"
   ```

4. Anotá el SA vía el chart y activá el secret store:

   ```bash
   helm upgrade rampa-agent oci://ghcr.io/CHANGEME/charts/rampa-agent -n rampa-system --reuse-values \
     --set serviceAccount.annotations."iam\.gke\.io/gcp-service-account"=rampa-agent@<PROJECT_ID>.iam.gserviceaccount.com \
     --set secretStore.enabled=true --set secretStore.provider=gcp --set secretStore.name=gcp-secret-manager \
     --set secretStore.gcp.projectID=<PROJECT_ID> \
     --set secretStore.gcp.clusterLocation=<REGION_O_ZONA> --set secretStore.gcp.clusterName=<CLUSTER>
   ```

   ESO necesita `clusterLocation` y `clusterName` para intercambiar el token de Workload Identity.

### Azure (AKS): Workload Identity

1. El cluster tiene que tener el OIDC issuer y Workload Identity habilitados:

   ```bash
   az aks update -g <RG> -n <CLUSTER> --enable-oidc-issuer --enable-workload-identity
   ISSUER=$(az aks show -g <RG> -n <CLUSTER> --query oidcIssuerProfile.issuerUrl -o tsv)
   ```

2. Creá la managed identity y dale acceso al Key Vault (modelo RBAC):

   ```bash
   az identity create -g <RG> -n rampa-agent
   CLIENT_ID=$(az identity show -g <RG> -n rampa-agent --query clientId -o tsv)
   PRINCIPAL_ID=$(az identity show -g <RG> -n rampa-agent --query principalId -o tsv)
   KV_ID=$(az keyvault show -n <KEYVAULT> --query id -o tsv)

   az role assignment create --assignee-object-id "$PRINCIPAL_ID" --assignee-principal-type ServicePrincipal \
     --role "Key Vault Secrets User" --scope "$KV_ID"
   # Solo si vas a escribir secretos desde la web:
   az role assignment create --assignee-object-id "$PRINCIPAL_ID" --assignee-principal-type ServicePrincipal \
     --role "Key Vault Secrets Officer" --scope "$KV_ID"
   ```

   Key Vault no admite `/` en los nombres, así que el prefijo es `rampa-`. El scope de RBAC es el vault entero; usá un vault dedicado a Rampa.

3. Creá la credencial federada hacia el ServiceAccount del agente:

   ```bash
   az identity federated-credential create -g <RG> --identity-name rampa-agent -n rampa-agent \
     --issuer "$ISSUER" --subject system:serviceaccount:rampa-system:rampa-agent \
     --audiences api://AzureADTokenExchange
   ```

4. Anotá el SA, marcá el pod y activá el secret store:

   ```bash
   TENANT_ID=$(az account show --query tenantId -o tsv)
   helm upgrade rampa-agent oci://ghcr.io/CHANGEME/charts/rampa-agent -n rampa-system --reuse-values \
     --set serviceAccount.annotations."azure\.workload\.identity/client-id"=$CLIENT_ID \
     --set podLabels."azure\.workload\.identity/use"=\"true\" \
     --set secretStore.enabled=true --set secretStore.provider=azure --set secretStore.name=azure-key-vault \
     --set secretStore.azure.vaultURL=https://<KEYVAULT>.vault.azure.net \
     --set secretStore.azure.tenantID=$TENANT_ID
   ```

   El label `azure.workload.identity/use: "true"` en el pod es obligatorio para que el webhook de Azure inyecte el token federado.

## 4. Verificar

1. El pod tiene que estar `Running` y `READY 1/1`. La readiness solo pasa cuando el agente conectó con el plano de control:

   ```bash
   kubectl -n rampa-system get pods -l app.kubernetes.io/name=rampa-agent
   ```

2. Los logs tienen que mostrar la conexión aceptada con el nombre del cluster y la organización:

   ```bash
   kubectl -n rampa-system logs deploy/rampa-agent -f
   ```

   | Lo que ves | Qué significa |
   |---|---|
   | `welcome` con `cluster_name` y `org_slug` | Todo bien. |
   | `unauthorized` | El token es inválido o fue rotado. Generá uno nuevo en la web (sección 5). |
   | `dial`, `timeout`, `no such host` | El cluster no tiene salida HTTPS hacia `api.url`, o hay un proxy. Revisá egress y `extraEnv` (`HTTPS_PROXY`, `NO_PROXY`). |
   | `x509` | TLS: el proxy corporativo intercepta o la hora del nodo está mal. |

3. En la web, en **Clusters**, el cluster tiene que aparecer como **online** en menos de un minuto, con la versión del agente y de Kubernetes. Si sigue **offline**, los logs del paso anterior dicen por qué.

4. Si activaste `secretStore`, verificá que ESO lo aceptó:

   ```bash
   kubectl get clustersecretstore aws-secrets-manager
   # STATUS tiene que ser Valid. Si dice InvalidProviderConfig, la identidad de nube del SA no alcanza.
   ```

5. Verificación de identidad de nube (opcional): tirá un pod efímero con el mismo ServiceAccount y probá la identidad.

   ```bash
   # AWS
   kubectl -n rampa-system run aws-check --rm -it --restart=Never \
     --overrides='{"spec":{"serviceAccountName":"rampa-agent"}}' \
     --image=public.ecr.aws/aws-cli/aws-cli -- sts get-caller-identity
   ```

## 5. Rotar el token

El token del cluster se puede rotar en cualquier momento desde la web (**Clusters > el cluster > Rotar token**) o con la API (`POST /v1/orgs/{org}/clusters/{id}/rotate-token`). El token anterior deja de valer en el acto: el agente se desconecta y no puede volver a entrar hasta recibir el nuevo.

Si el chart administra el Secret:

```bash
helm upgrade rampa-agent oci://ghcr.io/CHANGEME/charts/rampa-agent -n rampa-system --reuse-values \
  --set cluster.token=rampa_cluster_NUEVO
```

El Deployment lleva una anotación con el hash del token, así que el pod se reinicia solo y toma el nuevo valor.

Si usás `existingSecret`:

```bash
kubectl -n rampa-system create secret generic rampa-agent-token \
  --from-literal=token=rampa_cluster_NUEVO --dry-run=client -o yaml | kubectl apply -f -
kubectl -n rampa-system rollout restart deploy/rampa-agent
```

El reinicio es necesario porque el token se inyecta como variable de entorno. Mientras el agente está caído los deploys quedan encolados en el plano de control y se ejecutan al reconectar: no se pierde nada.

## 6. Actualizar y desinstalar

Actualizar a una nueva versión del chart o del agente:

```bash
helm upgrade rampa-agent oci://ghcr.io/CHANGEME/charts/rampa-agent -n rampa-system --reuse-values --version <VERSION>
```

La strategy es `Recreate`: el agente viejo se apaga antes de que arranque el nuevo, hay unos segundos sin conexión y el cluster puede parpadear a offline en la web.

Desinstalar:

```bash
helm uninstall rampa-agent -n rampa-system
kubectl delete namespace rampa-system
```

Esto borra el Deployment, el Secret del token, el ServiceAccount, el ClusterRole, el ClusterRoleBinding y el ClusterSecretStore. **No toca los namespaces de las apps** (`<app>-<scope>`): siguen corriendo como estaban, solo que sin nadie que los actualice. Si querés limpiar todo, borrá antes los scopes desde la web para que el agente destruya los namespaces, y después desinstalá. Por último, borrá el cluster en la web para invalidar el token, y el rol o identidad de nube que creaste en la sección 3.

## 7. Permisos del agente y su justificación

El agente pide un `ClusterRole` (`charts/rampa-agent/templates/clusterrole.yaml`, con un comentario por bloque). Es amplio a propósito: el agente es el brazo del plano de control dentro del cluster y gestiona namespaces completos. Estas son las reglas y por qué existen.

| API group | Recursos | Verbos | Por qué |
|---|---|---|---|
| `""` (core) | `namespaces` | get, list, watch, create, update, patch, delete | Cada scope de una app es un namespace `<app>-<scope>`. Deploy lo crea con labels; undeploy lo borra completo. |
| `apps` | `deployments` | todos | Workload de apps `api`, `worker` y `static` con strategy `rolling`. Se aplica con server-side apply y se observa hasta que está sano. |
| `apps` | `replicasets` | `get`, `list`, `watch` | Identificar la revisión nueva de un Deployment y observar solo sus pods durante el rollout. |
| `argoproj.io` | `rollouts` | todos | Workload cuando la strategy es `canary` o `bluegreen`. Requiere Argo Rollouts; si no está instalado la regla no hace nada. |
| `batch` | `cronjobs`, `jobs` | todos | Apps de tipo `cron`. Los Jobs para observar y limpiar los que genera el CronJob y, en la Fase C, para correr OpenTofu que provisiona services. |
| `""` (core) | `services`, `serviceaccounts`, `resourcequotas` | todos | Los genera el chart de cada app: Service para el tráfico, ServiceAccount con la identidad de nube de la app, ResourceQuota en los scopes efímeros. Se borran en el prune de cada deploy. |
| `""` (core) | `pods`, `pods/log` | get, list, watch | **Solo lectura.** Para diagnosticar por qué un rollout no avanza (ImagePullBackOff, CrashLoopBackOff) y para el tail de logs que se ve en la web. El agente nunca crea, ejecuta ni borra pods. |
| `""` (core) | `nodes` | list | **Solo lectura.** Contar nodos para el `heartbeat` que ve la web. Si se quita, el agente reporta `nodes: 0` y sigue funcionando. |
| `""` (core) | `events` | get, list, watch | **Solo lectura.** Los eventos de Kubernetes relevantes a un deploy se reenvían al plano de control como detalle del timeline. |
| `networking.k8s.io` | `ingresses` | todos | La URL de cada scope: `<app>-<scope>.<dominio del cluster>` o el `domain` propio del rampa.yaml. |
| `autoscaling` | `horizontalpodautoscalers` | todos | El `autoscaling` declarado en el scope. |
| `policy` | `poddisruptionbudgets` | todos | Se genera cuando un scope tiene más de una réplica. |
| `external-secrets.io` | `externalsecrets`, `clustersecretstores` | todos | `ExternalSecret` trae los parameters secretos de cada app desde tu secret manager; `ClusterSecretStore` es el que instala este chart. Requiere ESO; si no está la regla no hace nada. |

"Todos" es `get, list, watch, create, update, patch, delete`. El agente no tiene permisos sobre `secrets`, `configmaps`, `clusterroles`, `clusterrolebindings` ni sobre `pods/exec`: los secretos de las apps entran al namespace vía ESO, y el agente ni los lee ni los escribe en Kubernetes.

Si tu política de seguridad no permite un ClusterRole así, instalá con `rbac.create=false` y `serviceAccount.create=false`, y proveé un ServiceAccount con los permisos que consideres. Tené en cuenta que sin permiso sobre `namespaces` el agente no puede crear scopes nuevos.

## 8. Endurecimiento adicional (opcional)

- **Egreso.** El agente solo necesita salida HTTPS a `api.url`, a la API de Kubernetes del cluster y a los endpoints del secret manager de tu nube. Podés restringirlo con una NetworkPolicy de egreso en `rampa-system`; el chart no la incluye porque depende de tu CNI.
- **Prioridad.** `priorityClassName: system-cluster-critical` evita que el agente sea desalojado antes que las apps; si el agente se cae, el cluster queda offline hasta que vuelva.
- **Imagen.** Fijá `image.tag` a una versión concreta y, si tu registry es privado, usá `imagePullSecrets`.
- **Pod Security.** El chart cumple el perfil `restricted` de Pod Security Standards: `runAsNonRoot`, `readOnlyRootFilesystem`, `drop: [ALL]`, `seccompProfile: RuntimeDefault`, sin escalado de privilegios. Podés etiquetar el namespace con `pod-security.kubernetes.io/enforce=restricted`.
