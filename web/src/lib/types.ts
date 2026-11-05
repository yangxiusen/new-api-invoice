export type Me = {
  user: { id: string; username: string; display_name: string; role: number; status: number };
  invoice_permission: { enabled: boolean; version: string; changed_at: number | null };
  capabilities: {
    can_submit: boolean;
    can_manage_titles: boolean;
    can_read_history: boolean;
    can_admin: boolean;
  };
};
export type Page<T> = { items: T[]; total: number; page: number; page_size: number };
export type TitleInput = {
  company_name: string;
  tax_no: string;
  email: string;
  registered_address: string;
  registered_phone: string;
  bank_name: string;
  bank_account: string;
};
export type Title = TitleInput & {
  id: string;
  user_id: string;
  title_type: string;
  created_at: number | null;
  updated_at: number | null;
};
export type Order = {
  id: string;
  trade_no: string;
  payment_method: string;
  payment_provider: string;
  pay_money: string;
  amount_cents: string;
  currency: string;
  create_time: number | null;
  complete_time: number | null;
  payment_time: number | null;
  payment_time_source: "complete" | "create_fallback";
};
export type State = "pending" | "invoiced" | "rejected" | "cancelled";
export type Application = {
  id: string;
  request_no: string;
  user_id: string;
  company_name: string;
  total_amount_cents: string;
  currency: string;
  status: State;
  item_count: number;
  created_at: number | null;
  invoiced_at: number | null;
  username?: string | null;
  display_name?: string | null;
};
export type Detail = Application &
  TitleInput & {
    title_type: string;
    user_remark: string;
    admin_remark: string;
    items: {
      id: string;
      topup_id: string;
      trade_no: string;
      pay_money: string;
      amount_cents: string;
      payment_method: string;
      payment_provider: string;
      topup_create_time: number | null;
      topup_complete_time: number | null;
      payment_time_source: string;
    }[];
    file: { name: string; mime: string; size: string; downloadable: boolean } | null;
    creation_source?: "user" | "admin" | "legacy";
    operator?: { id: string | null; name: string } | null;
  };
export type User = {
  id: string;
  username: string;
  display_name: string;
  email: string;
  account_status: "enabled" | "disabled";
  invoice_enabled: boolean;
  permission_version: string;
  permission_changed_at: number | null;
  permission_source: string | null;
  can_change_permission: boolean;
  can_create_application: boolean;
};
export type Operation = {
  id: string;
  occurred_at: number;
  actor_type: string;
  actor_user_id: string | null;
  actor_name: string;
  target_user_id: string;
  operation_type: string;
  application_id: string | null;
  request_no: string | null;
  before_state: string;
  after_state: string;
  remark: string;
  result: "success";
};
export type WriteResult = {
  resource_type: string;
  resource_id: string;
  request_no?: string;
  operation_id: string | null;
  permission_version?: string;
  changed: boolean;
};
