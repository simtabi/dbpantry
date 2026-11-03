#!/usr/bin/env bash
# verify: checks clean up the volumes they create, count only their own project, wait adaptively, and never reach the operator's volumes or backups/
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# Guards for the verify suite itself, from defects found in real runs:
#
#   - check 20 created a socket volume per engine and removed it inline while
#     its container still held it, so the rm failed silently and every run
#     leaked two volumes. Every `volume create "$v"` must be tracked instead.
#   - check 15 counted every volume named mdb_*, so under an isolated project it
#     reported the operator's real volumes as having "survived make down".
#   - check 37 waited a flat 180s for pgBackRest's first full backup, which a
#     loaded machine overran while the backup was still visibly running.
#     wait_ready_while keeps waiting while a "still working" probe succeeds.
#   - check 29 could work on <project>_pg17_data under the operator's project,
#     check 27 removed the operator project's Patroni and etcd volumes, and
#     checks 21 and 22 wrote, pruned and planted files in the checkout's
#     backups/ (where retention once deleted about 175 real dumps). lib.sh now
#     refuses any volume or project without a test prefix and gives every check
#     a private MDB_BACKUP_DIR; the scan below fails a check that removes a
#     volume around those guards, names an operator volume, or spells the
#     checkout's backups/ path.

cd "$MDB_ROOT/scripts/verify" || vfail "no scripts/verify"

for f in [0-9][0-9]-*.sh; do
    [[ "$f" == "$(basename "${BASH_SOURCE[0]}")" ]] && continue   # its own patterns
    while IFS= read -r v; do
        grep -qF "track_volume \"$v\"" "$f" \
            || vfail "$f creates volume $v but never passes it to track_volume, so a failure (or an rm while in use) leaks it"
    done < <(sed -nE 's/.*volume create "(\$[A-Za-z_]+)".*/\1/p' "$f")
    if grep -nE "grep -c '\^mdb_'" "$f" >/dev/null; then
        vfail "$f counts every mdb_ volume on the machine; count the project's own (mdb_project_volumes)"
    fi
done
vinfo "every created volume is tracked; no check counts other projects' volumes"

declare -F wait_ready_while >/dev/null || vfail "lib.sh has no wait_ready_while"
t="$(mktemp -d "${TMPDIR:-/tmp}/mdb-wait.XXXXXX")"; add_cleanup "rm -rf '$t'"
# Ready only after 5s. With a 2s budget and a busy probe it must still pass...
ready_at=$(( $(date +%s) + 5 ))
( wait_ready_while 2 30 "the slow thing" true -- bash -c "(( \$(date +%s) >= $ready_at ))" ) >/dev/null 2>&1 \
    || vfail "wait_ready_while gave up although the busy probe said work was still in progress"
# ...and without one it must give up at the budget, not the ceiling.
start=$SECONDS
if ( wait_ready_while 2 30 "the idle thing" false -- false ) >/dev/null 2>&1; then
    vfail "wait_ready_while passed a condition that never held"
fi
(( SECONDS - start < 10 )) || vfail "wait_ready_while waited for the ceiling although nothing was busy"
vinfo "wait_ready_while extends past its budget only while work is in progress"

# --- the operator's volumes and backups/ are out of reach ------------------------

# hygiene_scan FILE: one line per violation, nothing when clean. Comment lines
# are skipped: the history of what went wrong is allowed to be written down.
hygiene_scan() {
    local f="$1" body
    body="$(grep -nvE '^[[:space:]]*#' "$f" || true)"
    # 1. Volumes are removed only through lib.sh's guarded paths.
    printf '%s\n' "$body" | grep -E 'volume rm' | sed "s|^|$f: removes a volume directly (use remove_test_volumes or track_volume): |" || true
    # 2. No operator volume name. Test volumes are mdbverify*/mdbscratch*/mdb-verify-*.
    printf '%s\n' "$body" | grep -E '(^|[^A-Za-z0-9_-])mdb_[A-Za-z0-9_]*_(data|pitr)([^A-Za-z0-9_]|$)' \
        | sed "s|^|$f: names an operator volume: |" || true
    # 3. No path to the checkout's backups/ (lib.sh's MDB_CHECKOUT_BACKUPS names it).
    printf '%s\n' "$body" | grep -E "(MDB_ROOT|[^A-Za-z_]ROOT)/backups|(^|[[:space:]\"'=(])(\./)?backups(/|[\"'])|(find|cd|ls|rm -[a-z]*) +(\./)?backups([[:space:]]|\$)" \
        | sed "s|^|$f: reaches the checkout's backups/: |" || true
}

# Exemptions, each a ceiling: the file must still match (a stale entry fails),
# and with exactly this many lines (measured 2026-11-03), so a new use in the
# same file fails too.
#   46: excludes backups/ from its read-only markdown link scan.
#   47: maps the Compose bind-mount source "backups" to the file that creates
#       it; it reads no data.
exempt_lines() {
    case "$1" in
        46-doc-links-resolve.sh) echo 1 ;;
        47-compose-paths-exist.sh) echo 1 ;;
        *) echo 0 ;;
    esac
}

