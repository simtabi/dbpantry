#!/usr/bin/env bash
# verify: every row of the parity ledger names a dbpantry command that exists, a proof that exists, and a make target whose recipe runs that command
# tags: fast structure
# phase: 2

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# Refactor plan §5 and §10 (phase 2). The make targets now run the dbpantry
# binary, which runs the scripts they used to run. docs/reference/parity.tsv
# says, for every make target, which command it runs, what proves that
# command, and when its logic moves into Go. A row is only worth something
# while it is true, so for every row:
#
#   1. the command exists in the binary (its help names exactly that path; an
#      unknown subcommand would show its parent's);
#   2. each proof exists: NN is scripts/verify/NN-*.sh, go:Name a Go test;
#   3. the make target exists and its recipe runs `$(DBPANTRY) <command>`;
#
# and every make target with a help line has a row, or is one of the few that
# build and test the binary itself. The other direction, every command of the
# binary having a row, is src/internal/cli's TestLedgerCoversTheTree.

need_dbpantry
cd "$MDB_ROOT" || exit 1
ledger="docs/reference/parity.tsv"
need_file "$ledger"

# Targets that build or test the binary, or list the make targets themselves:
# they cannot run it. Each must still exist (a stale entry fails).
#   help: prints the Makefile's own targets.
#   dbpantry, go-*: build, format-check, vet, test and lint the binary.
#   kit: copies the kit the binary embeds (design.md D-85), before it exists.
LEDGER_EXEMPT="help dbpantry kit go-ci go-fmt-check go-vet go-test go-lint"

# ledger_scan FILE MAKEFILE: one line per violation.
ledger_scan() {
    local file="$1" mk="$2" target command proofs port _note p words usage recipe
    while IFS=$'\t' read -r target command proofs port _note; do
        [[ -z "$target" || "$target" == \#* || "$target" == make_target ]] && continue
        [[ -n "$command" && -n "$proofs" && -n "$port" ]] || { printf '%s: incomplete row\n' "$target"; continue; }
        read -r -a words <<<"$command"
        usage="$("$DBPANTRY_BIN" --root "$MDB_ROOT" "${words[@]}" --help 2>/dev/null | awk '/^Usage:/{getline; print; exit}')"
        [[ "$usage" =~ ^[[:space:]]*dbpantry\ ${command}([[:space:]]|$) ]] \
            || printf '%s: no command "dbpantry %s" (help shows: %s)\n' "$target" "$command" "${usage:-nothing}"
        for p in ${proofs//,/ }; do
            case "$p" in
                go:*) grep -rqE "^func ${p#go:}\(t \*testing\.T\)" src --include='*_test.go' \
                        || printf '%s: no Go test %s\n' "$target" "${p#go:}" ;;
                [0-9][0-9]) compgen -G "scripts/verify/${p}-*.sh" >/dev/null \
                        || printf '%s: no check %s\n' "$target" "$p" ;;
                *) printf '%s: proof %s is neither NN nor go:Name\n' "$target" "$p" ;;
            esac
        done
        [[ "$target" == - ]] && continue
        recipe="$(awk -v t="$target" '
            $0 ~ "^"t":" { on = 1; next }
            on && /^\t/ { print; next }
            on { exit }' "$mk")"
        [[ -n "$recipe" ]] || { printf '%s: no such make target\n' "$target"; continue; }
        # shellcheck disable=SC2016  # the literal make syntax of the recipe
        grep -qF "\$(DBPANTRY) $command" <<<"$recipe" \
            || printf '%s: the recipe does not run $(DBPANTRY) %s\n' "$target" "$command"
    done < "$file"
}

found="$(ledger_scan "$ledger" Makefile)"
[[ -z "$found" ]] || { printf '      %s\n' "$found" >&2; vfail "the parity ledger is wrong (listed above)"; }
rows="$(grep -cvE '^(#|make_target	|$)' "$ledger")"
(( rows >= 40 )) || vfail "the ledger has $rows row(s); expected at least 40 (measured 2026-11-16: 54)"
vinfo "$rows ledger rows: each names a real command, an existing proof and, for a make target, a recipe that runs it"

# Every documented make target has a row, or is exempt.
targets="$(grep -oE '^[a-zA-Z_-]+:.*## ' Makefile | cut -d: -f1 | sort -u)"
ntargets="$(printf '%s\n' "$targets" | grep -c .)"
(( ntargets >= 45 )) || vfail "read $ntargets make target(s); expected at least 45"
missing=""
for t in $targets; do
    [[ " $LEDGER_EXEMPT " == *" $t "* ]] && continue
    cut -f1 "$ledger" | grep -qxF "$t" || missing+="$t "
done
[[ -z "$missing" ]] || vfail "make target(s) with no ledger row: $missing"
for t in $LEDGER_EXEMPT; do
    printf '%s\n' "$targets" | grep -qxF "$t" || vfail "exempt target $t no longer exists; drop it from LEDGER_EXEMPT"
done
vinfo "all $ntargets documented make targets have a row or build the binary itself"

# The scan can fail: each kind of wrong row is reported, and a right one is not.
t="$(mktemp -d "${TMPDIR:-/tmp}/mdb-ledger.XXXXXX")"; add_cleanup "rm -rf '$t'"
# shellcheck disable=SC2016  # literal make syntax
printf 'up:\n\t@$(DBPANTRY) up\nstatus:\n\t@scripts/runtime status\n' > "$t/Makefile"
printf 'up\tup\t15\t4\n-\tbackup bogus\t15\t4\nup\tup\t99,go:TestNoSuchThing\t4\nstatus\tstatus\t52\t4\nnope\tdown\t15\t4\n' > "$t/ledger.tsv"
planted="$(ledger_scan "$t/ledger.tsv" "$t/Makefile")"
# shellcheck disable=SC2016  # literal make syntax
for want in 'no command "dbpantry backup bogus"' 'no check 99' 'no Go test TestNoSuchThing' \
            'status: the recipe does not run $(DBPANTRY) status' 'nope: no such make target'; do
    grep -qF "$want" <<<"$planted" || vfail "the ledger scan missed a planted error ($want): [$planted]"
done
[[ "$(grep -c . <<<"$planted")" == 5 ]] || vfail "the ledger scan flagged the right row too: [$planted]"
vinfo "the scan reports a missing command, check, Go test, make target and recipe, and passes a right row"
