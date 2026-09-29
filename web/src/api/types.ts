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
}

export interface OrgSettings {
  require_approval_prod: boolean;
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

export type ProviderKind = "aws" | "gcp" | "azure";

export interface Provider {
  kind: ProviderKind;
  display_name: string;
  available: boolean;
}

export interface InstructionField {
  name: string;
  label: string;
  placeholder?: string;
  help?: string;
  required: boolean;
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
  provider: ProviderKind;
  name: string;
  region: string;
  external_ref: string;
  settings: Record<string, string>;
  status: CloudAccountStatus;
  verification: Verification | null;
  instructions: Instructions | null;
  created_at: string;
}

export interface CreateCloudAccountRequest {
  provider: ProviderKind;
  name: string;
  region: string;
}

export interface PatchCloudAccountRequest {
  external_ref?: string;
  settings?: Record<string, string>;
}

// Clusters

export type ClusterEnv = "nonprod" | "prod";
export type ClusterStatus = "offline" | "online";

export interface Cluster {
  id: string;
  name: string;
  env: ClusterEnv;
  cloud_account_id: string;
  domain: string;
  ingress_class: string;
  secret_store: string;
  status: ClusterStatus;
  agent_version: string;
  kubernetes_version: string;
  last_seen_at: string | null;
  created_at: string;
}

// Respuesta del POST y del rotate-token: el token y el comando de instalación
// se muestran una sola vez.
export interface ClusterWithToken extends Cluster {
  token: string;
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

export interface Scope {
  name: string;
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
  "application.imported",
  "application.updated",
  "application.deleted",
  "scope.bound",
  "scope.delete_requested",
  "scope.progress",
  "scope.deleted",
  "scope.failed",
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

export interface VersionInfo {
  version: string;
  protocol: number;
}
