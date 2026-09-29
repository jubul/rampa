// Hooks de TanStack Query por recurso. Las claves están centralizadas en
// `keys` para poder invalidar desde las mutaciones y desde el stream SSE.

import { keepPreviousData, useMutation, useQueries, useQuery, useQueryClient, type UseQueryOptions } from "@tanstack/react-query";
import * as api from "./client";
import { isRunActive } from "../lib/infra";
import type {
  AddMemberRequest,
  Application,
  ApplicationSpec,
  CreateInfraClusterRequest,
  FromSpecRequest,
  CreateCloudAccountRequest,
  CreateClusterRequest,
  CreateDeploymentRequest,
  CreateOrgRequest,
  CreateTokenRequest,
  Deployment,
  EventsFilter,
  ImportAppRequest,
  ListResponse,
  Me,
  PatchCloudAccountRequest,
  PatchOrgRequest,
  Scope,
} from "./types";

export const keys = {
  me: ["me"] as const,
  orgs: ["orgs"] as const,
  org: (org: string) => ["org", org] as const,
  members: (org: string) => ["org", org, "members"] as const,
  tokens: (org: string) => ["org", org, "tokens"] as const,
  providers: (org: string) => ["org", org, "providers"] as const,
  cloudAccounts: (org: string) => ["org", org, "cloud-accounts"] as const,
  clusters: (org: string) => ["org", org, "clusters"] as const,
  apps: (org: string) => ["org", org, "apps"] as const,
  app: (org: string, app: string) => ["org", org, "apps", app] as const,
  scopes: (org: string, app: string) => ["org", org, "apps", app, "scopes"] as const,
  deployments: (org: string, app: string, scope: string) =>
    ["org", org, "apps", app, "scopes", scope, "deployments"] as const,
  deployment: (org: string, id: string) => ["org", org, "deployments", id] as const,
  appSpec: (org: string, app: string) => ["org", org, "apps", app, "spec"] as const,
  onboarding: (org: string) => ["org", org, "onboarding"] as const,
  infraModules: (org: string) => ["org", org, "infra", "modules"] as const,
  infraRuns: (org: string) => ["org", org, "infra", "runs"] as const,
  infraRun: (org: string, id: string) => ["org", org, "infra", "runs", id] as const,
  events: (org: string, filter: EventsFilter) => ["org", org, "events", filter] as const,
};

// Auth

export function useMe(options?: Partial<UseQueryOptions<Me, Error>>) {
  return useQuery<Me, Error>({
    queryKey: keys.me,
    queryFn: ({ signal }) => api.auth.me(signal),
    retry: false,
    staleTime: 60_000,
    ...options,
  });
}

export function useDevLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (email: string) => api.auth.devLogin(email),
    onSuccess: (me) => {
      qc.setQueryData(keys.me, me);
    },
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.auth.logout(),
    onSettled: () => {
      qc.clear();
    },
  });
}

// Organizaciones

export function useOrg(org: string) {
  return useQuery({
    queryKey: keys.org(org),
    queryFn: ({ signal }) => api.orgs.get(org, signal),
    enabled: org !== "",
  });
}

export function useCreateOrg() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateOrgRequest) => api.orgs.create(body),
    onSuccess: async () => {
      await Promise.all([qc.invalidateQueries({ queryKey: keys.me }), qc.invalidateQueries({ queryKey: keys.orgs })]);
    },
  });
}

export function useUpdateOrg(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: PatchOrgRequest) => api.orgs.patch(org, body),
    onSuccess: (updated) => {
      qc.setQueryData(keys.org(org), updated);
      void qc.invalidateQueries({ queryKey: keys.me });
      void qc.invalidateQueries({ queryKey: keys.orgs });
    },
  });
}

export function useMembers(org: string) {
  return useQuery({
    queryKey: keys.members(org),
    queryFn: ({ signal }) => api.members.list(org, signal),
    select: (r) => r.items,
  });
}

export function useAddMember(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: AddMemberRequest) => api.members.add(org, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.members(org) }),
  });
}

export function useRemoveMember(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (userId: string) => api.members.remove(org, userId),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.members(org) }),
  });
}

export function useTokens(org: string, enabled = true) {
  return useQuery({
    enabled,
    queryKey: keys.tokens(org),
    queryFn: ({ signal }) => api.tokens.list(org, signal),
    select: (r) => r.items,
  });
}

export function useCreateToken(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateTokenRequest) => api.tokens.create(org, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.tokens(org) }),
  });
}

export function useRemoveToken(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.tokens.remove(org, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.tokens(org) }),
  });
}

// Providers y cuentas de nube

export function useProviders(org: string) {
  return useQuery({
    queryKey: keys.providers(org),
    queryFn: ({ signal }) => api.providers.list(org, signal),
    select: (r) => r.items,
    staleTime: 5 * 60_000,
  });
}

export function useCloudAccounts(org: string) {
  return useQuery({
    queryKey: keys.cloudAccounts(org),
    queryFn: ({ signal }) => api.cloudAccounts.list(org, signal),
    select: (r) => r.items,
  });
}

