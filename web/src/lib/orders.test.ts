import { it, expect } from "vitest";
import { checkSelection } from "./orders";
it("blocks confirmation when an order disappeared or its amount changed", () => {
  const selected = [
    { id: "1", amount_cents: "1000" },
    { id: "2", amount_cents: "2000" },
  ];
  expect(() => checkSelection(selected, [{ id: "1", amount_cents: "1000" }])).toThrow();
  expect(() =>
    checkSelection(selected, [
      { id: "1", amount_cents: "2000" },
      { id: "2", amount_cents: "2000" },
    ]),
  ).toThrow();
  expect(checkSelection(selected, selected)).toEqual({ "1": "1000", "2": "2000" });
});
