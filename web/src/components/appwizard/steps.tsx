import type { ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Plus, Trash2 } from "lucide-react";
import type { ApplicationSpec, SpecParameter, SpecScope, SpecService } from "../../api/types";
import { Card, CardBody } from "../ui/Card";
import { useTerms } from "../../hooks/useTerms";
import { Button } from "../ui/Button";
import { Input, Textarea } from "../ui/Input";
import { Select } from "../ui/Select";
import { Field } from "../ui/Field";
import { Chip } from "../ui/Chip";
import { Table, TBody, TD, TH, THead, TR } from "../ui/Table";
import {
  APP_TYPES,
  PERMANENT_SCOPES,
  RUNTIMES,
  SCOPE_ORDER,
  SERVICE_TYPES,
  SIZES,
  STRATEGIES,
  TIERS,
  defaultParameter,
  defaultScope,
  defaultService,
  hasPort,
  toggleScope,
  validateAppName,
  validateParamName,
} from "../../lib/spec";

export type SetSpec = (updater: (s: ApplicationSpec) => ApplicationSpec) => void;

interface StepProps {
  spec: ApplicationSpec;
  setSpec: SetSpec;
  org: string;
}

const checkboxCls = "size-4 rounded border-slate-300 dark:border-slate-700";

function toInt(v: string): number {
  const n = parseInt(v, 10);
  return Number.isFinite(n) ? n : 0;
}

function StepIntro({ children }: { children: ReactNode }) {
  return <p className="mb-4 text-sm text-slate-600 dark:text-slate-300">{children}</p>;
}

// Paso 1: básicos

export function BasicsStep({ spec, setSpec, lockName }: StepProps & { lockName: boolean }) {
  const { t } = useTranslation();
  const nameError = spec.name === "" ? undefined : (validateAppName(spec.name) ?? undefined);
  const tier = TIERS.find((x) => x.value === spec.tier);
  const type = APP_TYPES.find((x) => x.value === spec.type);
  const patch = (p: Partial<ApplicationSpec>) => setSpec((s) => ({ ...s, ...p }));

  return (
    <Card>
      <CardBody className="space-y-5">
        <StepIntro>{t("wizard.basics.intro")}</StepIntro>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field
            label={t("apps.colName")}
            required
            error={nameError}
            help={lockName ? t("wizard.basics.nameLocked") : t("wizard.basics.nameHelp")}
          >
            {(id) => (
              <Input
                id={id}
                value={spec.name}
                disabled={lockName}
                aria-invalid={nameError !== undefined}
                placeholder="checkout-api"
                onChange={(e) => patch({ name: e.target.value })}
              />
            )}
          </Field>
          <Field label={t("apps.colTeam")} required help={t("wizard.basics.teamHelp")}>
            {(id) => <Input id={id} value={spec.team} placeholder="payments" onChange={(e) => patch({ team: e.target.value })} />}
          </Field>
          <Field label="Tier" help={tier ? t(`tiers.${tier.value}.help`) : undefined}>
            {(id) => (
              <Select
                id={id}
                value={String(spec.tier)}
                options={TIERS.map((x) => ({ value: String(x.value), label: t(`tiers.${x.value}.label`) }))}
                onChange={(e) => patch({ tier: toInt(e.target.value) })}
              />
            )}
          </Field>
          <Field label="Runtime" help={t("wizard.basics.runtimeHelp")}>
            {(id) => (
              <Select id={id} value={spec.runtime} options={RUNTIMES.map((r) => ({ value: r, label: r }))} onChange={(e) => patch({ runtime: e.target.value })} />
            )}
          </Field>
        </div>
        <Field label={t("wizard.basics.description")}>
          {(id) => <Textarea id={id} rows={2} value={spec.description} placeholder={t("wizard.basics.descriptionPlaceholder")} onChange={(e) => patch({ description: e.target.value })} />}
        </Field>

        <div>
          <div className="mb-2 text-sm font-medium text-slate-700 dark:text-slate-200">{t("apps.colType")}</div>
          <div className="grid gap-2 sm:grid-cols-4" role="radiogroup" aria-label={t("wizard.basics.appType")}>
            {APP_TYPES.map((x) => (
              <button
                key={x.value}
                type="button"
                role="radio"
                aria-checked={spec.type === x.value}
                onClick={() => patch({ type: x.value })}
                className={`rounded-md border p-3 text-left ${spec.type === x.value ? "border-slate-900 bg-slate-50 ring-1 ring-slate-900 dark:border-slate-200 dark:bg-slate-800 dark:ring-slate-200" : "border-slate-300 hover:bg-slate-50 dark:border-slate-700 dark:hover:bg-slate-800"}`}
              >
                <div className="text-sm font-medium text-slate-900 dark:text-slate-100">{t(`appTypes.${x.value}.label`)}</div>
                <div className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">{t(`appTypes.${x.value}.help`)}</div>
              </button>
            ))}
          </div>
          {type && <span className="sr-only">{t(`appTypes.${type.value}.help`)}</span>}
        </div>

        {hasPort(spec.type) && (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t("wizard.basics.port")} help={t("wizard.basics.portHelp")}>
              {(id) => <Input id={id} type="number" min={1} max={65535} value={spec.port || ""} onChange={(e) => patch({ port: toInt(e.target.value) })} />}
            </Field>
            {spec.type === "api" && (
              <Field label="Healthcheck" help={t("wizard.basics.healthcheckHelp")}>
                {(id) => <Input id={id} value={spec.healthcheck} placeholder="/health" onChange={(e) => patch({ healthcheck: e.target.value })} />}
              </Field>
            )}
          </div>
        )}

        {spec.type === "cron" && (
          <Field
            label="Schedule"
            required
            help={t("wizard.basics.scheduleHelp")}
          >
            {(id) => <Input id={id} className="font-mono" value={spec.schedule} placeholder="*/5 * * * *" onChange={(e) => patch({ schedule: e.target.value })} />}
          </Field>
        )}
      </CardBody>
    </Card>
  );
}

