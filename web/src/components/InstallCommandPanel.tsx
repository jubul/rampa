import { useState } from "react";
import { KeyRound } from "lucide-react";
import type { Cluster, ClusterWithToken } from "../api/types";
import { errorMessage } from "../api/client";
import { useRotateClusterToken } from "../api/hooks";
import { Button } from "./ui/Button";
import { CopyField } from "./ui/CopyField";
import { InlineError, Notice } from "./ui/States";

/**
 * El install_command solo se entrega al crear el cluster o al rotar su token.
 * Este panel rota el token a pedido y muestra el comando para pegarlo.
 */
export function InstallCommandPanel({ org, cluster, canRotate }: { org: string; cluster: Cluster; canRotate: boolean }) {
  const rotate = useRotateClusterToken(org);
  const [issued, setIssued] = useState<ClusterWithToken | null>(null);

  if (issued) {
    return (
      <div className="space-y-3">
        <Notice tone="amber">
          <strong>Este comando se muestra una sola vez.</strong> El token anterior de {cluster.name} ya no vale.
        </Notice>
        <CopyField label="Comando de instalación" value={issued.install_command} multiline />
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <p className="text-xs text-slate-500">
        Rampa guarda solo un hash del token, así que el comando no se puede volver a mostrar. Podés generar uno nuevo. Ojo: el token actual deja de valer, y si el cluster ya tenía un agente instalado va a desconectarse hasta que uses el comando nuevo.
      </p>
      <Button
        variant="secondary"
        size="sm"
        disabled={!canRotate}
        title={canRotate ? undefined : "Necesitás rol admin"}
        loading={rotate.isPending}
        icon={<KeyRound className="size-3.5" />}
        onClick={() => rotate.mutate(cluster.id, { onSuccess: setIssued })}
      >
        Generar comando de instalación
      </Button>
      {rotate.isError && <InlineError error={new Error(errorMessage(rotate.error))} />}
    </div>
  );
}
