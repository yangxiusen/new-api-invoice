import { it, expect } from "vitest";
import { amount, sumCents } from "./format";
it("preserves integer cents above the JS safe integer limit", () => {
  expect(amount("9007199254740993")).toBe("¥90,071,992,547,409.93");
  expect(sumCents(["9007199254740993", "7"])).toBe("9007199254741000");
});
