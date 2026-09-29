import { describe, expect, it } from "vitest";
import {
  defaultScope,
  defaultSpec,
  groupDetailsByStep,
  localErrors,
  normalizeSpec,
  stepForDetail,
  toggleScope,
  validateAppName,
  validateParamName,
} from "./spec";

describe("defaultSpec", () => {
  it("usa los defaults del manifest", () => {
    const s = defaultSpec();
    expect(s.tier).toBe(2);
    expect(s.port).toBe(8080);
    expect(s.healthcheck).toBe("/health");
    expect(s.type).toBe("api");
    expect(s.scopes.map((x) => x.name)).toEqual(["dev", "prod", "pr"]);
    expect(s.scopes.find((x) => x.name === "pr")?.ttl).toBe("72h");
    expect(s.scopes.find((x) => x.name === "dev")).toMatchObject({ size: "S", replicas: 1, strategy: "rolling" });
  });

  it("devuelve objetos independientes", () => {
    const a = defaultSpec();
    a.scopes[0]!.size = "XL";
    expect(defaultSpec().scopes[0]!.size).toBe("S");
  });
});

describe("validateAppName", () => {
  it("acepta nombres dns-safe", () => {
    for (const n of ["checkout-api", "a", "api2", "x-y-z"]) expect(validateAppName(n)).toBeNull();
  });
  it("rechaza los inválidos", () => {
    for (const n of ["", "Checkout", "1api", "api-", "-api", "api_v2", "con espacio", "a".repeat(41)]) {
      expect(validateAppName(n)).not.toBeNull();
    }
  });
});

describe("validateParamName", () => {
  it("exige mayúsculas", () => {
    expect(validateParamName("LOG_LEVEL")).toBeNull();
    expect(validateParamName("log_level")).not.toBeNull();
    expect(validateParamName("1X")).not.toBeNull();
    expect(validateParamName("")).not.toBeNull();
  });
});

describe("toggleScope", () => {
  it("mantiene el orden y conserva la configuración", () => {
    const s = defaultSpec().scopes;
    const withStaging = toggleScope(s, "staging", true);
    expect(withStaging.map((x) => x.name)).toEqual(["dev", "staging", "prod", "pr"]);
    expect(toggleScope(withStaging, "dev", false).map((x) => x.name)).toEqual(["staging", "prod", "pr"]);
    expect(defaultScope("staging").replicas).toBe(2);
  });
});

describe("normalizeSpec", () => {
  it("vacía lo que no aplica al tipo", () => {
    const s = { ...defaultSpec(), type: "worker", schedule: "* * * * *" };
    const n = normalizeSpec(s);
    expect(n.port).toBe(0);
    expect(n.healthcheck).toBe("");
    expect(n.schedule).toBe("");
  });
  it("limpia parameters según origen y descarta scopes que no existen", () => {
    const s = defaultSpec();
    s.parameters = [
      { name: "A", source: "secret", value: "x", service: "y", scopes: ["dev", "staging"] },
      { name: "B", source: "service", service: "db", key: " url ", value: "x", scopes: [] },
      { name: "  ", source: "value", value: "", scopes: [] },
    ];
    const n = normalizeSpec(s);
    expect(n.parameters).toEqual([
      { name: "A", source: "secret", scopes: ["dev"] },
      { name: "B", source: "service", service: "db", key: "url", scopes: [] },
    ]);
  });
  it("solo prod lleva dominio y solo pr lleva ttl", () => {
    const s = defaultSpec();
    s.scopes[0]!.domain = "x.com";
    s.scopes[1]!.domain = "prod.com";
    s.scopes[1]!.ttl = "1h";
    const n = normalizeSpec(s);
    expect(n.scopes[0]!.domain).toBe("");
    expect(n.scopes[1]).toMatchObject({ domain: "prod.com", ttl: "" });
    expect(n.scopes[2]!.ttl).toBe("72h");
  });
});

describe("errores por paso", () => {
  it("asocia detalles de la API al paso", () => {
    expect(stepForDetail("spec.scopes.prod.replicas debe ser mayor a 0")).toBe("scopes");
    expect(stepForDetail("spec.parameters[2].name inválido")).toBe("parameters");
    expect(stepForDetail("spec.parameters[1].fromService no existe")).toBe("parameters");
    expect(stepForDetail("spec.services[0].type no soportado")).toBe("services");
    expect(stepForDetail("metadata.name inválido")).toBe("basics");
    const g = groupDetailsByStep(["spec.type x", "spec.scopes y"]);
    expect(g.basics).toHaveLength(1);
    expect(g.scopes).toHaveLength(1);
  });
  it("valida local lo mínimo", () => {
    const e = localErrors(defaultSpec());
    expect(e.basics.length).toBe(2);
    const s = { ...defaultSpec(), name: "ok", team: "t" };
    expect(localErrors(s).basics).toHaveLength(0);
  });
});
