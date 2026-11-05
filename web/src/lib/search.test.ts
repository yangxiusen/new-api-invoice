import { it, expect } from "vitest";
import { parseSearch, stringifySearch } from "./search";
it("keeps deep-linked IDs as exact decimal strings", () => {
  expect(parseSearch("?user_id=9007199254740993").user_id).toBe("9007199254740993");
  expect(stringifySearch({ user_id: "2" })).toBe("?user_id=2");
});
