import { useMemo, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { ArrowLeft, Clock, Server, ServerCog, Wallet } from "lucide-react";
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
import { useTerms } from "../hooks/useTerms";
import {
  buildClusterRequest,
  costHint,
  initialInputValues,
  inputKind,
  sizeInputName,
  splitModules,
  validateClusterForm,
  visibleInputs,
} from "../lib/infra";
import { cap, termsFor } from "../lib/terminology";
import { cn } from "../lib/cn";
import type { CloudAccount, ClusterEnv, InfraModule, InfraModuleInput, Provider } from "../api/types";

type Kind = "cluster" | "vm";

export function NewClusterPage() {
  const { t } = useTranslation();
  const { org = "" } = useParams();
  const modulesQ = useInfraModules(org);
  const accountsQ = useCloudAccounts(org);
  const { providers } = useTerms(org);

  return (
    <>
      <Link to={`/o/${org}/infra`} className="mb-3 inline-flex items-center gap-1 text-xs font-medium text-slate-500 dark:text-slate-400 hover:text-slate-900 dark:hover:text-slate-100">
        <ArrowLeft className="size-3.5" /> {t("nav.infra")}
      </Link>
      <PageHeader title={t("newCluster.title")} description={t("newCluster.description")} />
      {modulesQ.isPending || accountsQ.isPending ? (
        <Card>
          <LoadingState />
        </Card>
      ) : modulesQ.isError ? (
        <Card>
          <ErrorState
            title={isApiError(modulesQ.error) && modulesQ.error.status === 501 ? t("newCluster.disabled") : t("newCluster.modulesError")}
            error={modulesQ.error}
            onRetry={() => void modulesQ.refetch()}
          />
        </Card>
      ) : accountsQ.isError ? (
        <Card>
          <ErrorState error={accountsQ.error} onRetry={() => void accountsQ.refetch()} />
        </Card>
      ) : (
        <NewClusterForm
          org={org}
          modules={modulesQ.data.items}
          runnerReady={modulesQ.data.runner_ready !== false}
          runnerError={modulesQ.data.runner_error}
          accounts={accountsQ.data}
          providers={providers ?? []}
        />
      )}
    </>
  );
}

function NewClusterForm({
  org,
  modules,
  runnerReady,
  runnerError,
  accounts,
  providers,
}: {
  org: string;
  modules: InfraModule[];
  runnerReady: boolean;
  runnerError?: string;
  accounts: CloudAccount[];
  providers: Provider[];
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const { isAdmin } = useOrgRole(org);
  const create = useCreateInfraCluster(org);

  const [kind, setKind] = useState<Kind>(searchParams.get("kind") === "vm" ? "vm" : "cluster");
  const [accountId, setAccountId] = useState("");
  const [moduleName, setModuleName] = useState("");
  const [name, setName] = useState("");
  const [env, setEnv] = useState<ClusterEnv>("nonprod");
  const [valuesByModule, setValuesByModule] = useState<Record<string, Record<string, string>>>({});
  const [touched, setTouched] = useState(false);

  const providerOf = (kindName: string) => providers.find((p) => p.kind === kindName);
  const providerName = (kindName: string) => providerOf(kindName)?.display_name ?? kindName;

  const connected = accounts.filter((a) => a.status === "connected");
  const { available, upcoming } = useMemo(() => splitModules(modules, kind), [modules, kind]);
  // Solo cuentas cuyo proveedor tiene un módulo disponible de este tipo.
  const eligible = connected.filter((a) => available.some((m) => m.provider === a.provider));
  const account = eligible.find((a) => a.id === accountId);
  const provider = account ? providerOf(account.provider) : undefined;
  const terms = termsFor(provider);
  const providerModules = account ? available.filter((m) => m.provider === account.provider) : [];
  const module = providerModules.find((m) => m.module === moduleName) ?? providerModules[0];
  const moduleKey = module?.module ?? "";
  const values = valuesByModule[moduleKey] ?? initialInputValues(module?.inputs);
  const setValue = (field: string, v: string) =>
    setValuesByModule((cur) => ({ ...cur, [moduleKey]: { ...(cur[moduleKey] ?? initialInputValues(module?.inputs)), [field]: v } }));

  const base = { cloud_account_id: account?.id ?? "", name, env };
  const errors = validateClusterForm(base, module?.inputs, values);
  const blocked = !runnerReady || !module || !isAdmin;

  const changeKind = (k: Kind) => {
    setKind(k);
    setAccountId("");
    setModuleName("");
    setTouched(false);
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (blocked || !module || Object.keys(errors).length > 0) return;
    create.mutate(buildClusterRequest(base, module, values), { onSuccess: (run) => navigate(`/o/${org}/infra/runs/${run.id}`) });
  };
  const err = (k: string) => (touched ? errors[k] : undefined);

  const submitLabel = kind === "vm" ? t("newCluster.createVm", { vm: terms.vm }) : t("newCluster.createCluster", { cluster: terms.cluster });
  const sizeName = sizeInputName(module?.inputs);
  const cost = kind === "vm" && module && sizeName ? costHint(module, { input: sizeName, value: values[sizeName] ?? "" }) : null;

  return (
    <div className="grid gap-6 lg:grid-cols-3">
      <form onSubmit={submit} className="space-y-5 lg:col-span-2" noValidate>
        {!runnerReady && (
          <Notice tone="amber">
            <strong>{t("newCluster.runnerNotReady")}</strong> {t("newCluster.runnerNotReadyBody")}
            {runnerError && <span className="mt-1 block font-mono text-xs">{runnerError}</span>}
          </Notice>
        )}
        {!isAdmin && <Notice tone="amber">{t("newCluster.needAdmin")}</Notice>}

        <Card>
          <CardHeader title={t("newCluster.whatToCreate")} />
          <CardBody>
            <div role="radiogroup" aria-label={t("newCluster.whatToCreate")} className="grid gap-3 sm:grid-cols-2">
              <KindOption
                active={kind === "cluster"}
                icon={<ServerCog className="size-5" aria-hidden />}
                title={t("newCluster.kindCluster")}
                description={t("newCluster.kindClusterDescription")}
                onClick={() => changeKind("cluster")}
              />
              <KindOption
                active={kind === "vm"}
                icon={<Server className="size-5" aria-hidden />}
                title={t("newCluster.kindVm")}
                description={t("newCluster.kindVmDescription")}
                onClick={() => changeKind("vm")}
              />
            </div>
            {kind === "vm" && (
              <div className="mt-4">
                <Notice tone="amber">
                  <strong>{t("newCluster.vmWarningTitle")}</strong> {t("newCluster.vmWarningBody")}
                </Notice>
              </div>
            )}
          </CardBody>
        </Card>

        <Card>
          <CardHeader title={kind === "vm" ? cap(terms.vm) : cap(terms.cluster)} />
          <CardBody className="space-y-4">
            <Field
              label={cap(terms.account)}
              required
              error={err("cloud_account_id")}
              help={eligible.length === 0 ? t("newCluster.noEligible", { account: terms.account }) : t("newCluster.onlyConnected")}
            >
              {(id) => (
                <Select
                  id={id}
                  value={accountId}
                  placeholder={t("newCluster.pickAccount")}
                  options={eligible.map((a) => ({ value: a.id, label: `${a.name} (${providerName(a.provider)}, ${a.region})` }))}
                  onChange={(e) => {
                    setAccountId(e.target.value);
                    setModuleName("");
                  }}
                />
              )}
            </Field>
            {eligible.length === 0 && (
              <Link to={`/o/${org}/cloud-accounts?connect=1`} className="inline-block text-sm font-medium text-slate-900 underline dark:text-slate-100">
                {t("newCluster.connectAccount")}
              </Link>
            )}
            {providerModules.length > 1 && (
              <Field label={t("newCluster.module")}>
                {(id) => <Select id={id} value={moduleKey} options={providerModules.map((m) => ({ value: m.module ?? "", label: m.module ?? m.provider }))} onChange={(e) => setModuleName(e.target.value)} />}
              </Field>
            )}
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label={t("newCluster.name")} required error={err("name")} help={t("newCluster.nameHelp", { example: kind === "vm" ? "rampa-vm" : "rampa-nonprod" })}>
                {(id) => <Input id={id} value={name} aria-invalid={err("name") !== undefined} placeholder={kind === "vm" ? "rampa-vm" : "rampa-nonprod"} onChange={(e) => setName(e.target.value)} />}
              </Field>
              <Field label={t("newCluster.env")} help={env === "prod" ? t("newCluster.envProdHelp") : t("newCluster.envNonprodHelp")}>
                {(id) => (
                  <Select
                    id={id}
                    value={env}
                    options={[
                      { value: "nonprod", label: t("newCluster.envNonprod") },
                      { value: "prod", label: t("newCluster.envProd") },
                    ]}
                    onChange={(e) => setEnv(e.target.value as ClusterEnv)}
                  />
                )}
              </Field>
            </div>
          </CardBody>
        </Card>

        {module && visibleInputs(module.inputs).length > 0 && (
          <Card>
            <CardHeader title={t("newCluster.settings")} description={module.module} />
            <CardBody className="grid gap-4 sm:grid-cols-2">
              {visibleInputs(module.inputs).map((input) => (
                <InputField key={`${moduleKey}:${input.name}`} input={input} value={values[input.name] ?? ""} error={err(input.name)} onChange={(v) => setValue(input.name, v)} />
              ))}
            </CardBody>
          </Card>
        )}

        {create.isError && <InlineError error={create.error} />}
        <div className="flex justify-end gap-2">
          <Link to={`/o/${org}/infra`} className="inline-flex h-9 items-center rounded-md border border-slate-300 bg-white px-3.5 text-sm font-medium text-slate-800 hover:bg-slate-50 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100 dark:hover:bg-slate-800">
            {t("common.cancel")}
          </Link>
          <Button type="submit" disabled={blocked} loading={create.isPending}>
            {submitLabel}
          </Button>
        </div>
      </form>

      <div className="space-y-4">
        <Notice tone="amber">
          <div className="flex items-start gap-2">
            <Wallet className="mt-0.5 size-4 shrink-0" aria-hidden />
            <div>
              <strong>{t("newCluster.costTitle")}</strong>{" "}
              {kind === "vm"
                ? cost
                  ? t("newCluster.costVmChosen", { cost })
                  : t("newCluster.costVm", { vm: terms.vm })
                : account?.provider === "aws"
                  ? t("newCluster.costClusterAws")
                  : t("newCluster.costCluster", { cluster: terms.cluster })}{" "}
              {t("newCluster.costPayProvider", { provider: provider?.display_name ?? t("newCluster.yourProvider") })}
            </div>
          </div>
        </Notice>
        <Notice tone="blue">
          <div className="flex items-start gap-2">
            <Clock className="mt-0.5 size-4 shrink-0" aria-hidden />
            <div>
              <strong>{kind === "vm" ? t("newCluster.timeVm") : t("newCluster.timeCluster")}</strong> {t("newCluster.timeBody")}
            </div>
          </div>
        </Notice>
        <Card>
          <CardHeader title={t("newCluster.modules")} />
          <CardBody className="space-y-2 text-sm">
            {available.map((m) => (
              <div key={m.module ?? m.provider} className="flex items-center justify-between gap-2">
                <span>
                  {providerName(m.provider)} <span className="font-mono text-xs text-slate-500 dark:text-slate-400">{m.module}</span>
                </span>
                <Badge tone="green">{t("newCluster.available")}</Badge>
              </div>
            ))}
            {available.length === 0 && <p className="text-xs text-slate-500 dark:text-slate-400">{t("newCluster.noModules")}</p>}
            {upcoming.map((m) => {
              const pt = termsFor(providerOf(m.provider));
              return (
                <div key={`${m.provider}:${m.module ?? ""}`} className="flex items-center justify-between gap-2 text-slate-500 dark:text-slate-400">
                  <span>
                    {providerName(m.provider)} <span className="text-xs">({kind === "vm" ? pt.vm : pt.cluster})</span>
                  </span>
                  <Badge tone="gray">{t("newCluster.comingSoon")}</Badge>
                </div>
              );
            })}
          </CardBody>
        </Card>
      </div>
    </div>
  );
}

function KindOption({ active, icon, title, description, onClick }: { active: boolean; icon: React.ReactNode; title: string; description: string; onClick: () => void }) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={active}
      onClick={onClick}
      className={cn(
        "flex items-start gap-3 rounded-md border px-4 py-3 text-left",
        active
          ? "border-slate-900 ring-1 ring-slate-900 dark:border-slate-200 dark:ring-slate-200"
          : "border-slate-200 hover:border-slate-300 dark:border-slate-700 dark:hover:border-slate-500",
      )}
    >
      <span className="mt-0.5 text-slate-600 dark:text-slate-300">{icon}</span>
      <span>
        <span className="block text-sm font-medium text-slate-900 dark:text-slate-100">{title}</span>
        <span className="mt-0.5 block text-xs text-slate-500 dark:text-slate-400">{description}</span>
      </span>
    </button>
  );
}

