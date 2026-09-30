import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
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
  const { t } = useTranslation();
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

  const repoError = repoUrl.trim() !== "" && !safeHttpUrl(repoUrl.trim()) ? t("wizard.repoUrlInvalid") : undefined;
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
      <nav aria-label={t("wizard.stepsLabel")} className="flex flex-wrap gap-2">
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
                step === s.key ? "border-slate-900 bg-slate-900 text-white dark:border-slate-100 dark:bg-slate-100 dark:text-slate-900" : "border-slate-300 bg-white text-slate-700 hover:bg-slate-50 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200 dark:hover:bg-slate-800",
              )}
            >
              <span className="text-xs opacity-70">{i + 1}</span>
              {t(`wizard.steps.${s.key}`)}
              {n > 0 && (
                <span className="rounded-full bg-red-600 px-1.5 text-[11px] leading-4 text-white" title={t("wizard.errorCount", { count: n })}>
                  {n}
                </span>
              )}
            </button>
          );
        })}
      </nav>

      {step !== "review" && errorsByStep[step].length > 0 && <StepErrors errors={errorsByStep[step]} />}

      {step === "basics" && <BasicsStep org={org} spec={spec} setSpec={setSpec} lockName={mode === "edit"} />}
      {step === "scopes" && <ScopesStep org={org} spec={spec} setSpec={setSpec} />}
      {step === "parameters" && <ParametersStep org={org} spec={spec} setSpec={setSpec} />}
      {step === "services" && <ServicesStep org={org} spec={spec} setSpec={setSpec} />}
      {step === "review" && (
        <Card>
          <CardBody className="space-y-4">
            {mode === "create" && (
              <Field label={t("apps.repoUrl")} help={t("wizard.repoUrlHelp")} error={repoError}>
                {(id) => <Input id={id} value={repoUrl} placeholder="https://github.com/acme/checkout-api" aria-invalid={repoError !== undefined} onChange={(e) => setRepoUrl(e.target.value)} />}
              </Field>
            )}
            <div className="flex items-center gap-2 text-sm">
              {localCount > 0 ? (
                <span className="text-red-700 dark:text-red-300">{t("wizard.missingRequired")}</span>
              ) : pending ? (
                <span className="inline-flex items-center gap-2 text-slate-500 dark:text-slate-400">
                  <Loader2 className="size-4 animate-spin" /> {t("wizard.validating")}
                </span>
              ) : preview.isError ? (
                <span className="text-red-700 dark:text-red-300">{t("wizard.validateFailed", { error: errorMessage(preview.error) })}</span>
              ) : valid ? (
                <span className="inline-flex items-center gap-1.5 text-green-700 dark:text-green-300">
                  <CheckCircle2 className="size-4" /> {t("wizard.manifestValid")}
                </span>
              ) : (
                <span className="text-red-700 dark:text-red-300">{t("wizard.manifestInvalid")}</span>
              )}
            </div>
            {WIZARD_STEPS.filter((s) => s.key !== "review" && errorsByStep[s.key].length > 0).map((s) => (
              <div key={s.key} className="rounded-md border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 p-3" role="alert">
                <div className="mb-1 flex items-center justify-between">
                  <span className="text-sm font-medium text-red-800 dark:text-red-300">{t(`wizard.steps.${s.key}`)}</span>
                  <Button size="sm" variant="secondary" onClick={() => setStep(s.key)}>
                    {t("wizard.goToStep")}
                  </Button>
                </div>
                <ul className="list-inside list-disc space-y-0.5 font-mono text-xs text-red-700 dark:text-red-300">
                  {errorsByStep[s.key].map((d, i) => (
                    <li key={i}>{d}</li>
                  ))}
                </ul>
              </div>
            ))}
            <div>
              <div className="mb-1.5 text-sm font-medium text-slate-700 dark:text-slate-200">{t("wizard.resultYaml")}</div>
              {remote?.manifest ? (
                <CodeBlock value={remote.manifest} />
              ) : (
                <p className="rounded-md border border-dashed border-slate-300 dark:border-slate-700 px-4 py-6 text-center text-sm text-slate-500 dark:text-slate-400">
                  {localCount > 0 ? t("wizard.yamlWaiting") : t("wizard.yamlGenerating")}
                </p>
              )}
              <p className="mt-1.5 text-xs text-slate-500 dark:text-slate-400">{t("wizard.yamlReadonly")}</p>
            </div>
            {mode === "edit" && <Notice tone="blue">{t("wizard.editNotice")}</Notice>}
            {saveError && <InlineError error={saveError} />}
          </CardBody>
        </Card>
      )}

      <div className="flex items-center justify-between">
        <Button variant="secondary" disabled={!prev} icon={<ArrowLeft className="size-4" />} onClick={() => prev && setStep(prev.key)}>
          {t("wizard.back")}
        </Button>
        <div className="flex items-center gap-2">
          {next && (
            <Button variant={step === "review" ? "secondary" : "primary"} onClick={() => setStep(next.key)}>
              {t("wizard.next")} <ArrowRight className="size-4" />
            </Button>
          )}
          <Button variant={next ? "secondary" : "primary"} disabled={!canSave} loading={saving} onClick={save} title={canSave ? undefined : t("wizard.saveDisabled")}>
            {mode === "create" ? t("wizard.create") : t("wizard.saveChanges")}
          </Button>
        </div>
      </div>
    </div>
  );
}

function StepErrors({ errors }: { errors: string[] }) {
  return (
    <div className="rounded-md border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-3 py-2 text-sm text-red-700 dark:text-red-300" role="alert">
      <ul className="list-inside list-disc space-y-0.5 font-mono text-xs">
        {errors.map((d, i) => (
          <li key={i}>{d}</li>
        ))}
      </ul>
    </div>
  );
}
