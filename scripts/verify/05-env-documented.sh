#!/usr/bin/env bash
# verify: every MDB_ variable in use is documented in .env.example
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

need_file "$MDB_ROOT/.env.example"

# SPEC section 13: "One .env.example, every variable documented inline".
# An undocumented variable is a variable nobody can discover.

documented=()
while IFS= read -r v; do
    [ -n "$v" ] && documented+=("$v")
done < <(
    grep -oE '^#?[[:space:]]*MDB_[A-Z0-9_]+=' .env.example \
        | sed -E 's/^#?[[:space:]]*//; s/=$//' | sort -u
)

# Internal variables, deliberately NOT documented in .env.example.
#
# These are not user configuration: they are set by the toolkit itself or baked
# into an image at build time. MDB_ENGINE in particular is an ENV in each
# MySQL-family Dockerfile that selects which half of the shared init scripts
# applies (D-29); documenting it in .env.example would invite a user to set it
# and quietly mismatch the image they are running.
#
# The rule this check enforces is "every variable a user can meaningfully set
# is documented", so the list is kept explicit and short rather than allowing a
# pattern that could hide a real omission.
INTERNAL_VARS='MDB_ROOT|MDB_ENV_FILE|MDB_IMAGE_PREFIX|MDB_ENGINE|MDB_STAGE|MDB_CONF_DIR|MDB_CERT_DIR|MDB_INITDB_DIR|MDB_DATA_DIR|MDB_OVERRIDE_CONF_DIR|MDB_DAEMON|MDB_CLIENT|MDB_ADMIN|MDB_CONF_SECTION|MDB_ROOT_PW_ENV|MDB_PG_TEST_MODE|MDB_MYSQL_TEST_MODE|MDB_MARIADB_TEST_MODE|MDB_IN_CONTAINER|MDB_ENGINES_DIR'

# Only files that actually consume variables at runtime. Scripts under
# scripts/verify are excluded: they name variables in order to assert on them,
# which is not the same as depending on them being configured.
used=()
while IFS= read -r v; do
    [ -n "$v" ] && used+=("$v")
done < <(
    grep -rhoE '\bMDB_[A-Z0-9_]+' \
        --include='*.yml' --include='*.yaml' --include='Dockerfile*' \
        --include='*.tmpl' --include='*.cnf' \
        images caddy overrides . 2>/dev/null \
        | grep -vE "^(${INTERNAL_VARS})\$" | sort -u
)

# The compose files and images consume dozens; none means the scan broke,
# not that nothing is configured (it used to exit 0 here).
(( ${#used[@]} >= 50 )) || vfail "found only ${#used[@]} MDB_ variable(s) in compose files and images; the scan is broken"

# A key a script or the binary reads from the env file is user-settable by
# definition: get/mdb_env_get/env_get MDB_X in scripts/, and
# Effective/File/env("MDB_X") in the Go binary (2026-11-24 audit, G4: the
# MDB_DASHBOARD_* settings were read and documented nowhere).
read_keys=()
while IFS= read -r v; do
    [ -n "$v" ] && read_keys+=("$v")
done < <(
    { grep -rhoE '\b(get|mdb_env_get|env_get) +"?MDB_[A-Z0-9_]+' scripts --exclude-dir=verify 2>/dev/null
      grep -rhoE '(Effective|File|env)\("MDB_[A-Z0-9_]+"' src --include='*.go' --exclude='*_test.go' 2>/dev/null
    } | grep -oE 'MDB_[A-Z0-9_]+' | sort -u
)
(( ${#read_keys[@]} >= 30 )) || vfail "found only ${#read_keys[@]} key(s) read from the env file; the scan is broken"
for v in "${read_keys[@]}"; do
    printf '%s\n' "$v" | grep -qxE "$INTERNAL_VARS" && continue
    printf '%s\n' "${used[@]}" | grep -qx "$v" || used+=("$v")
done

missing=0
for v in "${used[@]}"; do
    [[ "$v" == "MDB_ROOT" ]] && continue
    if ! printf '%s\n' "${documented[@]}" | grep -qx "$v"; then
        printf '      %s is used but not documented in .env.example\n' "$v" >&2
        (( missing++ )) || true
    fi
done

(( missing == 0 )) || vfail "$missing undocumented MDB_ variable(s)"

vinfo "${#used[@]} variable(s) in use, all documented"
