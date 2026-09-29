import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ArrowLeft, ArrowRight, CheckCircle2, Loader2 } from "lucide-react";
import type { ApplicationSpec } from "../../api/types";
import { errorMessage } from "../../api/client";
import { useCreateFromSpec, useSpecPreview, useUpdateSpec } from "../../api/hooks";
import { useDebounced } from "../../hooks/useDebounced";
import { Button } from "../ui/Button";
import { Card, CardBody } from "../ui/Card";
import { CodeBlock } from "../ui/CodeBlock";
import { Field } from "../ui/Field";
import { Input } from "../ui/Input";
import { InlineError, Notice } from "../ui/States";
import { cn } from "../../lib/cn";
import { safeHttpUrl } from "../../lib/url";
import { WIZARD_STEPS, countErrors, groupDetailsByStep, localErrors, normalizeSpec, type WizardStep } from "../../lib/spec";
import { BasicsStep, ParametersStep, ScopesStep, ServicesStep, type SetSpec } from "./steps";

interface Props {
  org: string;
  mode: "create" | "edit";
  initialSpec: ApplicationSpec;
}

/** Wizard de aplicación. Todo el estado vive en un solo ApplicationSpec. */
export function AppWizard({ org, mode, initialSpec }: Props) {
  const navigate = useNavigate();
  const [spec, setSpecState] = useState<ApplicationSpec>(initialSpec);
  const [repoUrl, setRepoUrl] = useState("");
  const [step, setStep] = useState<WizardStep>("basics");
  const setSpec: SetSpec = (fn) => setSpecState((s) => fn(s));

  const normalized = useMemo(() => normalizeSpec(spec), [spec]);
  const debounced = useDebounced(normalized, 500);
  const settled = JSON.stringify(debounced) === JSON.stringify(normalized);

  const local = useMemo(() => localErrors(normalized), [normalized]);
  const localCount = countErrors(local);
  const preview = useSpecPreview(org, debounced, localCount === 0 && settled);

  const create = useCreateFromSpec(org);
  const update = useUpdateSpec(org, initialSpec.name);
  const saving = create.isPending || update.isPending;
  const saveError = create.error ?? update.error;

  const remote = preview.data && !preview.isPlaceholderData && settled ? preview.data : null;
  const remoteDetails = remote && !remote.valid ? remote.details : [];
  const byStepRemote = groupDetailsByStep(remoteDetails);
  const errorsByStep = useMemo(() => {
    const out: Record<WizardStep, string[]> = { basics: [], scopes: [], parameters: [], services: [], review: [] };
    for (const s of WIZARD_STEPS) out[s.key] = [...local[s.key], ...byStepRemote[s.key]];
    return out;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [local, remote]);

  const repoError = repoUrl.trim() !== "" && !safeHttpUrl(repoUrl.trim()) ? "Tiene que ser una URL http o https" : undefined;
  const pending = localCount === 0 && (!settled || preview.isFetching || (!remote && !preview.isError));
  const valid = localCount === 0 && remote !== null && remote.valid && !preview.isError;
  const canSave = valid && !repoError && !saving;

  const save = () => {
    if (mode === "create") {
      const body = { spec: normalized, ...(repoUrl.trim() ? { repo_url: repoUrl.trim() } : {}) };
      create.mutate(body, { onSuccess: (a) => navigate(`/o/${org}/apps/${a.name}`) });
    } else {
      update.mutate(normalized, { onSuccess: (a) => navigate(`/o/${org}/apps/${a.name}`) });
    }
  };

  const idx = WIZARD_STEPS.findIndex((s) => s.key === step);
  const prev = WIZARD_STEPS[idx - 1];
  const next = WIZARD_STEPS[idx + 1];

  return (
    <div className="space-y-5">
      <nav aria-label="Pasos" className="flex flex-wrap gap-2">
        {WIZARD_STEPS.map((s, i) => {
          const n = errorsByStep[s.key].length;
          return (
            <button
              key={s.key}
              type="button"
              onClick={() => setStep(s.key)}
              aria-current={step === s.key ? "step" : undefined}
              className={cn(
                "flex items-center gap-2 rounded-md border px-3 py-1.5 text-sm font-medium",
                step === s.key ? "border-slate-900 bg-slate-900 text-white" : "border-slate-300 bg-white text-slate-700 hover:bg-slate-50",
              )}
            >
              <span className="text-xs opacity-70">{i + 1}</span>
              {s.label}
              {n > 0 && (
                <span className="rounded-full bg-red-600 px-1.5 text-[11px] leading-4 text-white" title={`${n} error${n === 1 ? "" : "es"}`}>
                  {n}
                </span>
              )}
            </button>
          );
        })}
      </nav>

      {step !== "review" && errorsByStep[step].length > 0 && <StepErrors errors={errorsByStep[step]} />}

      {step === "basics" && <BasicsStep spec={spec} setSpec={setSpec} lockName={mode === "edit"} />}
      {step === "scopes" && <ScopesStep spec={spec} setSpec={setSpec} />}
      {step === "parameters" && <ParametersStep spec={spec} setSpec={setSpec} />}
      {step === "services" && <ServicesStep spec={spec} setSpec={setSpec} />}
      {step === "review" && (
        <Card>
          <CardBody className="space-y-4">
            {mode === "create" && (
              <Field label="URL del repositorio" help="Opcional. Podés completarla después." error={repoError}>
                {(id) => <Input id={id} value={repoUrl} placeholder="https://github.com/acme/checkout-api" aria-invalid={repoError !== undefined} onChange={(e) => setRepoUrl(e.target.value)} />}
              </Field>
            )}
            <div className="flex items-center gap-2 text-sm">
              {localCount > 0 ? (
                <span className="text-red-700">Faltan datos obligatorios. Revisá los pasos marcados en rojo.</span>
              ) : pending ? (
                <span className="inline-flex items-center gap-2 text-slate-500">
                  <Loader2 className="size-4 animate-spin" /> Validando
                </span>
              ) : preview.isError ? (
                <span className="text-red-700">No se pudo validar: {errorMessage(preview.error)}</span>
              ) : valid ? (
                <span className="inline-flex items-center gap-1.5 text-green-700">
                  <CheckCircle2 className="size-4" /> El manifest es válido
                </span>
              ) : (
                <span className="text-red-700">El manifest tiene errores</span>
              )}
            </div>
            {WIZARD_STEPS.filter((s) => s.key !== "review" && errorsByStep[s.key].length > 0).map((s) => (
              <div key={s.key} className="rounded-md border border-red-200 bg-red-50 p-3" role="alert">
                <div className="mb-1 flex items-center justify-between">
                  <span className="text-sm font-medium text-red-800">{s.label}</span>
                  <Button size="sm" variant="secondary" onClick={() => setStep(s.key)}>
                    Ir al paso
                  </Button>
                </div>
                <ul className="list-inside list-disc space-y-0.5 font-mono text-xs text-red-700">
                  {errorsByStep[s.key].map((d, i) => (
                    <li key={i}>{d}</li>
                  ))}
                </ul>
              </div>
            ))}
            <div>
              <div className="mb-1.5 text-sm font-medium text-slate-700">rampa.yaml resultante</div>
              {remote?.manifest ? (
                <CodeBlock value={remote.manifest} />
              ) : (
                <p className="rounded-md border border-dashed border-slate-300 px-4 py-6 text-center text-sm text-slate-500">
                  {localCount > 0 ? "El YAML aparece cuando los datos obligatorios están completos." : "Generando el YAML..."}
                </p>
              )}
              <p className="mt-1.5 text-xs text-slate-500">Solo lectura. El YAML es la fuente de verdad: esto es lo que se guarda.</p>
            </div>
            {mode === "edit" && <Notice tone="blue">Al guardar se reemplaza el manifest de la aplicación. Los scopes ya desplegados no cambian hasta el próximo deploy.</Notice>}
            {saveError && <InlineError error={saveError} />}
          </CardBody>
        </Card>
      )}

      <div className="flex items-center justify-between">
        <Button variant="secondary" disabled={!prev} icon={<ArrowLeft className="size-4" />} onClick={() => prev && setStep(prev.key)}>
          Atrás
        </Button>
        <div className="flex items-center gap-2">
          {next && (
            <Button variant={step === "review" ? "secondary" : "primary"} onClick={() => setStep(next.key)}>
              Siguiente <ArrowRight className="size-4" />
            </Button>
          )}
          <Button variant={next ? "secondary" : "primary"} disabled={!canSave} loading={saving} onClick={save} title={canSave ? undefined : "Completá los pasos hasta que la revisión sea válida"}>
            {mode === "create" ? "Crear aplicación" : "Guardar cambios"}
          </Button>
        </div>
      </div>
    </div>
  );
}

function StepErrors({ errors }: { errors: string[] }) {
  return (
    <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700" role="alert">
      <ul className="list-inside list-disc space-y-0.5 font-mono text-xs">
        {errors.map((d, i) => (
          <li key={i}>{d}</li>
        ))}
      </ul>
    </div>
  );
}