// Paso 2: scopes

export function ScopesStep({ spec, setSpec }: StepProps) {
  const { t } = useTranslation();
  const update = (name: string, p: Partial<SpecScope>) =>
    setSpec((s) => ({ ...s, scopes: s.scopes.map((sc) => (sc.name === name ? { ...sc, ...p } : sc)) }));

  return (
    <Card>
      <CardBody>
        <StepIntro>
          <Trans i18nKey="wizard.scopes.intro" components={{ strong: <strong /> }} />
        </StepIntro>
        <div className="rounded-md border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-800/60 p-3 text-xs text-slate-600 dark:text-slate-300">
          <p className="font-medium text-slate-800 dark:text-slate-100">{t("wizard.scopes.computeTitle")}</p>
          <p className="mt-0.5">{t("wizard.scopes.computeKubernetes")}</p>
          <p className="mt-0.5">{t("wizard.scopes.computeServerless")}</p>
        </div>
      </CardBody>
      <Table>
        <THead>
          <tr>
            <TH>{t("wizard.scopes.include")}</TH>
            <TH>Scope</TH>
            <TH>Compute</TH>
            <TH>Size</TH>
            <TH>{t("wizard.scopes.replicas")}</TH>
            <TH>{t("wizard.scopes.strategy")}</TH>
            <TH>{t("wizard.scopes.extras")}</TH>
          </tr>
        </THead>
        <TBody>
          {SCOPE_ORDER.map((name) => {
            const sc = spec.scopes.find((s) => s.name === name);
            const included = sc !== undefined;
            const row = sc ?? defaultScope(name);
            return (
              <TR key={name} className={included ? "" : "opacity-60"}>
                <TD>
                  <input
                    type="checkbox"
                    className={checkboxCls}
                    checked={included}
                    aria-label={t("wizard.scopes.includeScope", { name })}
                    onChange={(e) => setSpec((s) => ({ ...s, scopes: toggleScope(s.scopes, name, e.target.checked) }))}
                  />
                </TD>
                <TD className="font-medium text-slate-900 dark:text-slate-100">{name}</TD>
                <TD className="min-w-36">
                  <Select
                    aria-label={t("wizard.scopes.computeOf", { name })}
                    disabled={!included}
                    value={row.compute === "serverless" ? "serverless" : "kubernetes"}
                    options={[
                      { value: "kubernetes", label: "kubernetes" },
                      { value: "serverless", label: "serverless" },
                    ]}
                    onChange={(e) => update(name, { compute: e.target.value })}
                  />
                  {included && row.compute === "serverless" && <p className="mt-1 text-xs text-amber-800 dark:text-amber-300">{t("wizard.scopes.serverlessSoon")}</p>}
                </TD>
                <TD className="min-w-56">
                  <Select
                    aria-label={t("wizard.scopes.sizeOf", { name })}
                    disabled={!included}
                    value={row.size}
                    options={SIZES.map((z) => ({ value: z.value, label: t("wizard.scopes.sizeOption", { size: z.value, cpu: z.cpu, memory: z.memory }) }))}
                    onChange={(e) => update(name, { size: e.target.value })}
                  />
                </TD>
                <TD>
                  <Input
                    type="number"
                    min={1}
                    className="w-20"
                    aria-label={t("wizard.scopes.replicasOf", { name })}
                    disabled={!included}
                    value={row.replicas || ""}
                    onChange={(e) => update(name, { replicas: toInt(e.target.value) })}
                  />
                </TD>
                <TD>
                  <Select
                    aria-label={t("wizard.scopes.strategyOf", { name })}
                    disabled={!included}
                    value={row.strategy}
                    options={STRATEGIES.map((x) => ({ value: x, label: x }))}
                    onChange={(e) => update(name, { strategy: e.target.value })}
                  />
                </TD>
                <TD className="min-w-64">
                  {included && name === "prod" && (
                    <div className="space-y-2">
                      <Input
                        aria-label={t("wizard.scopes.prodDomain")}
                        placeholder={t("wizard.scopes.prodDomainPlaceholder")}
                        value={row.domain}
                        onChange={(e) => update(name, { domain: e.target.value })}
                      />
                      <AutoscalingEditor scope={row} onChange={(a) => update(name, { autoscaling: a })} />
                    </div>
                  )}
                  {included && name !== "prod" && name !== "pr" && <AutoscalingEditor scope={row} onChange={(a) => update(name, { autoscaling: a })} />}
                  {included && name === "pr" && (
                    <Input
                      aria-label={t("wizard.scopes.prTtl")}
                      placeholder="72h"
                      value={row.ttl}
                      onChange={(e) => update(name, { ttl: e.target.value })}
                    />
                  )}
                  {included && name === "pr" && <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">{t("wizard.scopes.ttlHelp")}</p>}
                  {included && name === "prod" && <p className="text-xs text-slate-500 dark:text-slate-400">{t("wizard.scopes.domainHelp")}</p>}
                </TD>
              </TR>
            );
          })}
        </TBody>
      </Table>
    </Card>
  );
}

function AutoscalingEditor({ scope, onChange }: { scope: SpecScope; onChange: (a: SpecScope["autoscaling"]) => void }) {
  const { t } = useTranslation();
  const a = scope.autoscaling;
  return (
    <div className="space-y-1.5">
      <label className="flex items-center gap-1.5 text-xs text-slate-700 dark:text-slate-200">
        <input
          type="checkbox"
          className={checkboxCls}
          checked={a !== null}
          onChange={(e) => onChange(e.target.checked ? { min: Math.max(scope.replicas, 1), max: Math.max(scope.replicas, 1) * 3, cpu: 70 } : null)}
        />
        Autoscaling
      </label>
      {a && (
        <div className="grid grid-cols-3 gap-1.5">
          <Input type="number" min={1} aria-label={t("wizard.scopes.minReplicas")} title={t("wizard.scopes.min")} value={a.min || ""} onChange={(e) => onChange({ ...a, min: toInt(e.target.value) })} />
          <Input type="number" min={1} aria-label={t("wizard.scopes.maxReplicas")} title={t("wizard.scopes.max")} value={a.max || ""} onChange={(e) => onChange({ ...a, max: toInt(e.target.value) })} />
          <Input type="number" min={1} max={100} aria-label={t("wizard.scopes.cpuTarget")} title="CPU %" value={a.cpu || ""} onChange={(e) => onChange({ ...a, cpu: toInt(e.target.value) })} />
          <span className="text-[11px] text-slate-500 dark:text-slate-400">{t("wizard.scopes.minShort")}</span>
          <span className="text-[11px] text-slate-500 dark:text-slate-400">{t("wizard.scopes.maxShort")}</span>
          <span className="text-[11px] text-slate-500 dark:text-slate-400">CPU %</span>
        </div>
      )}
    </div>
  );
}

// Paso 3: parameters

function ScopeChips({ available, value, onChange }: { available: string[]; value: string[]; onChange: (v: string[]) => void }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {available.map((n) => (
        <Chip key={n} label={n} active={value.includes(n)} onClick={() => onChange(value.includes(n) ? value.filter((x) => x !== n) : [...value, n])} />
      ))}
      <span className="text-xs text-slate-500 dark:text-slate-400">{value.length === 0 ? t("wizard.allScopes") : ""}</span>
    </div>
  );
}

