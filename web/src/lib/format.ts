export function amount(raw: string | null | undefined) {
  if (raw == null) return "—";
  try {
    const n = BigInt(raw);
    const sign = n < 0n ? "-" : "";
    const a = n < 0n ? -n : n;
    return `${sign}¥${(a / 100n).toLocaleString("en-US")}.${(a % 100n).toString().padStart(2, "0")}`;
  } catch {
    return "金额异常";
  }
}
export const sumCents = (values: string[]) => values.reduce((s, v) => s + BigInt(v), 0n).toString();
export const date = (unix: number | null | undefined) =>
  unix
    ? new Intl.DateTimeFormat("zh-CN", {
        timeZone: "Asia/Shanghai",
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      }).format(new Date(unix * 1000))
    : "—";
export const states = {
  pending: "待开票",
  invoiced: "已开票",
  rejected: "已驳回",
  cancelled: "已取消",
};
export const operations: Record<string, string> = {
  permission_import: "旧权限继承",
  permission_enable: "开通开票",
  permission_revoke: "撤销开票",
  title_create: "新增抬头",
  title_update: "修改抬头",
  title_delete: "删除抬头",
  title_create_admin: "代建抬头",
  create_user: "自助申请",
  create_admin: "管理员代办",
  cancel_user: "用户取消",
  reject_admin: "驳回申请",
  complete_admin: "上传并完成",
};
