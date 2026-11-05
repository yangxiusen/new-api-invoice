DO $invoice$ BEGIN IF to_regclass('public.invoice_titles') IS NULL THEN
CREATE TABLE IF NOT EXISTS invoice_titles (
  id bigserial PRIMARY KEY,
  user_id bigint,
  title_type varchar(32) DEFAULT 'enterprise'::character varying,
  company_name varchar(255),
  tax_no varchar(64),
  email varchar(255),
  registered_address varchar(512) DEFAULT ''::character varying,
  registered_phone varchar(64) DEFAULT ''::character varying,
  bank_name varchar(255) DEFAULT ''::character varying,
  bank_account varchar(128) DEFAULT ''::character varying,
  created_at bigint,
  updated_at bigint
);
CREATE INDEX IF NOT EXISTS idx_invoice_titles_user_company ON public.invoice_titles USING btree (user_id, company_name);
CREATE INDEX IF NOT EXISTS idx_invoice_titles_user_id ON public.invoice_titles USING btree (user_id);
END IF; END $invoice$;

DO $invoice$ BEGIN IF to_regclass('public.invoice_applications') IS NULL THEN
CREATE TABLE IF NOT EXISTS invoice_applications (
  id bigserial PRIMARY KEY,
  request_no varchar(64),
  user_id bigint,
  status varchar(32),
  total_amount_cents bigint,
  currency varchar(16) DEFAULT 'CNY'::character varying,
  title_type varchar(32) DEFAULT 'enterprise'::character varying,
  company_name varchar(255),
  tax_no varchar(64),
  email varchar(255),
  registered_address varchar(512) DEFAULT ''::character varying,
  registered_phone varchar(64) DEFAULT ''::character varying,
  bank_name varchar(255) DEFAULT ''::character varying,
  bank_account varchar(128) DEFAULT ''::character varying,
  user_remark varchar(512) DEFAULT ''::character varying,
  admin_remark varchar(512) DEFAULT ''::character varying,
  invoice_file_name varchar(255) DEFAULT ''::character varying,
  invoice_file_path varchar(512) DEFAULT ''::character varying,
  invoice_file_mime varchar(128) DEFAULT ''::character varying,
  invoice_file_size bigint,
  invoiced_at bigint,
  created_at bigint,
  updated_at bigint
);
CREATE INDEX IF NOT EXISTS idx_invoice_app_status_created ON public.invoice_applications USING btree (status, created_at);
CREATE INDEX IF NOT EXISTS idx_invoice_app_user_status_created ON public.invoice_applications USING btree (user_id, status, created_at);
CREATE INDEX IF NOT EXISTS idx_invoice_applications_company_name ON public.invoice_applications USING btree (company_name);
CREATE UNIQUE INDEX IF NOT EXISTS idx_invoice_applications_request_no ON public.invoice_applications USING btree (request_no);
END IF; END $invoice$;

DO $invoice$ BEGIN IF to_regclass('public.invoice_application_items') IS NULL THEN
CREATE TABLE IF NOT EXISTS invoice_application_items (
  id bigserial PRIMARY KEY,
  application_id bigint,
  user_id bigint,
  top_up_id bigint,
  trade_no varchar(255),
  payment_method varchar(50) DEFAULT ''::character varying,
  payment_provider varchar(50) DEFAULT ''::character varying,
  top_up_create_time bigint,
  top_up_complete_time bigint,
  top_up_amount bigint,
  pay_money varchar(64),
  amount_cents bigint,
  active boolean,
  active_key varchar(64),
  created_at bigint
);
CREATE INDEX IF NOT EXISTS idx_invoice_application_items_application_id ON public.invoice_application_items USING btree (application_id);
CREATE INDEX IF NOT EXISTS idx_invoice_application_items_trade_no ON public.invoice_application_items USING btree (trade_no);
CREATE INDEX IF NOT EXISTS idx_invoice_application_items_user_id ON public.invoice_application_items USING btree (user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_invoice_items_topup_active_key ON public.invoice_application_items USING btree (top_up_id, active_key);

END IF; END $invoice$;
