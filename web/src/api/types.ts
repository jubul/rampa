// Tipos de la API v1 de Rampa. Espejo de docs/api.md: nombres en snake_case,
// fechas RFC 3339 en UTC, ids UUID como strings.

export type ErrorCode = "unauthorized" | "forbidden" | "not_found" | "invalid" | "conflict" | "internal";

export interface ApiErrorBody {
  error: {
    code: ErrorCode | string;
    message: string;
    details?: string[];
  };
}

export interface ListResponse<T> {
  items: T[];
}

// Roles por organización, de mayor a menor.
export type Role = "owner" | "admin" | "developer" | "viewer";
export const ROLES: Role[] = ["owner", "admin", "developer", "viewer"];

export interface User {
  id: string;
  email: string;
  name: string;
}

export interface OrgSummary {
  id: string;
  slug: string;
  name: string;
}

export interface Membership {
  org: OrgSummary;
  role: Role;
}

export interface Me {
  user: User;
  memberships: Membership[];
  /** Idioma preferido del usuario (es o en). Si está, gana sobre el navegador. */
  locale?: string | null;
}

export interface PatchMeRequest {
  locale: string;
}

export interface OrgSettings {
  require_approval_prod: boolean;
  /** Organización de GitHub donde vive la copia de rampa que corre los workflows. */
  github_org?: string;
}

export interface Org extends OrgSummary {
  created_at: string;
  settings: OrgSettings;
}

export interface CreateOrgRequest {
  slug: string;
  name: string;
}

// PATCH /v1/orgs/{org}: nombre y políticas. Solo admin.
export interface PatchOrgRequest {
  name?: string;
  settings?: OrgSettings;
}

export interface Member {
  user: User;
  role: Role;
}

export interface AddMemberRequest {
  email: string;
  role: Role;
}

export interface Token {
  id: string;
  name: string;
  role: Role;
  expires_at: string | null;
  created_at: string;
  last_used_at?: string | null;
  // Solo viene en la respuesta del POST, una sola vez.
  token?: string;
}

export interface CreateTokenRequest {
  name: string;
  role: Role;
  expires_in_days: number;
}

// Providers y cuentas de nube

export type ProviderKind = "aws" | "gcp" | "azure" | "digitalocean" | "oci" | "ovh";

/** federated: la cuenta se conecta sin secretos. token: se guarda un token del cliente, cifrado. */
export type ProviderAuth = "federated" | "token";

export interface LocalizedTerm {
  es: string;
  en: string;
}

export type TerminologyKey = "account" | "cluster" | "vm" | "registry" | "secret_store" | "region";

export type Terminology = Partial<Record<TerminologyKey, LocalizedTerm>>;

export interface Provider {
  kind: ProviderKind | string;
  display_name: string;
  available: boolean;
  auth?: ProviderAuth;
  terminology?: Terminology;
}

export interface InstructionField {
  name: string;
  label: string;
  placeholder?: string;
  help?: string;
  required: boolean;
  /** true: se renderiza como password, se manda en el PATCH y no se vuelve a mostrar. */
  secret?: boolean;
}

export interface Instructions {
  title: string;
  description: string;
  url?: string;
  command?: string;
  fields: InstructionField[];
}

export interface Verification {
  ok: boolean;
  identity: string;
  message: string;
}

export type CloudAccountStatus = "pending" | "connected" | "failed";

export interface CloudAccount {
  id: string;
  provider: ProviderKind | string;
  name: string;
  region: string;
  external_ref: string;
  settings: Record<string, string>;
  status: CloudAccountStatus;
  verification: Verification | null;
  instructions: Instructions | null;
  /** true si la cuenta (auth token) tiene credenciales guardadas. Nunca se devuelven. */
  has_credentials?: boolean;
  created_at: string;
}

export interface CreateCloudAccountRequest {
  provider: ProviderKind | string;
  name: string;
  region: string;
}

export interface PatchCloudAccountRequest {
  external_ref?: string;
  settings?: Record<string, string>;
}

// Clusters

export type ClusterEnv = "nonprod" | "prod";
export type ClusterStatus = "offline" | "online" | "failed";
/** kubernetes: cluster administrado o registrado. vm: una máquina de un solo nodo con k3s. */
export type ClusterKind = "kubernetes" | "vm";

