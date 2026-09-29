import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ArrowLeft, Clock, Wallet } from "lucide-react";
import { useCloudAccounts, useCreateInfraCluster, useInfraModules } from "../api/hooks";
import { isApiError } from "../api/client";
import { useOrgRole } from "../auth/AuthContext";
import { PageHeader } from "../components/ui/PageHeader";
import { Card, CardBody, CardHeader } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Input, Textarea } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { Field } from "../components/ui/Field";
import { Badge } from "../components/ui/Badge";
import { ErrorState, InlineError, LoadingState, Notice } from "../components/ui/States";
import {
  findInput,
  formValuesToRequest,
  inputsToFormValues,
  providerNames,
  splitClusterModules,
  validateClusterForm,
  type ClusterFormValues,
} from "../lib/infra";
import type { ClusterEnv } from "../api/types";

export function NewClusterPage() {
  const { org = "" } = useParams();
  const modulesQ = useInfraModules(org);
  const accountsQ = useCloudAccounts(org);

  return (
    <>
      <Link to={`/o/${org}/infra`} className="mb-3 inline-flex items-center gap-1 text-xs font-medium text-slate-500 hover:text-slate-900">
        <ArrowLeft className="size-3.5" /> Infraestructura
      </Link>
      <PageHeader title="Crear cluster con un click" description="Rampa crea la red, el cluster de Kubernetes y instala el agente en tu cuenta de nube." />
      {modulesQ.isPending || accountsQ.isPending ? (
        <Card>
          <LoadingState />
        </Card>
      ) : modulesQ.isError ? (
        <Card>
          <ErrorState
            title={isApiError(modulesQ.error) && modulesQ.error.status === 501 ? "La creación de infraestructura no está habilitada" : "No se pudieron cargar los módulos"}
            error={modulesQ.error}
            onRetry={() => void modulesQ.refetch()}
          />
        </Card>
      ) : accountsQ.isError ? (
        <Card>
          <ErrorState error={accountsQ.error} onRetry={() => void accountsQ.refetch()} />
        </Card>
      ) : (
        <ClusterForm org={org} modules={modulesQ.data.items} runnerReady={modulesQ.data.runner_ready !== false} accounts={accountsQ.data} />
      )}
    </>
  );
}