export function ParametersStep({ spec, setSpec, org }: StepProps) {
  const { t } = useTranslation();
  const { terms } = useTerms(org);
  const generic = terms();
  const scopeNames = spec.scopes.map((s) => s.name);
  const services = spec.services.filter((s) => s.name.trim() !== "");
  const update = (i: number, p: Partial<SpecParameter>) =>
    setSpec((s) => ({ ...s, parameters: s.parameters.map((x, j) => (j === i ? { ...x, ...p } : x)) }));

  return (
    <Card>
      <CardBody className="space-y-4">
        <StepIntro>{t("wizard.parameters.intro")}</StepIntro>
        {spec.parameters.length === 0 && <p className="rounded-md border border-dashed border-slate-300 dark:border-slate-700 px-4 py-6 text-center text-sm text-slate-500 dark:text-slate-400">{t("wizard.parameters.empty")}</p>}
        {spec.parameters.map((p, i) => {
          const nameErr = p.name === "" ? undefined : (validateParamName(p.name) ?? undefined);
          return (
            <div key={i} className="space-y-3 rounded-md border border-slate-200 dark:border-slate-800 p-3">
              <div className="grid gap-3 sm:grid-cols-[1fr_1fr_auto]">
                <Field label={t("apps.colName")} error={nameErr}>
                  {(id) => (
                    <Input
                      id={id}
                      className="font-mono"
                      value={p.name}
                      placeholder="LOG_LEVEL"
                      aria-invalid={nameErr !== undefined}
                      onChange={(e) => update(i, { name: e.target.value.toUpperCase() })}
                    />
                  )}
                </Field>
                <Field label={t("wizard.parameters.source")}>
                  {(id) => (
                    <Select
                      id={id}
                      value={p.source}
                      options={[
                        { value: "value", label: t("wizard.parameters.sourceValue") },
                        { value: "secret", label: t("wizard.parameters.sourceSecret", { store: generic.secretStore }) },
                        { value: "service", label: t("wizard.parameters.sourceService") },
                      ]}
                      onChange={(e) => update(i, { source: e.target.value as SpecParameter["source"] })}
                    />
                  )}
                </Field>
                <div className="flex items-end">
                  <Button
                    variant="ghost"
                    aria-label={t("wizard.parameters.remove")}
                    title={t("settings.remove")}
                    icon={<Trash2 className="size-4 text-red-600 dark:text-red-400" />}
                    onClick={() => setSpec((s) => ({ ...s, parameters: s.parameters.filter((_, j) => j !== i) }))}
                  />
                </div>
              </div>
              {p.source === "value" && (
                <Field label={t("wizard.parameters.value")}>{(id) => <Input id={id} value={p.value ?? ""} onChange={(e) => update(i, { value: e.target.value })} />}</Field>
              )}
              {p.source === "secret" && (
                <p className="text-xs text-slate-500 dark:text-slate-400">
                  {t("wizard.parameters.secretNote", { store: generic.secretStore })} <span className="font-mono">rampa/{spec.name || "app"}/&lt;scope&gt;/{p.name || t("wizard.parameters.nameExample")}</span>
                </p>
              )}
              {p.source === "service" && (
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label="Service" help={services.length === 0 ? t("wizard.parameters.addServiceFirst") : undefined}>
                    {(id) => (
                      <Select
                        id={id}
                        value={p.service ?? ""}
                        placeholder={t("wizard.parameters.pickService")}
                        options={services.map((s) => ({ value: s.name, label: `${s.name} (${s.type})` }))}
                        onChange={(e) => update(i, { service: e.target.value })}
                      />
                    )}
                  </Field>
                  <Field label={t("wizard.parameters.key")} help={t("wizard.parameters.keyHelp")}>
                    {(id) => <Input id={id} value={p.key ?? ""} placeholder="url" onChange={(e) => update(i, { key: e.target.value })} />}
                  </Field>
                </div>
              )}
              <div>
                <div className="mb-1 text-xs font-medium text-slate-600 dark:text-slate-300">Scopes</div>
                <ScopeChips available={scopeNames} value={p.scopes} onChange={(v) => update(i, { scopes: v })} />
              </div>
            </div>
          );
        })}
        <Button variant="secondary" icon={<Plus className="size-4" />} onClick={() => setSpec((s) => ({ ...s, parameters: [...s.parameters, defaultParameter()] }))}>
          {t("wizard.parameters.add")}
        </Button>
      </CardBody>
    </Card>
  );
}