export interface Cluster {
  id: string;
  name: string;
  kind?: ClusterKind;
  env: ClusterEnv;
  cloud_account_id: string;
  domain: string;
  ingress_class: string;
  secret_store: string;
  status: ClusterStatus;
  agent_version: string;
  kubernetes_version: string;
  last_seen_at: string | null;
  /** Presente si el cluster lo creó Rampa con OpenTofu. */
  infra_run_id?: string | null;
  agent_role_arn?: string;
  created_at: string;
}

// Respuesta del POST y del rotate-token: el token y el comando de instalación
// se muestran una sola vez.
export interface ClusterWithToken extends Cluster {
  token: string;
  install_command: string;
}

// GET /clusters/{id}/install-command
export interface InstallCommandResponse {
  install_command: string;
}

export interface CreateClusterRequest {
  name: string;
  env: ClusterEnv;
  cloud_account_id: string;
  domain: string;
  ingress_class: string;
  secret_store: string;
}

// Applications

export type AppType = "api" | "worker" | "cron" | "static";

export interface Application {
  id: string;
  name: string;
  team: string;
  tier: number;
  description: string;
  type: AppType | string;
  runtime: string;
  repo_url: string;
  manifest: string;
  manifest_sha: string;
  scopes_declared: string[];
  created_at: string;
  updated_at: string;
}

export interface ImportAppRequest {
  manifest: string;
  repo_url: string;
}

export interface UpdateManifestRequest {
  manifest: string;
}

// Scopes

export type ScopeStatus = "unbound" | "idle" | "deploying" | "healthy" | "degraded" | "failed" | "deleting";

export type ScopeCompute = "kubernetes" | "serverless";

export interface Scope {
  name: string;
  compute?: ScopeCompute;
  ephemeral: boolean;
  declared: boolean;
  cluster_id: string | null;
  namespace: string;
  url: string;
  status: ScopeStatus;
  current_deployment: Deployment | null;
  size: string;
  replicas: number;
  strategy: string;
  ttl_expires_at: string | null;
}

export interface BindScopeRequest {
  cluster_id: string;
}

// Deployments

export type DeploymentStatus =
  | "awaiting_approval"
  | "queued"
  | "applying"
  | "progressing"
  | "healthy"
  | "failed"
  | "cancelled";

export type WorkloadPhase = "applying" | "progressing" | "healthy" | "failed";

export interface WorkloadStatus {
  kind: string;
  phase: WorkloadPhase;
  desired: number;
  ready: number;
  updated: number;
  available: number;
  image?: string;
  message?: string;
}

export interface Deployment {
  id: string;
  app: string;
  scope: string;
  cluster_id: string;
  image: string;
  status: DeploymentStatus;
  message: string;
  requested_by: User;
  approved_by: User | null;
  workload: WorkloadStatus | null;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
}

export interface CreateDeploymentRequest {
  image: string;
  message?: string;
}

// Eventos

export type ActorType = "user" | "token" | "agent" | "system";

export interface Actor {
  type: ActorType;
  id: string;
  name: string;
}

export type SubjectType = "org" | "cloud_account" | "cluster" | "application" | "scope" | "deployment";

export interface Event {
  id: string;
  kind: string;
  actor: Actor;
  subject_type: SubjectType;
  subject_id: string;
  message: string;
  payload: Record<string, unknown>;
  created_at: string;
}

export interface EventsFilter {
  subject_type?: SubjectType;
  subject_id?: string;
  limit?: number;
  before?: string;
}

// Lista cerrada de kinds de docs/api.md (sección Eventos). EventSource solo
// entrega eventos con nombre si hay un listener registrado para ese nombre,
// así que los enumeramos acá. Si la API agrega un kind, va también en el doc.
export const EVENT_KINDS: readonly string[] = [
  "org.created",
  "org.updated",
  "org.member_set",
  "org.member_removed",
  "org.token_created",
  "org.token_deleted",
  "cloud_account.created",
  "cloud_account.updated",
  "cloud_account.verified",
  "cloud_account.failed",
  "cloud_account.deleted",
  "cluster.created",
  "cluster.online",
  "cluster.offline",
  "cluster.token_rotated",
  "cluster.deleted",
  "cluster.command_progress",
  "cluster.infra_ready",
  "infra_run.status",
  "application.imported",
  "application.updated",
  "application.deleted",
  "scope.bound",
  "scope.delete_requested",
  "scope.progress",
  "scope.deleted",
  "scope.failed",
  "scope.secrets_written",
  "deployment.requested",
  "deployment.awaiting_approval",
  "deployment.approved",
  "deployment.cancelled",
  "deployment.queued",
  "deployment.applying",
  "deployment.progress",
  "deployment.progressing",
  "deployment.healthy",
  "deployment.failed",
];

