#!/usr/bin/env bash
#
# converge-pooler.sh SECRET_FILE -- PSQL_COMMAND...
#
# Create or update the pooler's login role and the SECURITY DEFINER function it
# resolves application users through. Idempotent; safe on every start.
#
# Shared by the standalone image's converge stage (50-converge.sh) and the
# Patroni image's role-change callback (design.md D-72), so both topologies run
# the same SQL. PSQL_COMMAND is how to reach the server as a superuser over the
# local socket; this appends the database and the options it relies on.
#
# The pooler authenticates application users with auth_query rather than
# holding their passwords. It therefore needs:
#
#   1. a login role of its own -- the ONLY credential the pooler ever stores;
#   2. a way to read password verifiers, which lives in pg_shadow and is
#      superuser-only.
#
# (2) is granted through a SECURITY DEFINER function rather than by making the
# pooler a superuser. The function is deliberately narrow: it takes a single
# username and returns that one row. Note the WHERE clause excludes superusers,
# so a compromised pooler role cannot obtain the superuser's verifier.
#
# search_path is pinned inside the function because a SECURITY DEFINER function
# that resolves unqualified names through the caller's search_path is a
# privilege-escalation bug (CVE-2018-1058 is the canonical example).

set -euo pipefail

secret="${1:?usage: converge-pooler.sh SECRET_FILE -- PSQL_COMMAND...}"
shift
[[ "${1:-}" == "--" ]] && shift
(( $# > 0 )) || { echo "converge-pooler.sh: no psql command given" >&2; exit 2; }
[[ -r "$secret" ]] || { echo "converge-pooler.sh: cannot read $secret" >&2; exit 1; }

pw="$(tr -d '\n' < "$secret")"
esc_pw="${pw//\'/\'\'}"

"$@" -v ON_ERROR_STOP=1 -q --no-psqlrc -d postgres <<SQL
DO \$mdb\$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pgbouncer') THEN
        ALTER ROLE pgbouncer WITH LOGIN PASSWORD '${esc_pw}';
    ELSE
        CREATE ROLE pgbouncer WITH LOGIN PASSWORD '${esc_pw}';
    END IF;
END
\$mdb\$;

CREATE SCHEMA IF NOT EXISTS pgbouncer AUTHORIZATION pgbouncer;

CREATE OR REPLACE FUNCTION pgbouncer.get_auth(p_username TEXT)
RETURNS TABLE(username TEXT, password TEXT)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog
AS \$mdb\$
    SELECT usename::TEXT, passwd::TEXT
      FROM pg_shadow
     WHERE usename = p_username
       AND NOT usesuper;
\$mdb\$;

REVOKE ALL ON FUNCTION pgbouncer.get_auth(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pgbouncer.get_auth(TEXT) TO pgbouncer;
SQL
