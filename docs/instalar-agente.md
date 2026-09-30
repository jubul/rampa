# Instalar el agente de Rampa

El agente es un proceso chico que corre en tu cluster de Kubernetes. Abre una conexión saliente (WebSocket sobre TLS) al plano de control de Rampa y aplica lo que la API le manda: crea el namespace de cada scope, aplica los objetos ya renderizados, observa el rollout y reporta el estado. No abre puertos de entrada y Rampa nunca recibe tu kubeconfig ni credenciales de tu nube.

Esta guía cubre la instalación con el chart `rampa-agent`, cómo darle identidad de nube, cómo verificar, rotar el token, desinstalar y qué permisos pide y por qué. El chart está en `charts/rampa-agent` y es público a propósito: revisalo antes de instalar.

## 1. Prerequisitos

| Qué | Requerido | Notas |
|---|---|---|
| Cluster Kubernetes 1.27 o superior (EKS, GKE, AKS, DOKS, OKE, o una VM con k3s creada por Rampa) | sí | Un agente por cluster. |
| `kubectl` con permisos de cluster-admin en ese cluster | sí | Para instalar el ClusterRole. |
| Helm 3.12 o superior (o Helm 4) | sí | El chart se publica como OCI. |
| Salida HTTPS (443) hacia la URL de la API de Rampa | sí | Es la única conexión que hace el agente al exterior. Si tenés proxy, ver `extraEnv`. |
| Una cuenta de nube conectada en Rampa | sí | La web te la pide antes de crear el cluster. |
| [External Secrets Operator](https://external-secrets.io) | opcional | Necesario si las apps usan parameters secretos **y** el cluster usa el gestor de secretos de la nube. Con `secret_store: kubernetes` (sección 3.1) no hace falta. El chart puede crear el `ClusterSecretStore`. |
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

### Con un bootstrap token (VMs, cloud-init, scripts de instalación)

Un **bootstrap token** (`rampa_bootstrap_...`) es un token de un solo uso que vence en dos horas y que solo sirve para una cosa: canjearlo por el token real del cluster. Lo genera la web (**Clusters > el cluster > Comando de instalación**) o la API (`POST /v1/orgs/{org}/clusters/{id}/bootstrap-token`, rol admin). Es lo que usan los módulos de OpenTofu de Rampa (VMs con k3s en EC2, Droplets, DOKS, EKS) para instalar el agente solos.

Por qué existe: el cloud-init de una VM, el user data y las variables de un pipeline **son legibles** por cualquier proceso de la máquina (metadata del proveedor) o por cualquiera con acceso al repo o al historial del pipeline. Si ahí quedara el token del cluster, quedaría para siempre. Con el bootstrap token queda algo que a las dos horas no sirve y que, en cuanto el agente lo canjea, tampoco: un segundo canje responde `401`.

```bash
helm upgrade --install rampa-agent oci://ghcr.io/CHANGEME/charts/rampa-agent \
  --namespace rampa-system --create-namespace \
  --set api.url=https://api.rampa.dev \
  --set-string cluster.bootstrapToken=rampa_bootstrap_XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
```

`--set-string` evita que Helm interprete el valor como número. Qué pasa después:

1. El chart guarda el bootstrap token en el Secret `rampa-agent-bootstrap` y lo inyecta como `RAMPA_BOOTSTRAP_TOKEN`. También crea un `Role` y un `RoleBinding` en `rampa-system` acotados al Secret `rampa-agent-token` (sección 7).
2. Al arrancar, el agente busca el token del cluster en este orden: `RAMPA_CLUSTER_TOKEN` (no está en este modo), el Secret `rampa-agent-token`, y por último el canje. Como es la primera vez, hace `POST https://api.rampa.dev/v1/agent/bootstrap` y guarda el `cluster_token` que recibe en el Secret `rampa-system/rampa-agent-token`.
3. Si la API todavía no responde (una VM suele arrancar antes de que el plano de control termine de desplegar), reintenta con backoff de 1s a 30s hasta 10 minutos. Mientras tanto `/healthz` responde y `/readyz` no: el pod sigue vivo pero no está listo.
4. En cada arranque siguiente lee el Secret y no vuelve a canjear. El Secret `rampa-agent-bootstrap` ya no sirve para nada y se puede borrar (`kubectl -n rampa-system delete secret rampa-agent-bootstrap`); el pod lo referencia como `optional`.

Si el agente loguea que el bootstrap token "ya se usó o venció", pedí uno nuevo en la web y hacé `helm upgrade ... --set-string cluster.bootstrapToken=<nuevo>`: el pod se reinicia (lleva un hash del bootstrap token en sus anotaciones) y canjea otra vez. Si además el Secret `rampa-agent-token` existe con un token viejo (por ejemplo después de rotar el token, sección 5), borralo antes, porque el agente lo prefiere al bootstrap.

Estado estable recomendado para los próximos `helm upgrade`: pasar a `--set cluster.existingSecret=rampa-agent-token` (sin `bootstrapToken`). El agente recibe el token por variable de entorno desde el Secret que él mismo escribió y el chart deja de renderizar el Role. El Secret `rampa-agent-token` no lo administra Helm: `helm uninstall` no lo borra (sí lo borra `kubectl delete namespace rampa-system`).

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

Todos los values están documentados en `charts/rampa-agent/values.yaml`. Hace falta `api.url` y **exactamente uno** de `cluster.token`, `cluster.existingSecret` o `cluster.bootstrapToken`: si falta o hay más de uno, Helm falla antes de aplicar nada (`values.schema.json` y los templates) con un mensaje que dice exactamente qué pasa.

## 3. Darle identidad de nube al agente

Para desplegar apps el agente solo necesita los permisos de Kubernetes del ClusterRole. Para leer secretos del secret manager de tu nube (vía External Secrets) y, más adelante, provisionar services con OpenTofu, necesita una identidad de nube. Se le da al **ServiceAccount** del agente (`rampa-agent` en el namespace `rampa-system` por defecto) con el mecanismo nativo de cada nube. Nunca hay access keys en el cluster.

Permisos mínimos de la identidad, en las tres nubes: leer secretos cuyo nombre empiece con `rampa/`. El plano de control escribe secretos a través del agente (comando `write_secret` con `target: cloud`: el agente escribe `rampa/<app>/<scope>/<NOMBRE>` con su propia identidad; los valores no se guardan en Rampa), así que si vas a cargar parameters secretos desde la web, la identidad también necesita crear y actualizar secretos con ese prefijo. Si le falta un permiso, el resultado del comando en la web dice exactamente qué acción (`secretsmanager:CreateSecret`, `PutSecretValue`) faltó sobre qué recurso.

En una VM con k3s en EC2 (módulo `aws/ec2-k3s`) la identidad es el instance profile de la instancia y no hace falta IRSA; la región sale de IMDS. Si el agente no la resuelve (por ejemplo IMDS con hop limit 1), agregá `extraEnv: [{name: AWS_REGION, value: <REGION>}]`.

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

### 3.1 Sin gestor de secretos: `secret_store: kubernetes`

Nubes sin gestor de secretos (DigitalOcean, OVH) y VMs con k3s pueden declarar el cluster con `secret_store: kubernetes` en la web (los módulos de OpenTofu de esas nubes ya lo hacen). En ese modo:

- **No hace falta External Secrets Operator.** El chart de las apps no renderiza `ExternalSecret`; el `envFrom` del pod apunta al Secret `<app>-params` con `optional: true`, así que el pod arranca aunque todavía no haya secretos cargados.
- **El agente mantiene el Secret `<app>-params`.** Cuando alguien carga secretos desde la web o la API (`PUT .../scopes/{scope}/secrets`), la API arma un comando `write_secret` con `target: kubernetes` y se lo manda al agente por la conexión viva. El agente crea o actualiza el Secret en el namespace del scope, tipo `Opaque`, haciendo merge de claves (no borra las que no vienen) y etiquetándolo con `rampa.dev/app`, `rampa.dev/scope` y `app.kubernetes.io/managed-by=rampa`.
- **Los valores no quedan en Rampa.** Viajan una sola vez por TLS hasta el agente; la API no los persiste, y ni los eventos, ni los logs, ni el resultado del comando los llevan: solo las claves. Los tests del agente lo verifican.
- **Guardas.** El namespace tiene que llamarse exactamente `<app>-<scope>` (igual que en deploy y undeploy), nunca `default`, `kube-*` ni `rampa-system`, y si ya existe tiene que llevar la label `rampa.dev/app` de esa app; si lleva la de otra app se rechaza sin pisarla, y si no la lleva solo se adopta cuando lo creó Rampa (`app.kubernetes.io/managed-by=rampa`). Si el namespace del scope todavía no existe, lo crea con esas labels: cargar secretos antes del primer deploy es normal. No toca Secrets de otro tipo ni administrados por otro (`managed-by` distinto de `rampa`).
- **Permisos.** Requiere `get, create, update` sobre `secrets` en el ClusterRole (sección 7), que el chart incluye por defecto (`rbac.appSecrets: true`). Es un permiso de cluster: RBAC permite leer cualquier Secret por nombre en cualquier namespace, y lo acota el código del agente y la falta de `list` (detalle en la sección 7). Si tu cluster usa un gestor de secretos de nube por ESO y no querés que el agente tenga permisos sobre `secrets`, instalá con `--set rbac.appSecrets=false`.
- **Límites.** Un `write_secret` admite hasta 50 claves y 64 KiB por valor (el límite de AWS Secrets Manager). Con target `cloud`, si falla a mitad, el resultado lista las claves que sí se escribieron.
- **Scopes con gestor de nube.** Si `<app>-params` lo genera External Secrets (owner reference a `external-secrets.io` o label `reconcile.external-secrets.io/managed`), el agente se niega a escribirlo: ESO revertiría el cambio. Ese scope carga secretos con target `cloud`.
- **Cifrado en reposo.** Un Secret de Kubernetes es base64 en etcd. Las VMs con k3s que crea Rampa activan `secrets-encryption` en el arranque; en un cluster propio, activá el cifrado de secretos en reposo del proveedor (EKS lo hace con KMS, DOKS y OKE lo hacen por defecto) o quedate con el gestor de la nube.

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
   | `bootstrap token canjeado por el token del cluster` | Instalación por bootstrap: el token real ya está guardado en el Secret `rampa-agent-token`. |
   | `la API rechazó el bootstrap token (HTTP 401): ya se usó o venció` | El bootstrap se canjeó antes o pasaron más de dos horas. Generá otro en la web y hacé `helm upgrade` con `--set-string cluster.bootstrapToken=<nuevo>` (sección 2). |
   | `el canje del bootstrap token falló, reintento` | La API todavía no responde o no hay salida HTTPS. El agente insiste hasta 10 minutos. |
   | `no tiene permiso para leer el Secret rampa-system/rampa-agent-token` | Falta el Role del chart: instalaste con `rbac.create=false` sin dar `get` sobre ese Secret. |
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

Si instalaste con `bootstrapToken` y no pasaste a `existingSecret`, el Secret `rampa-agent-token` quedó con el token viejo y el agente lo va a preferir al bootstrap. Tenés dos caminos: actualizar ese Secret con el token nuevo como en el caso de `existingSecret` de arriba, o borrarlo (`kubectl -n rampa-system delete secret rampa-agent-token`) y hacer `helm upgrade ... --set-string cluster.bootstrapToken=<bootstrap nuevo>` para que el agente canjee otra vez.

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

Esto borra el Deployment, los Secrets que creó el chart (`rampa-agent-token` con `cluster.token`, `rampa-agent-bootstrap` con `cluster.bootstrapToken`), el ServiceAccount, el ClusterRole, el ClusterRoleBinding, el Role y RoleBinding del bootstrap y el ClusterSecretStore. El Secret `rampa-agent-token` que escribe el agente tras un bootstrap no es de Helm: lo borra el `kubectl delete namespace`. **No toca los namespaces de las apps** (`<app>-<scope>`): siguen corriendo como estaban, solo que sin nadie que los actualice. Si querés limpiar todo, borrá antes los scopes desde la web para que el agente destruya los namespaces, y después desinstalá. Por último, borrá el cluster en la web para invalidar el token, y el rol o identidad de nube que creaste en la sección 3.

## 7. Permisos del agente y su justificación

El agente pide un `ClusterRole` (`charts/rampa-agent/templates/clusterrole.yaml`, con un comentario por bloque). Es amplio a propósito: el agente es el brazo del plano de control dentro del cluster y gestiona namespaces completos. Estas son las reglas y por qué existen.

| API group | Recursos | Verbos | Por qué |
|---|---|---|---|
| `""` (core) | `namespaces` | get, create, update, delete | Cada scope de una app es un namespace `<app>-<scope>`. Deploy y `write_secret` lo consultan, lo crean con labels y actualizan sus labels; undeploy lo borra completo. Sin `list` ni `watch`. |
| `apps` | `deployments` | get, list, create, patch, delete | Workload de apps `api`, `worker` y `static` con strategy `rolling`. Se aplica con server-side apply (`patch`, y `create` cuando el objeto no existe), se lee con `get` hasta que está sano y el prune usa `list` y `delete`. |
| `apps` | `replicasets` | list | Identificar la revisión nueva de un Deployment y observar solo sus pods durante el rollout. |
| `argoproj.io` | `rollouts` | get, list, create, patch, delete | Workload cuando la strategy es `canary` o `bluegreen`. Requiere Argo Rollouts; si no está instalado la regla no hace nada. |
| `batch` | `cronjobs` | get, list, create, patch, delete | Apps de tipo `cron`. El agente no crea ni lee `jobs`. |
| `""` (core) | `services`, `serviceaccounts`, `resourcequotas` | list, create, patch, delete | Los genera el chart de cada app: Service para el tráfico, ServiceAccount con la identidad de nube de la app, ResourceQuota en los scopes efímeros. Apply y prune de cada deploy; no se leen por `get`. |
| `networking.k8s.io` | `ingresses` | list, create, patch, delete | La URL de cada scope: `<app>-<scope>.<dominio del cluster>` o el `domain` propio del rampa.yaml. |
| `autoscaling` | `horizontalpodautoscalers` | list, create, patch, delete | El `autoscaling` declarado en el scope. |
| `policy` | `poddisruptionbudgets` | list, create, patch, delete | Se genera cuando un scope tiene más de una réplica. |
| `external-secrets.io` | `externalsecrets` | list, create, patch, delete | Trae los parameters secretos de cada app desde tu secret manager. Requiere ESO; si no está la regla no hace nada. El agente no toca el `ClusterSecretStore`: lo instala Helm con este chart. |
| `""` (core) | `pods` | list | **Solo lectura.** Para diagnosticar por qué un rollout no avanza (ImagePullBackOff, CrashLoopBackOff) y elegir el pod del comando `logs`. El agente nunca crea, ejecuta ni borra pods. |
| `""` (core) | `pods/log` | get | **Solo lectura.** El tail de logs que se ve en la web. |
| `""` (core) | `nodes` | list | **Solo lectura.** Contar nodos para el `heartbeat` que ve la web. Si se quita, el agente reporta `nodes: 0` y sigue funcionando. |
| `""` (core) | `secrets` (solo con `rbac.appSecrets=true`, el default) | get, create, update | Comando `write_secret` con `target: kubernetes` (sección 3.1): leer, crear o actualizar el Secret `<app>-params`. **Ver el alcance real más abajo.** Se quita con `rbac.appSecrets=false`. |
| `""` (core), **Role** en `rampa-system` (solo con `cluster.bootstrapToken`) | `secrets`, `resourceNames: [rampa-agent-token]` | get, list, update, patch | Leer en cada arranque y actualizar el token del cluster que el agente obtuvo al canjear el bootstrap token. Acotado por `resourceNames` a ese único Secret; `list` solo se autoriza con `fieldSelector=metadata.name=rampa-agent-token`. |
| `""` (core), **Role** en `rampa-system` (solo con `cluster.bootstrapToken`) | `secrets` | create | Crear `rampa-agent-token` la primera vez. Va en una regla aparte porque RBAC autoriza el `create` antes de conocer el nombre del objeto: `resourceNames` no aplica a `create` y una regla que lo combine nunca autoriza nada. Sigue limitado al namespace del agente y no da lectura. |

Ninguna regla incluye `watch`: el agente consulta por polling y no usa watches. Tampoco tiene permisos sobre `events`, `jobs`, `configmaps`, `clusterroles`, `clusterrolebindings` ni `pods/exec`. Sobre `secrets` tiene exactamente lo de la fila de `secrets` y las dos del Role de bootstrap: con un gestor de secretos de nube los secretos de las apps entran vía ESO y podés dejar al agente sin ninguno con `rbac.appSecrets=false` e instalando con `cluster.token` o `cluster.existingSecret` (sin Role).

**Alcance real del permiso sobre `secrets`.** Es un ClusterRole, así que RBAC permite hacer `get` de cualquier Secret de cualquier namespace si se conoce su nombre, y `create` y `update` en cualquier namespace. La frase "solo puede tocar uno por nombre" describe lo que hace el código del agente, no lo que impone Kubernetes. Lo que lo acota: (1) el código solo escribe el Secret `<app>-params` en el namespace `<app>-<scope>` que lleva la label `rampa.dev/app` de esa app, y no toca namespaces del sistema ni Secrets de otro tipo, de otro administrador ni gestionados por External Secrets; (2) no hay `list` ni `watch`, así que no se pueden enumerar los Secrets del cluster; (3) el agente no expone ningún comando que lea un Secret y devuelva su contenido. Si comprometen al agente o al plano de control, ese permiso permitiría leer Secrets cuyo nombre se adivine. Si tu cluster usa un gestor de secretos de nube (AWS, GCP, Azure), instalá con `--set rbac.appSecrets=false`: el ClusterRole queda sin ninguna regla sobre `secrets` y los parameters entran por External Secrets.

Si tu política de seguridad no permite un ClusterRole así, instalá con `rbac.create=false` y `serviceAccount.create=false`, y proveé un ServiceAccount con los permisos que consideres. Tené en cuenta que sin permiso sobre `namespaces` el agente no puede crear scopes nuevos.

## 8. Endurecimiento adicional (opcional)

- **Menos permisos sobre `secrets`.** Con gestor de secretos de nube, `rbac.appSecrets=false`; y después de un bootstrap, pasar a `cluster.existingSecret=rampa-agent-token` para que el chart deje de renderizar el Role.
- **Egreso.** El agente solo necesita salida HTTPS a `api.url`, a la API de Kubernetes del cluster y a los endpoints del secret manager de tu nube. Podés restringirlo con una NetworkPolicy de egreso en `rampa-system`; el chart no la incluye porque depende de tu CNI.
- **Prioridad.** `priorityClassName: system-cluster-critical` evita que el agente sea desalojado antes que las apps; si el agente se cae, el cluster queda offline hasta que vuelva.
- **Imagen.** Fijá `image.tag` a una versión concreta y, si tu registry es privado, usá `imagePullSecrets`.
- **Pod Security.** El chart cumple el perfil `restricted` de Pod Security Standards: `runAsNonRoot`, `readOnlyRootFilesystem`, `drop: [ALL]`, `seccompProfile: RuntimeDefault`, sin escalado de privilegios. Podés etiquetar el namespace con `pod-security.kubernetes.io/enforce=restricted`.