/** Un input del módulo, con el control que corresponde a su tipo (opciones, número, booleano, lista o texto). */
function InputField({ input, value, error, onChange }: { input: InfraModuleInput; value: string; error?: string; onChange: (v: string) => void }) {
  const { t } = useTranslation();
  const kind = inputKind(input);
  const label = input.label || input.name;
  const help = [input.help, input.description].filter(Boolean).join(" ") || undefined;
  const wide = kind === "list" || (input.description ?? "").length > 80;
  return (
    <div className={wide ? "sm:col-span-2" : undefined}>
      <Field label={label} required={input.required} error={error} help={kind === "list" ? [help, t("newCluster.onePerLine")].filter(Boolean).join(" ") : help}>
        {(id) => {
          switch (kind) {
            case "select":
              return <Select id={id} value={value} options={(input.options ?? []).map((o) => ({ value: o, label: o }))} onChange={(e) => onChange(e.target.value)} />;
            case "bool":
              return (
                <Select
                  id={id}
                  value={value === "true" ? "true" : "false"}
                  options={[
                    { value: "true", label: t("common.yes") },
                    { value: "false", label: t("common.no") },
                  ]}
                  onChange={(e) => onChange(e.target.value)}
                />
              );
            case "number":
              return <Input id={id} type="number" value={value} aria-invalid={error !== undefined} onChange={(e) => onChange(e.target.value)} />;
            case "list":
              return <Textarea id={id} rows={3} className="font-mono text-xs" value={value} aria-invalid={error !== undefined} onChange={(e) => onChange(e.target.value)} />;
            default:
              return <Input id={id} value={value} aria-invalid={error !== undefined} placeholder={input.name} onChange={(e) => onChange(e.target.value)} />;
          }
        }}
      </Field>
    </div>
  );
}
