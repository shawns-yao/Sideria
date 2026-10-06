import { describe, it, expect } from "vitest";
import { linePath, memory, bytes, hostSchema } from "./model";
import { demoHosts } from "../demo/hosts";
describe("telemetry semantics", () => {
  it("keeps gaps and resets instead of joining fabricated samples", () => {
    expect(
      linePath(
        [
          { at: 0, value: 3 },
          { at: 5000, value: null },
          { at: 10000, value: 4 },
        ],
        100,
        20,
      ),
    ).toBe("M0.0,5.0  M100.0,0.0");
    expect(
      linePath(
        [
          { at: 0, value: 1 },
          { at: 30000, value: 2 },
        ],
        100,
        20,
      ),
    ).toContain("M100.0");
  });
  it("uses available memory, preserves unknown and validates incoming percentages", () => {
    expect(memory(null)).toBeNull();
    expect(memory(demoHosts[0]!.snapshot)).toBeCloseTo(42.5);
    expect(bytes(null)).toBe("—");
    expect(
      hostSchema.safeParse({
        ...demoHosts[0],
        snapshot: { ...demoHosts[0]!.snapshot, cpu: 101 },
      }).success,
    ).toBe(false);
  });
});
