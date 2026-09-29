import type { ReactNode } from "react";
import { Plus, Trash2 } from "lucide-react";
import type { ApplicationSpec, SpecParameter, SpecScope, SpecService } from "../../api/types";
import { Card, CardBody } from "../ui/Card";
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
}

const checkboxCls = "size-4 rounded border-slate-300";

function toInt(v: string): number {
  const n = parseInt(v, 10);
  return Number.isFinite(n) ? n : 0;
}

function StepIntro({ children }: { children: ReactNode }) {
  return <p className="mb-4 text-sm text-slate-600">{children}</p>;
}

// Paso 1: básicos

export function BasicsStep({ spec, setSpec, lockName }: StepProps & { lockName: boolean }) {
  const nameError = spec.name === "" ? undefined : (validateAppName(spec.name) ?? undefined);
  const tier = TIERS.find((t) => t.value === spec.tier);
  const type = APP_TYPES.find((t) => t.value === spec.type);
  const patch = (p: Partial<ApplicationSpec>) => setSpec((s) => ({ ...s, ...p }));

  return (
    <Card>
      <CardBody className="space-y-5">
        <StepIntro>Lo mínimo para identificar la aplicación y saber cómo se ejecuta.</StepIntro>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field
            label="Nombre"
            required
            error={nameError}
            help={lockName ? "El nombre no se puede cambiar." : "Minúsculas, dígitos y guiones. Es el nombre del namespace y de la URL."}
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
          <Field label="Equipo" required help="Owner de la app. Va a etiquetas, costos y alertas.">
            {(id) => <Input id={id} value={spec.team} placeholder="payments" onChange={(e) => patch({ team: e.target.value })} />}
          </Field>
          <Field label="Tier" help={tier?.help}>
            {(id) => (
              <Select
                id={id}
                value={String(spec.tier)}
                options={TIERS.map((t) => ({ value: String(t.value), label: t.label }))}
                onChange={(e) => patch({ tier: toInt(e.target.value) })}
              />
            )}
          </Field>
          <Field label="Runtime" help="Informativo: elige templates y dashboards.">
            {(id) => (
              <Select id={id} value={spec.runtime} options={RUNTIMES.map((r) => ({ value: r, label: r }))} onChange={(e) => patch({ runtime: e.target.value })} />
            )}
          </Field>
        </div>
        <Field label="Descripción">
          {(id) => <Textarea id={id} rows={2} value={spec.description} placeholder="API de checkout" onChange={(e) => patch({ description: e.target.value })} />}
        </Field>

        <div>
          <div className="mb-2 text-sm font-medium text-slate-700">Tipo</div>
          <div className="grid gap-2 sm:grid-cols-4" role="radiogroup" aria-label="Tipo de aplicación">
            {APP_TYPES.map((t) => (
              <button
                key={t.value}
                type="button"
                role="radio"
                aria-checked={spec.type === t.value}
                onClick={() => patch({ type: t.value })}
                className={`rounded-md border p-3 text-left ${spec.type === t.value ? "border-slate-900 bg-slate-50 ring-1 ring-slate-900" : "border-slate-300 hover:bg-slate-50"}`}
              >
                <div className="text-sm font-medium text-slate-900">{t.label}</div>
                <div className="mt-0.5 text-xs text-slate-500">{t.help}</div>
              </button>
            ))}
          </div>
          {type && <span className="sr-only">{type.help}</span>}
        </div>

        {hasPort(spec.type) && (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Puerto" help="Puerto en el que escucha el contenedor.">
              {(id) => <Input id={id} type="number" min={1} max={65535} value={spec.port || ""} onChange={(e) => patch({ port: toInt(e.target.value) })} />}
            </Field>
            {spec.type === "api" && (
              <Field label="Healthcheck" help="Ruta HTTP que responde 200 cuando la app está sana.">
                {(id) => <Input id={id} value={spec.healthcheck} placeholder="/health" onChange={(e) => patch({ healthcheck: e.target.value })} />}
              </Field>
            )}
          </div>
        )}

        {spec.type === "cron" && (
          <Field
            label="Schedule"
            required
            help='Cinco campos: minuto hora día-del-mes mes día-de-la-semana. Por ejemplo "*/5 * * * *" corre cada 5 minutos y "0 3 * * *" todos los días a las 3.'
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
  const update = (name: string, p: Partial<SpecScope>) =>
    setSpec((s) => ({ ...s, scopes: s.scopes.map((sc) => (sc.name === name ? { ...sc, ...p } : sc)) }));

  return (
    <Card>
      <CardBody>
        <StepIntro>
          Cada scope es un namespace propio. <strong>pr</strong> es la plantilla de los scopes efímeros que se crean por cada pull request.
        </StepIntro>
      </CardBody>
      <Table>
        <THead>
          <tr>
            <TH>Incluir</TH>
            <TH>Scope</TH>
            <TH>Size</TH>
            <TH>Réplicas</TH>
            <TH>Estrategia</TH>
            <TH>Extras</TH>
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
                    aria-label={`Incluir ${name}`}
                    onChange={(e) => setSpec((s) => ({ ...s, scopes: toggleScope(s.scopes, name, e.target.checked) }))}
                  />
                </TD>
                <TD className="font-medium text-slate-900">{name}</TD>
                <TD className="min-w-56">
                  <Select
                    aria-label={`Size de ${name}`}
                    disabled={!included}
                    value={row.size}
                    options={SIZES.map((z) => ({ value: z.value, label: `${z.value}: CPU ${z.cpu}, mem ${z.memory}` }))}
                    onChange={(e) => update(name, { size: e.target.value })}
                  />
                </TD>
                <TD>
                  <Input
                    type="number"
                    min={1}
                    className="w-20"
                    aria-label={`Réplicas de ${name}`}
                    disabled={!included}
                    value={row.replicas || ""}
                    onChange={(e) => update(name, { replicas: toInt(e.target.value) })}
                  />
                </TD>
                <TD>
                  <Select
                    aria-label={`Estrategia de ${name}`}
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
                        aria-label="Dominio de prod"
                        placeholder="checkout.acme.com (opcional)"
                        value={row.domain}
                        onChange={(e) => update(name, { domain: e.target.value })}
                      />
                      <AutoscalingEditor scope={row} onChange={(a) => update(name, { autoscaling: a })} />
                    </div>
                  )}
                  {included && name !== "prod" && name !== "pr" && <AutoscalingEditor scope={row} onChange={(a) => update(name, { autoscaling: a })} />}
                  {included && name === "pr" && (
                    <Input
                      aria-label="TTL de pr"
                      placeholder="72h"
                      value={row.ttl}
                      onChange={(e) => update(name, { ttl: e.target.value })}
                    />
                  )}
                  {included && name === "pr" && <p className="mt-1 text-xs text-slate-500">Tiempo de vida del scope antes de borrarse solo.</p>}
                  {included && name === "prod" && <p className="text-xs text-slate-500">Sin dominio, la URL usa el dominio del cluster.</p>}
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
  const a = scope.autoscaling;
  return (
    <div className="space-y-1.5">
      <label className="flex items-center gap-1.5 text-xs text-slate-700">
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
          <Input type="number" min={1} aria-label="Mínimo de réplicas" title="Mínimo" value={a.min || ""} onChange={(e) => onChange({ ...a, min: toInt(e.target.value) })} />
          <Input type="number" min={1} aria-label="Máximo de réplicas" title="Máximo" value={a.max || ""} onChange={(e) => onChange({ ...a, max: toInt(e.target.value) })} />
          <Input type="number" min={1} max={100} aria-label="CPU objetivo en porcentaje" title="CPU %" value={a.cpu || ""} onChange={(e) => onChange({ ...a, cpu: toInt(e.target.value) })} />
          <span className="text-[11px] text-slate-500">mín.</span>
          <span className="text-[11px] text-slate-500">máx.</span>
          <span className="text-[11px] text-slate-500">CPU %</span>
        </div>
      )}
    </div>
  );
}

