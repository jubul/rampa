import type { ReactNode } from "react";
import { useParams } from "react-router-dom";
import { CheckCircle2, Circle } from "lucide-react";
import { useApps, useClusters, useOnboarding } from "../api/hooks";
import { useOrgRole } from "../auth/AuthContext";
import type { Cluster, OnboardingStep } from "../api/types";
import { PageHeader } from "../components/ui/PageHeader";
import { Card, CardBody } from "../components/ui/Card";
import { LinkButton } from "../components/ui/LinkButton";
import { Badge } from "../components/ui/Badge";
import { ErrorState, LoadingState, Notice } from "../components/ui/States";
import { InstallCommandPanel } from "../components/InstallCommandPanel";
import { cn } from "../lib/cn";

export function OnboardingPage() {
  const { org = "" } = useParams();
  const onboardingQ = useOnboarding(org);
  const clustersQ = useClusters(org);
  const appsQ = useApps(org);
  const { isAdmin } = useOrgRole(org);

  if (onboardingQ.isPending) return <LoadingState label="Cargando los pasos" />;
  if (onboardingQ.isError) return <ErrorState title="No se pudo cargar el onboarding" error={onboardingQ.error} onRetry={() => void onboardingQ.refetch()} />;

  const { steps, next, complete } = onboardingQ.data;
  const done = steps.filter((s) => s.done).length;
  const offline = (clustersQ.data ?? []).find((c) => c.status === "offline");
  const firstApp = appsQ.data?.[0];

  const actions = (s: OnboardingStep): ReactNode => {
    switch (s.key) {
      case "cloud":
        return <LinkButton variant={s.done ? "secondary" : "primary"} to={`/o/${org}/cloud-accounts?connect=1`}>{s.done ? "Conectar otra nube" : "Conectar nube"}</LinkButton>;
      case "cluster":
        return (
          <>
            <LinkButton variant={s.done ? "secondary" : "primary"} to={`/o/${org}/infra/new-cluster`}>
              Crear cluster con un click
            </LinkButton>
            <LinkButton variant="secondary" to={`/o/${org}/clusters?new=1`}>
              Ya tengo un cluster
            </LinkButton>
          </>
        );
      case "agent":
        return <AgentHelp org={org} done={s.done} cluster={offline} loading={clustersQ.isPending} canRotate={isAdmin} />;
      case "app":
        return <LinkButton variant={s.done ? "secondary" : "primary"} to={`/o/${org}/apps/new`}>{s.done ? "Crear otra app" : "Crear primera app"}</LinkButton>;
      case "deploy":
        return firstApp ? (
          <LinkButton variant={s.done ? "secondary" : "primary"} to={`/o/${org}/apps/${firstApp.name}`}>
            {s.done ? "Ver la app" : "Hacer el primer deploy"}
          </LinkButton>
        ) : (
          <span className="text-xs text-slate-500">Primero creá una aplicación.</span>
        );
      default:
        return null;
    }
  };

  return (
    <>
      <PageHeader
        title="Empezar"
        description={complete ? "Todo listo: tu plataforma está funcionando." : `Llevás ${done} de ${steps.length} pasos. Todo se hace con clicks, sin escribir YAML.`}
      />
      {complete && <Notice tone="green">Completaste los cinco pasos. Podés seguir creando apps y clusters cuando quieras.</Notice>}
      <ol className="mt-4 space-y-3">
        {steps.map((s, i) => (
          <li key={s.key}>
            <Card className={cn(s.key === next && !complete && "ring-2 ring-slate-900")}>
              <CardBody className="flex flex-wrap items-start gap-4">
                <div className="mt-0.5" aria-hidden>
                  {s.done ? <CheckCircle2 className="size-6 text-green-600" /> : <Circle className="size-6 text-slate-300" />}
                </div>
                <div className="min-w-0 flex-1 space-y-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <h2 className="text-sm font-semibold text-slate-900">
                      {i + 1}. {s.title}
                    </h2>
                    <Badge tone={s.done ? "green" : s.key === next ? "amber" : "gray"}>{s.done ? "Hecho" : s.key === next ? "Siguiente" : "Pendiente"}</Badge>
                  </div>
                  {s.detail && <p className="text-xs text-slate-500">{s.detail}</p>}
                  <div className="flex flex-wrap items-start gap-2">{actions(s)}</div>
                </div>
              </CardBody>
            </Card>
          </li>
        ))}
      </ol>
    </>
  );
}

function AgentHelp({ org, done, cluster, loading, canRotate }: { org: string; done: boolean; cluster: Cluster | undefined; loading: boolean; canRotate: boolean }) {
  return (
    <div className="w-full space-y-3">
      <p className="text-xs text-slate-600">
        Si creaste el cluster con Rampa, el agente ya se instaló solo: solo hay que esperar a que aparezca en línea. Si registraste un cluster que ya tenías, instalá el agente con el comando de instalación.
      </p>
      {done && !cluster && <p className="text-xs text-green-700">Ya hay un cluster en línea.</p>}
      {!loading && cluster && (
        <div className="space-y-2 rounded-md border border-slate-200 bg-slate-50 p-3">
          <p className="text-xs font-medium text-slate-700">
            {cluster.name} está desconectado{cluster.infra_run_id ? " (lo creó Rampa: mirá su ejecución antes de tocar el token)" : ""}
          </p>
          {cluster.infra_run_id && (
            <LinkButton size="sm" variant="secondary" to={`/o/${org}/infra/runs/${cluster.infra_run_id}`}>
              Ver la ejecución
            </LinkButton>
          )}
          <InstallCommandPanel org={org} cluster={cluster} canRotate={canRotate} />
        </div>
      )}
      {!loading && !cluster && !done && <p className="text-xs text-slate-500">Todavía no hay un cluster desconectado. Primero creá o registrá uno.</p>}
    </div>
  );
}
