#!/usr/bin/env bash
# verify: the dbpantry binary alone, in an empty directory, extracts its kit with checksums, runs init into ~/.config/dbpantry, plans up, and restores a tampered kit
# tags: fast structure
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_dbpantry

# design.md D-85, management-plan.md phase B: "a go binary that can be used to
# launch dbpantry images, and the dashboard", with no checkout. The binary
# embeds the kit (`make kit`); with no checkout to find, it extracts it to
# ~/.config/dbpantry/kit/<version>, verified against its manifest, and keeps
# the instance in ~/.config/dbpantry (D-84).
#
# This copies the binary into an empty directory and runs it there with a
# fresh HOME and nothing else: no DBPANTRY_DIR, no checkout above it, no
# MDB_HOME. Nothing needs the container runtime: init generates secrets and
# certificates, and up is a dry run.
work="$(mktemp -d "${TMPDIR:-/tmp}/mdb-verify-bare.XXXXXX")" || vfail "mktemp failed"
add_cleanup "rm -rf '$work'"
mkdir -p "$work/bin" "$work/home"
cp "$DBPANTRY_BIN" "$work/bin/dbpantry"
# The caller's PATH, as a user has it: check-env asks Compose which ports the
# stack would publish, and without a container CLI it falls back to every
# *_PORT in the env file, HAProxy's included, which a busy host holds.
# The container CLI's own configuration too: HOME is a fresh directory, and
# Docker finds its context (OrbStack's socket, say) under ~/.docker, so it is
# named explicitly, as are a DOCKER_HOST and CONTAINER_RUNTIME the caller set.
docker_cfg="${DOCKER_CONFIG:-$HOME/.docker}"
bare() { ( cd "$work/bin" && env -i PATH="$PATH" HOME="$work/home" TMPDIR="${TMPDIR:-/tmp}" ${PROFILES:+PROFILES="$PROFILES"} \
    DOCKER_CONFIG="$docker_cfg" ${DOCKER_HOST:+DOCKER_HOST="$DOCKER_HOST"} \
    ${CONTAINER_RUNTIME:+CONTAINER_RUNTIME="$CONTAINER_RUNTIME"} ./dbpantry "$@" ); }

# 1. init from the bare binary. With the pg profile only: the default adds ui,
# whose Adminer publishes 8080, and init's check-env rightly refuses a port
# this host may hold. What is tested here is the binary, not the host.
# check-env's env-file fallback must also ignore HAProxy's ports when ha is
# off (design.md D-85): otherwise a busy 5432/7000 on the host fails this
# init whenever Compose cannot render.
out="$(PROFILES=pg bare init 2>&1)" || vfail "dbpantry init from the bare binary failed:
$(printf '%s\n' "$out" | tail -15 | sed 's/^/       /')"
home="$work/home/.config/dbpantry"
kitdir="$(find "$home/kit" -mindepth 1 -maxdepth 1 -type d ! -name '.*' 2>/dev/null | head -1)"
[[ -n "$kitdir" && -s "$kitdir/MANIFEST" ]] || vfail "no kit was extracted under $home/kit"
for f in .env secrets/pg_superuser_password.txt certs/ca.crt compose.engines.yml; do
    [[ -s "$home/$f" ]] || vfail "init did not create $f in ~/.config/dbpantry"
done
[[ "$(stat -f '%Lp' "$home" 2>/dev/null || stat -c '%a' "$home")" == 700 ]] \
    || vfail "the home $home is not 0700, and it holds secrets/"
[[ -x "$kitdir/scripts/init" ]] || vfail "the extracted scripts lost their executable bit"
n="$(grep -c . "$kitdir/MANIFEST")"
(( n > 100 )) || vfail "the kit's manifest lists only $n files"
vinfo "init from the bare binary: kit of $n files at ~/.config/dbpantry/kit/${kitdir##*/}, instance in ~/.config/dbpantry"

# 2. Nothing outside the home was written: not beside the binary, not
# elsewhere in HOME.
left="$(ls -A "$work/bin")"
[[ "$left" == dbpantry ]] || vfail "the binary's directory gained files: $(printf '%s' "$left" | tr '\n' ' ')"
# ~/Library is macOS's own: it creates Library/Containers there for the
# container CLI that check-env runs, whatever HOME holds. dbpantry writes none.
others="$(cd "$work/home" && find . -mindepth 1 -maxdepth 2 ! -path ./.config ! -path ./.config/dbpantry \
    ! -path ./Library ! -path './Library/*')"
[[ -z "$others" ]] || vfail "files were written in HOME outside ~/.config/dbpantry: $others"
vinfo "nothing was written beside the binary or elsewhere in HOME"

# 3. up plans from the extracted kit.
out="$(bare --dry-run up 2>&1)" || vfail "dbpantry --dry-run up failed: $(printf '%s\n' "$out" | tail -8)"
[[ "$out" == *"scripts/runtime up"* ]] || vfail "the dry run does not plan scripts/runtime up: $out"
vinfo "up --dry-run plans the boot from the extracted kit"

# 4. A tampered kit is put back. The checksum is what decides, not the file's
# presence: an edited script would otherwise run in place of the shipped one.
want="$(grep ' scripts/init$' "$kitdir/MANIFEST" | cut -d' ' -f2)"
printf '\necho tampered\n' >> "$kitdir/scripts/init"
out="$(bare --dry-run up 2>&1)" || vfail "dbpantry failed after the kit was tampered with: $(printf '%s\n' "$out" | tail -5)"
got="$(shasum -a 256 "$kitdir/scripts/init" | cut -d' ' -f1)"
[[ "$got" == "$want" ]] || vfail "a tampered scripts/init was not restored from the binary (sha256 $got, manifest $want)"
[[ "$out" == *"extracted the built-in kit"* ]] || vfail "the restore was silent; the run should say it extracted the kit"
vinfo "a tampered kit is re-extracted from the binary"