export function useCreateCloudAccount(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateCloudAccountRequest) => api.cloudAccounts.create(org, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.cloudAccounts(org) }),
  });
}

export function usePatchCloudAccount(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: PatchCloudAccountRequest }) => api.cloudAccounts.patch(org, id, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.cloudAccounts(org) }),
  });
}

export function useVerifyCloudAccount(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.cloudAccounts.verify(org, id),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.cloudAccounts(org) }),
  });
}

export function useRemoveCloudAccount(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.cloudAccounts.remove(org, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.cloudAccounts(org) }),
  });
}

// Clusters

export function useClusters(org: string) {
  return useQuery({
    queryKey: keys.clusters(org),
    queryFn: ({ signal }) => api.clusters.list(org, signal),
    select: (r) => r.items,
  });
}

export function useCreateCluster(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateClusterRequest) => api.clusters.create(org, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.clusters(org) }),
  });
}

export function useRotateClusterToken(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.clusters.rotateToken(org, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.clusters(org) }),
  });
}

export function useRemoveCluster(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.clusters.remove(org, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.clusters(org) }),
  });
}

// Applications

export function useApps(org: string) {
  return useQuery({
    queryKey: keys.apps(org),
    queryFn: ({ signal }) => api.apps.list(org, signal),
    select: (r) => r.items,
  });
}

export function useApp(org: string, app: string) {
  return useQuery({
    queryKey: keys.app(org, app),
    queryFn: ({ signal }) => api.apps.get(org, app, signal),
    enabled: app !== "",
  });
}

export function useImportApp(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ImportAppRequest) => api.apps.import(org, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.apps(org) }),
  });
}

export function useUpdateManifest(org: string, app: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (manifest: string) => api.apps.updateManifest(org, app, { manifest }),
    onSuccess: (updated: Application) => {
      qc.setQueryData(keys.app(org, app), updated);
      return Promise.all([
        qc.invalidateQueries({ queryKey: keys.apps(org) }),
        qc.invalidateQueries({ queryKey: keys.scopes(org, app) }),
      ]);
    },
  });
}

export function useRemoveApp(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (app: string) => api.apps.remove(org, app),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.apps(org) }),
  });
}

// Scopes

export function useScopes(org: string, app: string) {
  return useQuery({
    queryKey: keys.scopes(org, app),
    queryFn: ({ signal }) => api.scopes.list(org, app, signal),
    select: (r) => r.items,
    enabled: app !== "",
  });
}

/** Scopes de varias apps a la vez (dashboard). */
export function useScopesForApps(org: string, appNames: string[]) {
  return useQueries({
    queries: appNames.map((app) => ({
      queryKey: keys.scopes(org, app),
      queryFn: ({ signal }: { signal: AbortSignal }) => api.scopes.list(org, app, signal),
      select: (r: ListResponse<Scope>) => r.items,
    })),
    combine: (results) => ({
      data: results.flatMap((r, i) => (r.data ?? []).map((s) => ({ app: appNames[i] ?? "", scope: s }))),
      isPending: results.some((r) => r.isPending),
      isError: results.some((r) => r.isError),
      error: results.find((r) => r.error)?.error ?? null,
    }),
  });
}

export function useBindScope(org: string, app: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ scope, clusterId }: { scope: string; clusterId: string }) =>
      api.scopes.bind(org, app, scope, { cluster_id: clusterId }),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.scopes(org, app) }),
  });
}

export function useRemoveScope(org: string, app: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (scope: string) => api.scopes.remove(org, app, scope),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.scopes(org, app) }),
  });
}

// Deployments

export function useDeployments(org: string, app: string, scope: string, limit = 20) {
  return useQuery({
    queryKey: keys.deployments(org, app, scope),
    queryFn: ({ signal }) => api.deployments.list(org, app, scope, limit, signal),
    select: (r) => r.items,
    enabled: app !== "" && scope !== "",
  });
}

/** Historial combinado de todos los scopes de una app, del más nuevo al más viejo. */
export function useAppDeployments(org: string, app: string, scopeNames: string[], limitPerScope = 20) {
  return useQueries({
    queries: scopeNames.map((scope) => ({
      queryKey: keys.deployments(org, app, scope),
      queryFn: ({ signal }: { signal: AbortSignal }) => api.deployments.list(org, app, scope, limitPerScope, signal),
      select: (r: ListResponse<Deployment>) => r.items,
    })),
    combine: (results) => {
      const all = results.flatMap((r) => r.data ?? []);
      all.sort((a, b) => (a.created_at < b.created_at ? 1 : a.created_at > b.created_at ? -1 : 0));
      return {
        data: all,
        isPending: results.some((r) => r.isPending),
        isError: results.some((r) => r.isError),
        error: results.find((r) => r.error)?.error ?? null,
      };
    },
  });
}