// Paso 4: services

export function ServicesStep({ spec, setSpec, org }: StepProps) {
  const { t } = useTranslation();
  const { terms } = useTerms(org);
  const generic = terms();
  const permanent = spec.scopes.map((s) => s.name).filter((n) => (PERMANENT_SCOPES as readonly string[]).includes(n));
  const update = (i: number, p: Partial<SpecService>) =>
    setSpec((s) => ({ ...s, services: s.services.map((x, j) => (j === i ? { ...x, ...p } : x)) }));

  return (
    <Card>
      <CardBody className="space-y-4">
        <StepIntro>{t("wizard.services.intro", { store: generic.secretStore })}</StepIntro>
        {spec.services.length === 0 && <p className="rounded-md border border-dashed border-slate-300 dark:border-slate-700 px-4 py-6 text-center text-sm text-slate-500 dark:text-slate-400">{t("wizard.services.empty")}</p>}
        {spec.services.map((s, i) => (
          <div key={i} className="space-y-3 rounded-md border border-slate-200 dark:border-slate-800 p-3">
            <div className="grid gap-3 sm:grid-cols-[1fr_1fr_8rem_auto]">
              <Field label={t("apps.colName")}>
                {(id) => <Input id={id} value={s.name} placeholder="checkout-db" onChange={(e) => update(i, { name: e.target.value })} />}
              </Field>
              <Field label={t("apps.colType")}>
                {(id) => <Select id={id} value={s.type} options={SERVICE_TYPES.map((x) => ({ value: x, label: x }))} onChange={(e) => update(i, { type: e.target.value })} />}
              </Field>
              <Field label="Size">
                {(id) => <Select id={id} value={s.size} options={SIZES.map((z) => ({ value: z.value, label: z.value }))} onChange={(e) => update(i, { size: e.target.value })} />}
              </Field>
              <div className="flex items-end">
                <Button
                  variant="ghost"
                  aria-label={t("wizard.services.remove")}
                  title={t("settings.remove")}
                  icon={<Trash2 className="size-4 text-red-600 dark:text-red-400" />}
                  onClick={() => setSpec((sp) => ({ ...sp, services: sp.services.filter((_, j) => j !== i) }))}
                />
              </div>
            </div>
            <div>
              <div className="mb-1 text-xs font-medium text-slate-600 dark:text-slate-300">Scopes</div>
              <ScopeChips available={permanent} value={s.scopes} onChange={(v) => update(i, { scopes: v })} />
            </div>
          </div>
        ))}
        <Button variant="secondary" icon={<Plus className="size-4" />} onClick={() => setSpec((s) => ({ ...s, services: [...s.services, defaultService()] }))}>
          {t("wizard.services.add")}
        </Button>
      </CardBody>
    </Card>
  );
}
