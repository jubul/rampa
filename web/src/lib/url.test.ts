import { describe, expect, it } from "vitest";
import { safeHttpUrl } from "./url";

describe("safeHttpUrl", () => {
  it("acepta http y https sin importar mayúsculas", () => {
    expect(safeHttpUrl("https://github.com/acme/x")).toBe("https://github.com/acme/x");
    expect(safeHttpUrl("HTTP://example.com")).toBe("HTTP://example.com");
  });

  it("rechaza esquemas peligrosos o ausentes", () => {
    expect(safeHttpUrl("javascript:alert(1)")).toBeNull();
    expect(safeHttpUrl("data:text/html,<script>1</script>")).toBeNull();
    expect(safeHttpUrl("file:///etc/passwd")).toBeNull();
    expect(safeHttpUrl("  javascript:alert(1)")).toBeNull();
    expect(safeHttpUrl("git@github.com:acme/x.git")).toBeNull();
    expect(safeHttpUrl("")).toBeNull();
    expect(safeHttpUrl(undefined)).toBeNull();
  });
});
