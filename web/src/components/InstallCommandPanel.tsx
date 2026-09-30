import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { Terminal } from "lucide-react";
import type { Cluster } from "../api/types";
import { useInstallCommand } from "../api/hooks";
import { Button } from "./ui/Button";
import { CopyField } from "./ui/CopyField";
import { InlineError, LoadingState, Notice } from "./ui/States";

/**
 * Muestra el comando de instalación del agente con GET /clusters/{id}/install-command.
 * Cada consulta genera un bootstrap token nuevo (de un solo uso, vence en 2
 * horas) y NO rota el token del cluster: el agente que ya está instalado sigue
 * conectado.
 */
export function InstallCommandPanel({
  org,
  cluster,
  canView,
  autoLoad = false,
}: {
  org: string;
  cluster: Pick<Cluster, "id" | "name">;
  canView: boolean;
  /** Pide el comando apenas se monta (por ejemplo dentro de un diálogo). */
  autoLoad?: boolean;
}) {
  const { t } = useTranslation();
  const fetchCmd = useInstallCommand(org);
  const started = useRef(false);

  useEffect(() => {
    if (autoLoad && canView && !started.current) {
      started.current = true;
      fetchCmd.mutate(cluster.id);
    }
  }, [autoLoad, canView, cluster.id, fetchCmd]);

  return (
    <div className="space-y-3">
      <p className="text-xs text-slate-500 dark:text-slate-400">{t("installCommand.explain")}</p>
      {fetchCmd.isPending && <LoadingState />}
      {fetchCmd.data && (
        <>
          <Notice tone="amber">{t("installCommand.oneTime", { name: cluster.name })}</Notice>
          <CopyField label={t("installCommand.label")} value={fetchCmd.data.install_command} multiline />
        </>
      )}
      {fetchCmd.isError && <InlineError error={fetchCmd.error} />}
      {!autoLoad || fetchCmd.isError || fetchCmd.data ? (
        <Button
          variant="secondary"
          size="sm"
          disabled={!canView}
          title={canView ? undefined : t("common.needAdmin")}
          loading={fetchCmd.isPending}
          icon={<Terminal className="size-3.5" />}
          onClick={() => fetchCmd.mutate(cluster.id)}
        >
          {fetchCmd.data ? t("installCommand.regenerate") : t("installCommand.show")}
        </Button>
      ) : null}
    </div>
  );
}
