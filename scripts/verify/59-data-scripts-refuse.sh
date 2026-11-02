#!/usr/bin/env bash
# verify: backup honours MDB_BACKUP_DIR from .env, restore refuses an unverifiable dump, upgrade fails when its restore fails
# tags: fast backup
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# Three data-path guarantees, tested against a stub container CLI in a throwaway
# copy of scripts/ and engines/, so no real backups/, secrets/ or volume is in
# reach:
#
#   1. scripts/backup writes to MDB_BACKUP_DIR when it is set in .env, not only
#      when it is in the environment (it used to ignore .env, so the configured
#      directory was not the one written to, or pruned);
#   2. scripts/restore refuses a dump with no .sha256 alongside before touching
#      the database, unless MDB_RESTORE_ALLOW_UNVERIFIED=yes;
#   3. scripts/upgrade exits non-zero when restoring into the new major fails
#      (it ended that step in `|| true`).

tmp="$(mktemp -d "${TMPDIR:-/tmp}/mdb-datascripts.XXXXXX")"
add_cleanup "rm -rf '$tmp'"
root="$tmp/root"
mkdir -p "$root/secrets" "$tmp/bin"
cp -R "$MDB_ROOT/scripts" "$MDB_ROOT/engines" "$root/"
cp "$MDB_ROOT/.env.example" "$root/.env.example"
echo throwaway > "$root/secrets/pg_superuser_password.txt"
echo throwaway > "$root/secrets/mysql_root_password.txt"

stub="$tmp/bin/ctr"
cat > "$stub" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$STUB_LOG"
case "$*" in
    *pg_dumpall*|*mysqldump*) echo 'SELECT 1;' ;;
    *"exec -i "*"psql -U postgres -q"*|*"exec -i "*"mysql -uroot"*)
        cat >/dev/null; echo "restore: connection lost" >&2; exit "${STUB_RESTORE_RC:-0}" ;;
    *"-tAc"*|*"-N -B"*) echo 3 ;;
esac
exit 0
STUB
chmod +x "$stub"
log="$tmp/calls.log"

# run <env assignments...> -- <command...>, in the throwaway root
run() {
    local -a envs=()
    while [[ "$1" != -- ]]; do envs+=("$1"); shift; done; shift
    : > "$log"
    ( cd "$root" && env -i PATH="/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin:/usr/local/bin" HOME="$HOME" \
        STUB_LOG="$log" MDB_RUNTIME=docker MDB_CTR_CMD="$stub" MDB_COMPOSE_CMD="$stub compose" \
        MDB_COMPOSE_FLAVOR=compose-v2 MDB_BACKEND=compose CONFIRM=yes ${envs[@]+"${envs[@]}"} "$@" ) 2>&1
}

# --- 1. backup: MDB_BACKUP_DIR from .env ------------------------------------------
printf 'COMPOSE_PROJECT_NAME=proj\nMDB_BACKUP_DIR="%s"\n' "$tmp/dumps here" > "$root/.env"
run -- scripts/backup --engine nosuchengine >/dev/null || true
[[ -d "$tmp/dumps here" ]] || vfail "scripts/backup ignored MDB_BACKUP_DIR from .env"
[[ -d "$root/backups" ]] && vfail "scripts/backup used ./backups although .env names another directory"
printf 'COMPOSE_PROJECT_NAME=proj\nMDB_BACKUP_DIR=relative-dumps\n' > "$root/.env"
run -- scripts/backup --engine nosuchengine >/dev/null || true
[[ -d "$root/relative-dumps" ]] || vfail "a relative MDB_BACKUP_DIR is not resolved against the checkout"
run "MDB_BACKUP_DIR=$tmp/from-env" -- scripts/backup --engine nosuchengine >/dev/null || true
[[ -d "$tmp/from-env" ]] || vfail "the environment's MDB_BACKUP_DIR no longer wins over .env"
vinfo "backup: MDB_BACKUP_DIR from .env (quoted or relative) is used; the environment still wins"

# --- 2. restore: no checksum, no restore -------------------------------------------
printf 'COMPOSE_PROJECT_NAME=proj\n' > "$root/.env"
printf 'SELECT 1;\n' > "$tmp/pg_x_unverified.sql"
rc=0; out="$(run -- scripts/restore --engine pg --db x --file "$tmp/pg_x_unverified.sql")" || rc=$?
(( rc != 0 )) || vfail "restore of a dump with no .sha256 exited 0: $(tail -1 <<<"$out")"
[[ "$out" == *"no checksum"* ]] || vfail "the refusal does not say why: $(tail -2 <<<"$out")"
if grep -q ' exec ' "$log"; then vfail "restore reached the database before refusing: $(grep -m1 ' exec ' "$log")"; fi
run MDB_RESTORE_ALLOW_UNVERIFIED=yes -- scripts/restore --engine pg --db x --file "$tmp/pg_x_unverified.sql" >/dev/null 2>&1 || true
grep -q ' exec ' "$log" || vfail "MDB_RESTORE_ALLOW_UNVERIFIED=yes did not let the restore proceed"
vinfo "restore: refuses a dump with no checksum before touching the database; MDB_RESTORE_ALLOW_UNVERIFIED=yes overrides"

# --- 3. upgrade: a failed restore fails the upgrade --------------------------------
for engine in pg mysql; do
    rc=0; out="$(run STUB_RESTORE_RC=1 -- scripts/upgrade --engine "$engine" --from 1 --to 2)" || rc=$?
    (( rc != 0 )) || vfail "$engine: upgrade exited 0 although the restore failed: $(tail -1 <<<"$out")"
    [[ "$out" == *"restore into $engine 2 failed"* && "$out" == *"connection lost"* ]] \
        || vfail "$engine: the failure does not name the step and show the client's error: $(tail -3 <<<"$out")"
    rc=0; out="$(run -- scripts/upgrade --engine "$engine" --from 1 --to 2)" || rc=$?
    (( rc == 0 )) || vfail "$engine: a successful stubbed upgrade failed: $(tail -3 <<<"$out")"
done
vinfo "upgrade: a failed restore exits non-zero with the client's error; a good one still succeeds"