// Paso 3: parameters

function ScopeChips({ available, value, onChange }: { available: string[]; value: string[]; onChange: (v: string[]) => void }) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {available.map((n) => (
        <Chip key={n} label={n} active={value.includes(n)} onClick={() => onChange(value.includes(n) ? value.filter((x) => x !== n) : [...value, n])} />
      ))}
      <span className="text-xs text-slate-500">{value.length === 0 ? "Todos los scopes" : ""}</span>
    </div>
  );
}

export function ParametersStep({ spec, setSpec }: StepProps) {
  const scopeNames = spec.scopes.map((s) => s.name);
  const services = spec.services.filter((s) => s.name.trim() !== "");
  const update = (i: number, p: Partial<SpecParameter>) =>
    setSpec((s) => ({ ...s, parameters: s.parameters.map((x, j) => (j === i ? { ...x, ...p } : x)) }));

  return (
    <Card>
      <CardBody className="space-y-4">
        <StepIntro>
          Configuración que la app recibe como variables de entorno. Un mismo nombre puede repetirse con scopes distintos: gana la definición más específica.
        </StepIntro>
        {spec.parameters.length === 0 && <p className="rounded-md border border-dashed border-slate-300 px-4 py-6 text-center text-sm text-slate-500">Sin parameters. Es opcional.</p>}
        {spec.parameters.map((p, i) => {
          const nameErr = p.name === "" ? undefined : (validateParamName(p.name) ?? undefined);
          return (
            <div key={i} className="space-y-3 rounded-md border border-slate-200 p-3">
              <div className="grid gap-3 sm:grid-cols-[1fr_1fr_auto]">
                <Field label="Nombre" error={nameErr}>
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
                <Field label="Origen">
                  {(id) => (
                    <Select
                      id={id}
                      value={p.source}
                      options={[
                        { value: "value", label: "Valor plano" },
                        { value: "secret", label: "Secreto en el gestor de la nube" },
                        { value: "service", label: "Credencial de un service" },
                      ]}
                      onChange={(e) => update(i, { source: e.target.value as SpecParameter["source"] })}
                    />
                  )}
                </Field>
                <div className="flex items-end">
                  <Button
                    variant="ghost"
                    aria-label="Quitar parameter"
                    title="Quitar"
                    icon={<Trash2 className="size-4 text-red-600" />}
                    onClick={() => setSpec((s) => ({ ...s, parameters: s.parameters.filter((_, j) => j !== i) }))}
                  />
                </div>
              </div>
              {p.source === "value" && (
                <Field label="Valor">{(id) => <Input id={id} value={p.value ?? ""} onChange={(e) => update(i, { value: e.target.value })} />}</Field>
              )}
              {p.source === "secret" && (
                <p className="text-xs text-slate-500">
                  El valor no se guarda acá. Se carga en el gestor de secretos de la nube, en <span className="font-mono">rampa/{spec.name || "app"}/&lt;scope&gt;/{p.name || "NOMBRE"}</span>.
                </p>
              )}
              {p.source === "service" && (
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label="Service" help={services.length === 0 ? "Primero agregá un service en el paso Services." : undefined}>
                    {(id) => (
                      <Select
                        id={id}
                        value={p.service ?? ""}
                        placeholder="Elegí un service"
                        options={services.map((s) => ({ value: s.name, label: `${s.name} (${s.type})` }))}
                        onChange={(e) => update(i, { service: e.target.value })}
                      />
                    )}
                  </Field>
                  <Field label="Clave" help="Clave dentro del secreto del service, por ejemplo url.">
                    {(id) => <Input id={id} value={p.key ?? ""} placeholder="url" onChange={(e) => update(i, { key: e.target.value })} />}
                  </Field>
                </div>
              )}
              <div>
                <div className="mb-1 text-xs font-medium text-slate-600">Scopes</div>
                <ScopeChips available={scopeNames} value={p.scopes} onChange={(v) => update(i, { scopes: v })} />
              </div>
            </div>
          );
        })}
        <Button variant="secondary" icon={<Plus className="size-4" />} onClick={() => setSpec((s) => ({ ...s, parameters: [...s.parameters, defaultParameter()] }))}>
          Agregar parameter
        </Button>
      </CardBody>
    </Card>
  );
}