export function useCreateDeployment(org: string, app: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ scope, body }: { scope: string; body: CreateDeploymentRequest }) =>
      api.deployments.create(org, app, scope, body),
    onSuccess: (_d, { scope }) =>
      Promise.all([
        qc.invalidateQueries({ queryKey: keys.deployments(org, app, scope) }),
        qc.invalidateQueries({ queryKey: keys.scopes(org, app) }),
      ]),
  });
}

export function useApproveDeployment(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deployments.approve(org, id),
    onSuccess: (d) => invalidateDeployment(qc, org, d),
  });
}

export function useCancelDeployment(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deployments.cancel(org, id),
    onSuccess: (d) => invalidateDeployment(qc, org, d),
  });
}

function invalidateDeployment(qc: ReturnType<typeof useQueryClient>, org: string, d: Deployment) {
  qc.setQueryData(keys.deployment(org, d.id), d);
  return Promise.all([
    qc.invalidateQueries({ queryKey: keys.deployments(org, d.app, d.scope) }),
    qc.invalidateQueries({ queryKey: keys.scopes(org, d.app) }),
  ]);
}

// Eventos

export function useEvents(org: string, filter: EventsFilter = {}) {
  return useQuery({
    queryKey: keys.events(org, filter),
    queryFn: ({ signal }) => api.events.list(org, filter, signal),
    select: (r) => r.items,
  });
}

// Applications sin YAML

/** Preview del manifest a partir del spec. La clave es el spec serializado, así que cambia solo con el contenido. */
export function useSpecPreview(org: string, spec: ApplicationSpec, enabled: boolean) {
  return useQuery({
    queryKey: ["org", org, "apps", "preview", spec] as const,
    queryFn: ({ signal }) => api.apps.preview(org, spec, signal),
    enabled,
    retry: false,
    staleTime: 30_000,
    placeholderData: keepPreviousData,
  });
}

export function useCreateFromSpec(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: FromSpecRequest) => api.apps.createFromSpec(org, body),
    onSuccess: () =>
      Promise.all([
        qc.invalidateQueries({ queryKey: keys.apps(org) }),
        qc.invalidateQueries({ queryKey: keys.onboarding(org) }),
      ]),
  });
}

export function useAppSpec(org: string, app: string) {
  return useQuery({
    queryKey: keys.appSpec(org, app),
    queryFn: ({ signal }) => api.apps.getSpec(org, app, signal),
    enabled: app !== "",
    // El wizard edita una copia local: no queremos que un refetch la pise.
    staleTime: Infinity,
    gcTime: 0,
  });
}

export function useUpdateSpec(org: string, app: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (spec: ApplicationSpec) => api.apps.updateSpec(org, app, spec),
    onSuccess: (updated: Application) => {
      qc.setQueryData(keys.app(org, app), updated);
      return Promise.all([
        qc.invalidateQueries({ queryKey: keys.apps(org) }),
        qc.invalidateQueries({ queryKey: keys.scopes(org, app) }),
      ]);
    },
  });
}

// Onboarding

export function useOnboarding(org: string) {
  return useQuery({
    queryKey: keys.onboarding(org),
    queryFn: ({ signal }) => api.onboarding.get(org, signal),
  });
}

// Infraestructura

export function useInfraModules(org: string) {
  return useQuery({
    queryKey: keys.infraModules(org),
    queryFn: ({ signal }) => api.infra.modules(org, signal),
    retry: false,
  });
}

export function useInfraRuns(org: string) {
  return useQuery({
    queryKey: keys.infraRuns(org),
    queryFn: ({ signal }) => api.infra.runs(org, 50, signal),
    select: (r) => r.items,
    refetchInterval: (q) => (q.state.data?.items.some((r) => isRunActive(r.status)) ? 5000 : false),
  });
}

export function useInfraRun(org: string, id: string) {
  return useQuery({
    queryKey: keys.infraRun(org, id),
    queryFn: ({ signal }) => api.infra.run(org, id, signal),
    enabled: id !== "",
    refetchInterval: (q) => (q.state.data && isRunActive(q.state.data.status) ? 4000 : false),
  });
}

export function useCreateInfraCluster(org: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateInfraClusterRequest) => api.infra.createCluster(org, body),
    onSuccess: () =>
      Promise.all([
        qc.invalidateQueries({ queryKey: keys.infraRuns(org) }),
        qc.invalidateQueries({ queryKey: keys.clusters(org) }),
        qc.invalidateQueries({ queryKey: keys.onboarding(org) }),
      ]),
  });
}

function useRunAction(org: string, fn: (org: string, id: string) => Promise<import("./types").InfraRun>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => fn(org, id),
    onSuccess: (run) => {
      qc.setQueryData(keys.infraRun(org, run.id), run);
      return Promise.all([
        qc.invalidateQueries({ queryKey: keys.infraRuns(org) }),
        qc.invalidateQueries({ queryKey: keys.clusters(org) }),
      ]);
    },
  });
}

export function useRetryInfraRun(org: string) {
  return useRunAction(org, api.infra.retry);
}

export function useDestroyInfraRun(org: string) {
  return useRunAction(org, api.infra.destroy);
}
