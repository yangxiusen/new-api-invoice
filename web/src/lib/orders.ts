type Amount = { id: string; amount_cents: string };
export function checkSelection(selected: Amount[], current: Amount[]): Record<string, string> {
  const available = new Map(current.map((o) => [o.id, o.amount_cents]));
  if (selected.some((o) => available.get(o.id) !== o.amount_cents))
    throw new Error("所选订单的金额或可用状态已变化，请返回选单重新核对。");
  return Object.fromEntries(selected.map((o) => [o.id, o.amount_cents]));
}