// Paso 4: services

export function ServicesStep({ spec, setSpec }: StepProps) {
  const permanent = spec.scopes.map((s) => s.name).filter((n) => (PERMANENT_SCOPES as readonly string[]).includes(n));
  const update = (i: number, p: Partial<SpecService>) =>
    setSpec((s) => ({ ...s, services: s.services.map((x, j) => (j === i ? { ...x, ...p } : x)) }));

  return (
    <Card>
      <CardBody className="space-y-4">
        <StepIntro>
          Dependencias de infraestructura gestionadas por Rampa. Sus credenciales quedan en el gestor de secretos y se enlazan desde un parameter. Los scopes efímeros nunca reciben services.
        </StepIntro>
        {spec.services.length === 0 && <p className="rounded-md border border-dashed border-slate-300 px-4 py-6 text-center text-sm text-slate-500">Sin services. Es opcional.</p>}
        {spec.services.map((s, i) => (
          <div key={i} className="space-y-3 rounded-md border border-slate-200 p-3">
            <div className="grid gap-3 sm:grid-cols-[1fr_1fr_8rem_auto]">
              <Field label="Nombre">
                {(id) => <Input id={id} value={s.name} placeholder="checkout-db" onChange={(e) => update(i, { name: e.target.value })} />}
              </Field>
              <Field label="Tipo">
                {(id) => <Select id={id} value={s.type} options={SERVICE_TYPES.map((t) => ({ value: t, label: t }))} onChange={(e) => update(i, { type: e.target.value })} />}
              </Field>
              <Field label="Size">
                {(id) => <Select id={id} value={s.size} options={SIZES.map((z) => ({ value: z.value, label: z.value }))} onChange={(e) => update(i, { size: e.target.value })} />}
              </Field>
              <div className="flex items-end">
                <Button
                  variant="ghost"
                  aria-label="Quitar service"
                  title="Quitar"
                  icon={<Trash2 className="size-4 text-red-600" />}
                  onClick={() => setSpec((sp) => ({ ...sp, services: sp.services.filter((_, j) => j !== i) }))}
                />
              </div>
            </div>
            <div>
              <div className="mb-1 text-xs font-medium text-slate-600">Scopes</div>
              <ScopeChips available={permanent} value={s.scopes} onChange={(v) => update(i, { scopes: v })} />
            </div>
          </div>
        ))}
        <Button variant="secondary" icon={<Plus className="size-4" />} onClick={() => setSpec((s) => ({ ...s, services: [...s.services, defaultService()] }))}>
          Agregar service
        </Button>
      </CardBody>
    </Card>
  );
}
