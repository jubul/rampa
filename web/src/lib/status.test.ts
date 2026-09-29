import { describe, expect, it } from "vitest";
import { isDeploymentActive, labelForStatus, toneForStatus } from "./status";

describe("toneForStatus", () => {
  it("respeta la paleta pedida", () => {
    expect(toneForStatus("healthy")).toBe("green");
    expect(toneForStatus("failed")).toBe("red");
    expect(toneForStatus("progressing")).toBe("amber");
    expect(toneForStatus("applying")).toBe("amber");
    expect(toneForStatus("queued")).toBe("amber");
    expect(toneForStatus("awaiting_approval")).toBe("violet");
    expect(toneForStatus("offline")).toBe("gray");
    expect(toneForStatus("online")).toBe("green");
  });
  it("cae en gris para desconocidos", () => {
    expect(toneForStatus("lo-que-sea")).toBe("gray");
    expect(toneForStatus(undefined)).toBe("gray");
  });
});

describe("labelForStatus", () => {
  it("traduce y hace fallback al valor crudo", () => {
    expect(labelForStatus("awaiting_approval")).toBe("Espera aprobación");
    expect(labelForStatus("raro")).toBe("raro");
    expect(labelForStatus(null)).toBe("-");
  });
});

describe("isDeploymentActive", () => {
  it("solo los estados no terminales", () => {
    for (const s of ["awaiting_approval", "queued", "applying", "progressing"]) expect(isDeploymentActive(s)).toBe(true);
    for (const s of ["healthy", "failed", "cancelled"]) expect(isDeploymentActive(s)).toBe(false);
  });
});
