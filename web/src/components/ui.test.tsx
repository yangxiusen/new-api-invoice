import { it, expect, vi } from "vitest";
import { render, fireEvent, screen, cleanup } from "@testing-library/react";
import { Modal } from "./ui";
it("keeps an inline modal form outside its parent and isolates submit events", () => {
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value() {
      this.setAttribute("open", "");
    },
  });
  const parent = vi.fn((e: React.FormEvent) => e.preventDefault()),
    child = vi.fn((e: React.FormEvent) => e.preventDefault());
  render(
    <form onSubmit={parent}>
      <Modal title="新增抬头" onClose={() => undefined}>
        <form aria-label="抬头表单" onSubmit={child}>
          <input name="company_name" />
          <button type="submit">保存</button>
        </form>
      </Modal>
    </form>,
  );
  const inner = screen.getByRole("form", { name: "抬头表单" });
  expect(inner.parentElement?.closest("form")).toBeNull();
  fireEvent.submit(inner);
  expect(child).toHaveBeenCalledTimes(1);
  expect(parent).not.toHaveBeenCalled();
  cleanup();
});
