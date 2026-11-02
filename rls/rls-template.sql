-- dbpantry — PostgreSQL row-level security template (SPEC section 8)
--
-- Multi-tenancy enforced by the database rather than by the application. The
-- difference matters: an ORM scope you forget to apply leaks every tenant's
-- data, while a policy you forget to apply returns nothing. One fails open,
-- the other fails closed.
--
-- The tenant is carried in a session variable set per request, NOT in the
-- connection, so a pooled connection reused by another tenant cannot inherit
-- the previous one's identity.
--
-- Apply with:
--   make psql < rls/rls-template.sql
-- or, for a specific project database:
--   scripts/runtime compose exec -T pg psql -U postgres -d mydb < rls/rls-template.sql

BEGIN;

-- ---------------------------------------------------------------------------
-- 1. The tenant accessor
-- ---------------------------------------------------------------------------
-- current_setting(..., true) returns NULL rather than raising when the setting
-- is absent. That is deliberate: an unset tenant must match no rows, not error
-- in a way an application might catch and ignore.
CREATE SCHEMA IF NOT EXISTS app;

CREATE OR REPLACE FUNCTION app.current_tenant_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
    SELECT NULLIF(current_setting('app.tenant_id', true), '')::uuid;
$$;

COMMENT ON FUNCTION app.current_tenant_id() IS
    'The tenant for this session. NULL when unset, which matches no rows.';

-- ---------------------------------------------------------------------------
-- 2. An example tenant-scoped table
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS documents (
    id          bigserial PRIMARY KEY,
    tenant_id   uuid NOT NULL,
    title       text NOT NULL,
    body        text,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Every policy filters on tenant_id, so it wants an index. Without one, RLS
-- turns every query into a sequential scan.
CREATE INDEX IF NOT EXISTS documents_tenant_id_idx ON documents (tenant_id);

-- ---------------------------------------------------------------------------
-- 3. Enable RLS
-- ---------------------------------------------------------------------------
ALTER TABLE documents ENABLE ROW LEVEL SECURITY;

-- ENABLE alone does not apply to the table's OWNER, which is usually exactly
-- the role your application connects as. FORCE is what makes the policy apply
-- to the owner too, and leaving it out is the single most common way an RLS
-- setup silently does nothing.
ALTER TABLE documents FORCE ROW LEVEL SECURITY;

-- ---------------------------------------------------------------------------
-- 4. Policies
-- ---------------------------------------------------------------------------
DROP POLICY IF EXISTS documents_tenant_isolation ON documents;

CREATE POLICY documents_tenant_isolation ON documents
    USING (tenant_id = app.current_tenant_id())
    WITH CHECK (tenant_id = app.current_tenant_id());

-- USING governs what is visible to SELECT/UPDATE/DELETE.
-- WITH CHECK governs what INSERT/UPDATE may write.
-- Both are needed: USING alone would let a tenant INSERT rows labelled with
-- someone else's tenant_id -- writable but invisible, which is worse than
-- either being denied outright.

COMMIT;

-- ---------------------------------------------------------------------------
-- Usage, per request
-- ---------------------------------------------------------------------------
--   SET LOCAL app.tenant_id = '...uuid...';
--
-- SET LOCAL scopes it to the transaction, so it is discarded at commit. That is
-- what makes this safe behind pgBouncer in transaction pooling mode: the next
-- transaction on the same physical connection starts with no tenant, and
-- therefore sees nothing, rather than inheriting the previous tenant's.
--
-- Verify the isolation actually holds:
--
--   SET LOCAL app.tenant_id = '00000000-0000-0000-0000-000000000001';
--   SELECT count(*) FROM documents;   -- only tenant 1's rows
--   SET LOCAL app.tenant_id = '00000000-0000-0000-0000-000000000002';
--   SELECT count(*) FROM documents;   -- only tenant 2's rows
--   RESET app.tenant_id;
--   SELECT count(*) FROM documents;   -- 0 rows: unset tenant sees nothing
