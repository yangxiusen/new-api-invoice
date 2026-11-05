-- Run with the schema owner after migrations. The role must already exist.
-- psql -v runtime_role=invoice_runtime -f docs/runtime-grants.sql
-- Do not use an owner/superuser or a role inheriting broader host privileges.
GRANT USAGE ON SCHEMA public TO :"runtime_role";
GRANT SELECT(id,username,display_name,email,role,status,deleted_at,auth_version) ON public.users TO :"runtime_role";
GRANT SELECT(id,user_id,trade_no,money,amount,payment_method,payment_provider,create_time,complete_time,status) ON public.top_ups TO :"runtime_role";
GRANT SELECT,INSERT,UPDATE,DELETE ON public.invoice_titles,public.invoice_applications,public.invoice_application_items,public.invoice_user_permissions,public.invoice_write_requests TO :"runtime_role";
GRANT SELECT,INSERT ON public.invoice_operation_logs TO :"runtime_role";
GRANT SELECT ON public.invoice_schema_migrations,public.invoice_initializations TO :"runtime_role";
SELECT format('GRANT USAGE, SELECT ON SEQUENCE %s TO %I',pg_get_serial_sequence('public.'||t,'id'),:'runtime_role')
FROM unnest(ARRAY['invoice_titles','invoice_applications','invoice_application_items','invoice_operation_logs']) AS t
WHERE pg_get_serial_sequence('public.'||t,'id') IS NOT NULL
\gexec
-- Initializer additionally needs users.invoice_enabled, options.key/value,
-- and INSERT into invoice_initializations. Supply its own one-shot account.
-- Never grant UPDATE/DELETE on logs or any write/row-lock privilege on users/top_ups.