function ClusterForm({
  org,
  modules,
  runnerReady,
  accounts,
}: {
  org: string;
  modules: import("../api/types").InfraModule[];
  runnerReady: boolean;
  accounts: import("../api/types").CloudAccount[];
}) {
  const navigate = useNavigate();
  const { isAdmin } = useOrgRole(org);
  const create = useCreateInfraCluster(org);
  const { available, upcoming } = useMemo(() => splitClusterModules(modules), [modules]);
  const connected = accounts.filter((a) => a.status === "connected");

  const [moduleName, setModuleName] = useState(available[0]?.module ?? "");
  const module = available.find((m) => m.module === moduleName) ?? available[0];
  const [values, setValues] = useState<ClusterFormValues>(() => inputsToFormValues(module?.inputs));
  const [touched, setTouched] = useState(false);

  // Al cambiar de módulo se recalculan los defaults, conservando lo que ya escribió el usuario.
  useEffect(() => {
    const d = inputsToFormValues(module?.inputs);
    setValues((v) => ({ ...v, kubernetes_version: d.kubernetes_version, ingress_class: v.ingress_class || d.ingress_class }));
  }, [module]);

  const accountsForModule = connected.filter((a) => !module || a.provider === module.provider);
  const errors = validateClusterForm(values);
  const patch = (p: Partial<ClusterFormValues>) => setValues((v) => ({ ...v, ...p }));
  const versionInput = findInput(module?.inputs, "kubernetes_version");
  const blocked = !runnerReady || !module || accountsForModule.length === 0 || !isAdmin;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (blocked || Object.keys(errors).length > 0) return;
    create.mutate(formValuesToRequest(values), { onSuccess: (run) => navigate(`/o/${org}/infra/runs/${run.id}`) });
  };
  const err = (k: keyof ClusterFormValues) => (touched ? errors[k] : undefined);

  return (
    <div className="grid gap-6 lg:grid-cols-3">
      <form onSubmit={submit} className="space-y-5 lg:col-span-2" noValidate>
        {!runnerReady && (
          <Notice tone="amber">
            <strong>Este servidor todavía no puede crear infraestructura.</strong> Falta OpenTofu (<span className="font-mono">RAMPA_TOFU_BIN</span>) o la clave del estado (<span className="font-mono">RAMPA_STATE_KEY</span>). Pedile a quien administra Rampa que las configure y volvé a intentar.
          </Notice>
        )}
        {!isAdmin && <Notice tone="amber">Crear un cluster requiere rol admin.</Notice>}

        <Card>
          <CardHeader title="Cluster" />
          <CardBody className="space-y-4">
            {available.length > 1 && (
              <Field label="Proveedor">
                {(id) => (
                  <Select
                    id={id}
                    value={module?.module ?? ""}
                    options={available.map((m) => ({ value: m.module ?? "", label: providerNames[m.provider] ?? m.provider }))}
                    onChange={(e) => setModuleName(e.target.value)}
                  />
                )}
              </Field>
            )}
            <Field
              label="Cuenta de nube"
              required
              error={err("cloud_account_id")}
              help={accountsForModule.length === 0 ? "No hay cuentas conectadas para este proveedor. Conectá una y verificala primero." : "Solo aparecen cuentas conectadas."}
            >
              {(id) => (
                <Select
                  id={id}
                  value={values.cloud_account_id}
                  placeholder="Elegí una cuenta"
                  options={accountsForModule.map((a) => ({ value: a.id, label: `${a.name} (${a.region})` }))}
                  onChange={(e) => patch({ cloud_account_id: e.target.value })}
                />
              )}
            </Field>
            {accountsForModule.length === 0 && (
              <Link to={`/o/${org}/cloud-accounts?connect=1`} className="inline-block text-sm font-medium text-slate-900 underline">
                Conectar una cuenta de nube
              </Link>
            )}
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Nombre" required error={err("name")} help="Por ejemplo rampa-nonprod. También nombra los recursos en la nube.">
                {(id) => <Input id={id} value={values.name} aria-invalid={err("name") !== undefined} placeholder="rampa-nonprod" onChange={(e) => patch({ name: e.target.value })} />}
              </Field>
              <Field label="Versión de Kubernetes" error={err("kubernetes_version")}>
                {(id) =>
                  versionInput?.options && versionInput.options.length > 0 ? (
                    <Select id={id} value={values.kubernetes_version} options={versionInput.options.map((o) => ({ value: o, label: o }))} onChange={(e) => patch({ kubernetes_version: e.target.value })} />
                  ) : (
                    <Input id={id} value={values.kubernetes_version} onChange={(e) => patch({ kubernetes_version: e.target.value })} />
                  )
                }
              </Field>
            </div>
            <Field
              label="Entorno"
              help={values.env === "prod" ? "Aloja scopes prod. Los scopes prod solo pueden vivir en un cluster prod." : "Aloja dev, staging y los scopes efímeros de pull requests."}
            >
              {(id) => (
                <Select
                  id={id}
                  value={values.env}
                  options={[
                    { value: "nonprod", label: "nonprod: dev, staging y pull requests" },
                    { value: "prod", label: "prod: producción" },
                  ]}
                  onChange={(e) => patch({ env: e.target.value as ClusterEnv })}
                />
              )}
            </Field>
          </CardBody>
        </Card>

        <Card>
          <CardHeader title="Red y acceso" />
          <CardBody className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Dominio de los scopes" required error={err("domain")} help="Las apps sin dominio propio quedan en <app>-<scope>.<este dominio>.">
                {(id) => <Input id={id} value={values.domain} aria-invalid={err("domain") !== undefined} placeholder="apps.dev.acme.com" onChange={(e) => patch({ domain: e.target.value })} />}
              </Field>
              <Field label="Ingress class" help="Controlador de ingress que atiende ese dominio.">
                {(id) => <Input id={id} value={values.ingress_class} placeholder="nginx" onChange={(e) => patch({ ingress_class: e.target.value })} />}
              </Field>
            </div>
            <Field
              label="Administradores del cluster (opcional)"
              error={err("admin_arns")}
              help="ARNs de usuarios o roles de IAM que van a poder usar kubectl en el cluster. Uno por línea. Ejemplo: arn:aws:iam::123456789012:user/juan. Sin esto solo el rol de Rampa tiene acceso."
            >
              {(id) => <Textarea id={id} rows={3} className="font-mono text-xs" value={values.admin_arns} placeholder="arn:aws:iam::123456789012:user/juan" onChange={(e) => patch({ admin_arns: e.target.value })} />}
            </Field>
          </CardBody>
        </Card>

        {create.isError && <InlineError error={create.error} />}
        <div className="flex justify-end gap-2">
          <Link to={`/o/${org}/infra`} className="inline-flex h-9 items-center rounded-md border border-slate-300 bg-white px-3.5 text-sm font-medium text-slate-800 hover:bg-slate-50">
            Cancelar
          </Link>
          <Button type="submit" disabled={blocked} loading={create.isPending}>
            Crear cluster
          </Button>
        </div>
      </form>

      <div className="space-y-4">
        <Notice tone="amber">
          <div className="flex items-start gap-2">
            <Wallet className="mt-0.5 size-4 shrink-0" aria-hidden />
            <div>
              <strong>Tiene costo en tu cuenta de nube.</strong> El plano de control de EKS y el NAT gateway suman unos USD 100 al mes, más los nodos que se levanten para tus apps. Pagás a AWS, no a Rampa.
            </div>
          </div>
        </Notice>
        <Notice tone="blue">
          <div className="flex items-start gap-2">
            <Clock className="mt-0.5 size-4 shrink-0" aria-hidden />
            <div>
              <strong>Tarda de 15 a 25 minutos.</strong> Podés cerrar la página: la ejecución sigue y el log queda guardado.
            </div>
          </div>
        </Notice>
        <Card>
          <CardHeader title="Módulos" />
          <CardBody className="space-y-2 text-sm">
            {available.map((m) => (
              <div key={m.module ?? m.provider} className="flex items-center justify-between">
                <span>
                  {providerNames[m.provider] ?? m.provider} <span className="font-mono text-xs text-slate-500">{m.module}</span>
                </span>
                <Badge tone="green">Disponible</Badge>
              </div>
            ))}
            {available.length === 0 && <p className="text-xs text-slate-500">Ningún módulo de cluster está disponible todavía.</p>}
            {upcoming.map((m) => (
              <div key={m.module ?? m.provider} className="flex items-center justify-between text-slate-500">
                <span>{providerNames[m.provider] ?? m.provider}</span>
                <Badge tone="gray">Próximamente</Badge>
              </div>
            ))}
          </CardBody>
        </Card>
      </div>
    </div>
  );
}
