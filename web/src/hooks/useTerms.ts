import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import { useCloudAccounts, useProviders } from "../api/hooks";
import type { Provider } from "../api/types";
import { genericTerms, termsFor, type Terms } from "../lib/terminology";
import { currentLocale } from "../i18n";

/**
 * Términos por proveedor en el idioma activo, a partir de GET /providers.
 * `terms(kind)` devuelve los del provider; sin kind, los genéricos.
 */
export function useTerms(org: string) {
  const { i18n } = useTranslation();
  const providers = useProviders(org);
  const list = providers.data;
  const terms = useCallback(
    (kind?: string | null): Terms => {
      void i18n.language;
      if (!kind) return genericTerms(currentLocale());
      const p: Provider | undefined = list?.find((x) => x.kind === kind);
      return termsFor(p, currentLocale());
    },
    [list, i18n.language],
  );
  return { terms, providers: list, isLoading: providers.isPending };
}

/** Términos del provider de la cuenta de nube de un cluster (o genéricos si no hay cuenta). */
export function useClusterTerms(org: string) {
  const { terms } = useTerms(org);
  const accounts = useCloudAccounts(org);
  return useCallback(
    (cluster: { cloud_account_id?: string | null } | undefined | null): Terms => {
      const provider = accounts.data?.find((a) => a.id === cluster?.cloud_account_id)?.provider;
      return terms(provider);
    },
    [terms, accounts.data],
  );
}
