# Row-level security kit

Database-enforced multi-tenancy for PostgreSQL, and the Laravel wiring that
drives it.

## Why enforce it here rather than in the application

An ORM scope you forget to apply leaks every tenant's data. A policy you forget
to apply returns nothing. One fails open, the other fails closed — that is the
entire argument, and it is why the tenant filter belongs in the database even
when the application also applies it.

## Files

| File | What it is |
|---|---|
| `rls-template.sql` | The policy template: tenant accessor, an example table, `ENABLE` + `FORCE`, and both `USING` and `WITH CHECK` policies |
| `roles.md` | Which role does what, and why the app role must not own the table it is restricted by |

## Apply it

```bash
scripts/runtime compose exec -T pg psql -U postgres -d mydb < rls/rls-template.sql
```

## The two mistakes that make RLS silently do nothing

**Forgetting `FORCE`.** `ALTER TABLE x ENABLE ROW LEVEL SECURITY` does not
apply to the table's owner, and the owner is usually the exact role your
application connects as. The policy exists, the tests you write as a superuser
pass, and every tenant sees every row. `FORCE ROW LEVEL SECURITY` is what closes
it.

**Forgetting `WITH CHECK`.** `USING` controls what is *visible*. Without a
matching `WITH CHECK`, a tenant can `INSERT` rows labelled with someone else's
`tenant_id` — writable but invisible to them afterwards, which is harder to
diagnose than a plain refusal.

## Laravel

Set the tenant per request, inside the transaction:

```php
// app/Http/Middleware/SetTenant.php
public function handle(Request $request, Closure $next)
{
    $tenantId = $request->user()?->tenant_id;

    if ($tenantId !== null) {
        // SET LOCAL, not SET: it is discarded at commit, so a pooled
        // connection cannot carry one tenant's identity into the next
        // request. This is what makes it safe behind pgBouncer in
        // transaction pooling mode.
        DB::statement('SET LOCAL app.tenant_id = ?', [$tenantId]);
    }

    return $next($request);
}
```

> `SET LOCAL` only has effect inside a transaction. If your request is not
> already wrapped in one, wrap it — otherwise the setting is discarded
> immediately and every query legitimately returns nothing.

Models then need no global scope at all, because the database applies it:

```php
class Document extends Model
{
    // No tenant scope. The policy is the scope, and it cannot be forgotten
    // on a raw query, a report, or a console command.
}
```

## Verify it

```sql
SET LOCAL app.tenant_id = '00000000-0000-0000-0000-000000000001';
SELECT count(*) FROM documents;   -- tenant 1 only

SET LOCAL app.tenant_id = '00000000-0000-0000-0000-000000000002';
SELECT count(*) FROM documents;   -- tenant 2 only

RESET app.tenant_id;
SELECT count(*) FROM documents;   -- 0: an unset tenant sees nothing
```

The last case is the one to check first. If an unset tenant sees rows, `FORCE`
is missing.

---

[← Docs index](../README.md#documentation)
