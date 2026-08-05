-- Sub2API Usage Viewer role template for PostgreSQL.
--
-- DBA action required before use:
--   1. Replace <sub2api_database> with the existing Sub2API database name.
--   2. Set the role password through a secure interactive DBA channel, such as
--      psql's \password command. Do not put a password in this file.
--
-- This template changes only role attributes and access-control entries. It
-- does not change application tables, columns, indexes, policies, or rows.

CREATE ROLE sub2api_usage_viewer
    LOGIN
    NOSUPERUSER
    NOCREATEDB
    NOCREATEROLE
    NOINHERIT
    NOREPLICATION
    NOBYPASSRLS;

ALTER ROLE sub2api_usage_viewer SET default_transaction_read_only = on;

GRANT CONNECT ON DATABASE <sub2api_database> TO sub2api_usage_viewer;
GRANT USAGE ON SCHEMA public TO sub2api_usage_viewer;

GRANT SELECT (id, key, name, group_id, quota, quota_used, last_used_at, expires_at, status, created_at, deleted_at) ON TABLE public.api_keys TO sub2api_usage_viewer;
GRANT SELECT (id, name) ON TABLE public.groups TO sub2api_usage_viewer;
GRANT SELECT (id, api_key_id, actual_cost, created_at) ON TABLE public.usage_logs TO sub2api_usage_viewer;

-- The viewer intentionally rejects broader effective authority. Before launch,
-- verify that this role has no memberships, ownership, CREATE or TEMP rights,
-- table-wide reads, writes, sequence privileges, grant options, unexpected
-- column reads, or access to user-schema SECURITY DEFINER routines. PostgreSQL
-- PUBLIC privileges apply to every role and can also cause admission to fail.
-- Review and remediate surrounding authority deliberately; this template does
-- not revoke global privileges because that could disrupt the existing service.
--
-- To retire the viewer, terminate its sessions, revoke the three grants above
-- plus schema USAGE and database CONNECT, then remove the role using the DBA's
-- normal audited role-removal procedure.