// Spec estructurado de una application (docs/api.md, "Applications sin YAML").

export type SpecSize = "S" | "M" | "L" | "XL";
export type SpecStrategy = "rolling" | "canary" | "bluegreen";
export type SpecScopeName = "dev" | "staging" | "prod" | "pr";
export type SpecParameterSource = "value" | "secret" | "service";
export type SpecServiceType = "postgres" | "redis" | "sqs" | "s3" | "dynamodb";

export interface SpecAutoscaling {
  min: number;
  max: number;
  cpu: number;
}

export interface SpecScope {
  name: SpecScopeName | string;
  /** kubernetes (default) o serverless. Solo se envía si no es el default. */
  compute?: ScopeCompute | string;
  size: SpecSize | string;
  replicas: number;
  strategy: SpecStrategy | string;
  domain: string;
  autoscaling: SpecAutoscaling | null;
  ttl: string;
}

export interface SpecParameter {
  name: string;
  source: SpecParameterSource;
  value?: string;
  service?: string;
  key?: string;
  /** Vacío significa todos los scopes. */
  scopes: string[];
}

export interface SpecService {
  name: string;
  type: SpecServiceType | string;
  size: SpecSize | string;
  scopes: string[];
}

export interface ApplicationSpec {
  name: string;
  team: string;
  tier: number;
  description: string;
  type: AppType | string;
  runtime: string;
  port: number;
  healthcheck: string;
  schedule: string;
  scopes: SpecScope[];
  parameters: SpecParameter[];
  services: SpecService[];
}

export interface PreviewResponse {
  manifest: string;
  valid: boolean;
  details: string[];
}

export interface FromSpecRequest {
  spec: ApplicationSpec;
  repo_url?: string;
}

export interface AppSpecResponse {
  spec: ApplicationSpec;
  manifest: string;
}

// Onboarding

export type OnboardingStepKey = "cloud" | "cluster" | "agent" | "app" | "deploy";

export interface OnboardingStep {
  key: OnboardingStepKey | string;
  title: string;
  done: boolean;
  detail: string;
}

export interface Onboarding {
  steps: OnboardingStep[];
  next: string;
  complete: boolean;
}

// Infraestructura (OpenTofu ejecutado por Rampa)

export type InfraRunStatus =
  | "queued"
  | "initializing"
  | "planning"
  | "applying"
  | "succeeded"
  | "failed"
  | "destroying"
  | "destroyed";

export interface InfraRun {
  id: string;
  kind: string;
  provider: string;
  module: string;
  cloud_account_id: string;
  cluster_id: string | null;
  name: string;
  status: InfraRunStatus | string;
  operation?: "apply" | "destroy";
  attempt?: number;
  inputs: Record<string, unknown>;
  outputs: Record<string, unknown>;
  error: string;
  log_tail: string[];
  requested_by: User | null;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
}

export interface InfraModuleInput {
  name: string;
  label?: string;
  help?: string;
  description?: string;
  /** string (default), number, bool o list. */
  type?: string;
  required?: boolean;
  /** Los secretos no se piden en este formulario. */
  secret?: boolean;
  default?: unknown;
  options?: string[];
}

export type InfraModuleKind = "cluster" | "vm";

export interface InfraModule {
  provider: string;
  kind: InfraModuleKind | string;
  module?: string;
  description?: string;
  available: boolean;
  inputs?: InfraModuleInput[];
}

export interface InfraModulesResponse {
  items: InfraModule[];
  /** false si el servidor no tiene OpenTofu o la clave de estado configurados. */
  runner_ready?: boolean;
  runner_error?: string;
}

export interface CreateInfraClusterRequest {
  cloud_account_id: string;
  name: string;
  env: ClusterEnv;
  /** Módulo elegido (por ejemplo digitalocean/droplet-k3s). */
  module: string;
  /** Valores de los inputs del módulo. */
  inputs: Record<string, unknown>;
  // Campos históricos del contrato: se repiten desde inputs cuando el módulo los declara.
  kubernetes_version?: string;
  domain?: string;
  ingress_class?: string;
  admin_principal_arns?: string[];
}

// Secretos de aplicación por scope
export interface ScopeSecretsResponse {
  keys: string[];
}

export interface PutScopeSecretsRequest {
  values: Record<string, string>;
}

export interface VersionInfo {
  version: string;
  protocol: number;
}