scanned=0
for f in [0-9][0-9]-*.sh; do
    [[ "$f" == "$(basename "${BASH_SOURCE[0]}")" ]] && continue   # its own patterns
    scanned=$(( scanned + 1 ))
    found="$(hygiene_scan "$f")"
    n="$(printf '%s' "$found" | grep -c . || true)"
    allowed="$(exempt_lines "$f")"
    if (( allowed > 0 )); then
        (( n == allowed )) || vfail "$f is exempt for $allowed backups/ line(s) but has $n (update the exemption with a reason, or remove the new use):
$found"
        printf '%s\n' "$found" | grep -q "reaches the checkout's backups/" \
            || vfail "$f's exemption covers backups/ only: $found"
    elif [[ -n "$found" ]]; then
        vfail "a check can reach the operator's data:
$found"
    fi
done
# A glob that stops matching would report a clean suite having read nothing.
(( scanned >= 50 )) || vfail "scanned only $scanned checks; expected at least 50"
vinfo "$scanned checks scanned: none removes a volume around the guards, names an operator volume, or spells backups/"

# The scan can fail: a planted check with each violation is caught, and a clean
# one is not (both directions, so a scanner that can only say "clean" or only
# say "dirty" is itself a failure).
cat > "$t/98-planted.sh" <<'PLANT'
mdb_ctr volume rm -f "$vol"
track_volume mdb_pg17_data
find "$MDB_ROOT/backups" -name x
latest="$(find backups -name 'pg_*')"
PLANT
cat > "$t/97-clean.sh" <<'CLEAN'
# backups/ and mdb_pg17_data in a comment are fine
remove_test_volumes "$vol"
dumps="$SCRATCH_DIR/backups"
grep -q 'proj-backups-pg' "$log"
CLEAN
planted="$(hygiene_scan "$t/98-planted.sh")"
for want in 'removes a volume directly' 'names an operator volume' "reaches the checkout's backups/"; do
    printf '%s\n' "$planted" | grep -qF "$want" || vfail "the scan missed a planted violation ($want): [$planted]"
done
[[ "$(printf '%s\n' "$planted" | grep -c "reaches the checkout's backups/")" == 2 ]] \
    || vfail "the scan did not flag both planted backups/ paths: [$planted]"
[[ -z "$(hygiene_scan "$t/97-clean.sh")" ]] || vfail "the scan flagged a clean check: $(hygiene_scan "$t/97-clean.sh")"
vinfo "the scan flags each planted violation and passes a clean check"

# The runtime guards refuse what the scan cannot see (a name built at run time).
calls="$t/ctr-calls"
# shellcheck disable=SC2329  # mdb_ctr is the stub the guarded helpers would call
( mdb_ctr() { echo "$*" >> "$calls"; }; track_volume mdb_pg17_data ) >/dev/null 2>&1 \
    && vfail "track_volume accepted the operator volume mdb_pg17_data"
# shellcheck disable=SC2329
( mdb_ctr() { echo "$*" >> "$calls"; }; remove_test_volumes mdb_pg17_data ) >/dev/null 2>&1 \
    && vfail "remove_test_volumes accepted the operator volume mdb_pg17_data"
[[ ! -s "$calls" ]] || vfail "a refused volume still reached the container CLI: $(cat "$calls")"
( use_test_project mdb ) >/dev/null 2>&1 && vfail "use_test_project accepted the operator project mdb"
( track_volume mdb-verify-hygiene-$$; track_volume mdbscratch1_pg_data; use_test_project ) >/dev/null 2>&1 \
    || vfail "the guards refused a test name"
vinfo "track_volume, remove_test_volumes and use_test_project refuse operator names before any container call"

[[ -n "${MDB_BACKUP_DIR:-}" && -d "$MDB_BACKUP_DIR" && "$MDB_BACKUP_DIR" != "$MDB_ROOT"* ]] \
    || vfail "MDB_BACKUP_DIR for checks is not a private directory outside the checkout: ${MDB_BACKUP_DIR:-unset}"
[[ "$(bash -c 'printf %s "$MDB_BACKUP_DIR"')" == "$MDB_BACKUP_DIR" ]] || vfail "MDB_BACKUP_DIR is not exported to the scripts a check runs"
[[ "$MDB_UPGRADE_DIR" == "$MDB_BACKUP_DIR/"* ]] || vfail "MDB_UPGRADE_DIR is not inside the private backup directory: $MDB_UPGRADE_DIR"
got="$(cd "$MDB_ROOT" && bash -c '. scripts/env-lib.sh; mdb_backup_dir "$PWD" .env.example')"
[[ "$got" == "$MDB_BACKUP_DIR" ]] || vfail "scripts resolve the backup directory to $got, not the check's private one"
grep -qF -- '-u MDB_BACKUP_DIR -u MDB_UPGRADE_DIR' "$MDB_ROOT/scripts/verify/lib.sh" \
    || vfail "scratch_run does not unset MDB_BACKUP_DIR and MDB_UPGRADE_DIR (a scratch stack would write the check's directory)"
vinfo "every check gets a private MDB_BACKUP_DIR and MDB_UPGRADE_DIR; scripts resolve to it"
