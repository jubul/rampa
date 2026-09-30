import { afterEach, describe, expect, it } from "vitest";
import i18n from "../i18n";
import type { Provider } from "../api/types";
import { cap, genericTerms, termsFor } from "./terminology";

const digitalocean: Provider = {
  kind: "digitalocean",
  display_name: "DigitalOcean",
  available: true,
  auth: "token",
  terminology: {
    account: { es: "cuenta de DigitalOcean", en: "DigitalOcean account" },
    cluster: { es: "cluster DOKS", en: "DOKS cluster" },
    vm: { es: "Droplet", en: "Droplet" },
    registry: { es: "DOCR", en: "DOCR" },
  },
};

describe("terminología por proveedor", () => {
  afterEach(async () => {
    await i18n.changeLanguage("es");
  });

  it("usa los términos del provider según el idioma activo", async () => {
    expect(termsFor(digitalocean).cluster).toBe("cluster DOKS");
    expect(termsFor(digitalocean).vm).toBe("Droplet");
    await i18n.changeLanguage("en");
    expect(termsFor(digitalocean).account).toBe("DigitalOcean account");
  });

  it("cae al genérico para lo que el provider no trae o sin provider", async () => {
    expect(termsFor(digitalocean).secretStore).toBe(genericTerms().secretStore);
    expect(termsFor(undefined)).toEqual(genericTerms());
    expect(genericTerms("es").vm).toBe("máquina virtual");
    expect(genericTerms("en").vm).toBe("virtual machine");
  });

  it("capitaliza", () => {
    expect(cap("cuenta de AWS")).toBe("Cuenta de AWS");
    expect(cap("")).toBe("");
  });
});
