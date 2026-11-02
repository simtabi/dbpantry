# Roles under row-level security

Which role does what, and the ownership trap that makes RLS look like it works
when it does not.

## The roles

| Role | Created by | Purpose | RLS applies? |
|---|---|---|---|
| `postgres` | the image | Maintenance only, via `make` targets | No — superusers bypass RLS entirely |
| `<project>_user` | `make new-project` | The application connects as this | Yes, **provided `FORCE` is set** |
| `<project>_user_readonly` | `make new-project` | Reporting, analytics, read replicas | Yes |

## Superusers bypass RLS, always

`postgres` is not subject to any policy, and there is no setting that changes
that. This is the reason an RLS test run as `postgres` proves nothing: it will
pass whether or not your policies are correct.

Test as the application role. `make psql` connects as `postgres` by design, so
for RLS work connect explicitly:

```bash
scripts/runtime compose exec -T -e PGPASSWORD="$(cat secrets/pg_myapp_user_password.txt)" \
    pg psql -U myapp_user -d myapp
```

## The ownership trap

`ENABLE ROW LEVEL SECURITY` does not apply to the table's owner. `make
new-project` makes `<project>_user` the database owner, so tables it creates
are owned by the same role the application connects as — and the policy is
skipped for exactly that role.

Two ways out, and the toolkit takes the first:

1. **`FORCE ROW LEVEL SECURITY`** on every protected table. One line, applies
   to the owner too, and `rls-template.sql` includes it.
2. Separate the owner from the connecting role: a migration role owns the
   schema, the application role only has DML. More moving parts, and worth it
   only when you already run migrations under a distinct identity.

## Grants the policy still needs

RLS narrows what a role can see; it does not grant anything. The role still
needs ordinary privileges:

```sql
GRANT USAGE ON SCHEMA public TO myapp_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON documents TO myapp_user;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO myapp_user;
```

A role with a correct policy and no `SELECT` grant gets a permission error, not
an empty result — which is at least an honest failure.

---

[← Docs index](../readme.md#documentation)
