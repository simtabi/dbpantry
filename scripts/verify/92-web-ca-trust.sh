#!/usr/bin/env bash
# verify: certs --trust installs only the web CA's root in each platform's store, finds it again by fingerprint, and never hands over a key
# tags: fast structure security
# phase: 1

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

cd "$MDB_ROOT" || exit 1

# design.md D-112. Browsers refused every UI because Caddy's CA lived in its
# volume, which no host trust store knew. scripts/certs now generates a web CA
# in the home and `dbpantry certs trust` (scripts/certs --trust) installs its
# root. This runs that against stub system tools -- no real keychain, store or
# sudo is touched -- and asserts, per platform, the exact commands, that a
# second run is a no-op where the store can say so, that removal finds the
# certificate by fingerprint, and that no key file is ever passed to a tool.

tmp="$(mktemp -d "${TMPDIR:-/tmp}/mdb-trust.XXXXXX")"
add_cleanup "rm -rf '$tmp'"
home="$tmp/home"; stubs="$tmp/bin"; fakehome="$tmp/user"; log="$tmp/calls.log"
mkdir -p "$home" "$stubs" "$fakehome"
cp .env.example "$home/.env"
env -i PATH="/usr/bin:/bin" HOME="$fakehome" MDB_HOME="$home" bash scripts/certs >/dev/null 2>&1 \
    || vfail "scripts/certs failed in a scratch home"
root="$home/certs/web/root.crt"
[[ -s "$root" && -s "$home/certs/web/intermediate.crt" ]] || vfail "scripts/certs wrote no web CA"
fp="$(openssl x509 -in "$root" -noout -fingerprint -sha1 | sed 's/.*=//; s/://g')"

for t in security sudo update-ca-certificates update-ca-trust certutil certutil.exe; do
    # shellcheck disable=SC2016  # $* and $STUB_LOG expand in the stub, not here
    printf '#!/usr/bin/env bash\nprintf "%%s %%s\\n" "%s" "$*" >> "$STUB_LOG"\n' "$t" > "$stubs/$t"
done
# security find-certificate reports the root as present only when asked to.
cat > "$stubs/security" <<'STUB'
#!/usr/bin/env bash
printf 'security %s\n' "$*" >> "$STUB_LOG"
[[ "$1" == find-certificate && -n "${STUB_HAVE:-}" ]] && printf 'SHA-1 hash: %s\n' "$STUB_HAVE"
exit 0
STUB
cat > "$stubs/uname" <<'STUB'
#!/usr/bin/env bash
case "$1" in -s) printf '%s\n' "$STUB_S" ;; -r) printf '%s\n' "${STUB_R:-6.8.0}" ;; esac
STUB
cat > "$stubs/wslpath" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' 'C:\dbp\root.crt'
STUB
chmod +x "$stubs"/*

# trust OS MODE [VAR=VALUE...] -- run scripts/certs --MODE on a stub platform;
# prints its output; the calls it made are in $log. Counted in a file, since
# most calls run in a $(...) subshell.
trust() {
    local os="$1" mode="$2"; shift 2
    : > "$log"
    printf '%s %s\n' "$os" "$mode" >> "$tmp/scenarios"
    env -i PATH="$stubs:/usr/bin:/bin" HOME="$fakehome" MDB_HOME="$home" STUB_LOG="$log" STUB_S="$os" "$@" \
        bash scripts/certs "--$mode" 2>&1
}
calls() { cat "$log"; }

# --- macOS ----------------------------------------------------------------------
out="$(trust Darwin trust)"
kc="$fakehome/Library/Keychains/login.keychain-db"
calls | grep -qxF "security add-trusted-cert -r trustRoot -p ssl -k $kc $root" \
    || vfail "macOS: the root was not added to the login keychain as a trusted TLS root: $(calls | tr '\n' ';') / $out"
out="$(trust Darwin trust STUB_HAVE="$fp")"
calls | grep -q 'add-trusted-cert' && vfail "macOS: a second trust added the root again"
[[ "$out" == *"already in the login keychain"* ]] || vfail "macOS: a second trust did not say it was already trusted: $out"
trust Darwin untrust >/dev/null
calls | grep -qxF "security delete-certificate -Z $fp $kc" || vfail "macOS: untrust did not delete by fingerprint: $(calls)"
vinfo "macOS: added to the login keychain as a trusted TLS root once, removed by fingerprint"

# --- Linux, Debian family, with a browser NSS database ------------------------------
sys="$tmp/sys-deb"; mkdir -p "$sys/usr/local/share/ca-certificates" "$fakehome/.pki/nssdb"
trust Linux trust MDB_TRUST_SYSROOT="$sys" >/dev/null
dst="$sys/usr/local/share/ca-certificates/dbpantry-mdb-web-ca.crt"
calls | grep -qxF "sudo cp $root $dst" || vfail "Debian: the root was not copied into the store: $(calls | tr '\n' ';')"
calls | grep -qxF "sudo update-ca-certificates" || vfail "Debian: update-ca-certificates did not run"
calls | grep -qxF "certutil -d sql:$fakehome/.pki/nssdb -A -t C,, -n dbpantry-mdb-web-ca -i $root" \
    || vfail "Debian: the browsers' NSS database was not updated: $(calls | tr '\n' ';')"
trust Linux untrust MDB_TRUST_SYSROOT="$sys" >/dev/null
calls | grep -qxF "sudo rm -f $dst" || vfail "Debian: untrust did not remove the store file"
rm -rf "$fakehome/.pki"
vinfo "Linux (Debian): system store and NSS database, through sudo"

# --- Linux, Fedora family -------------------------------------------------------------
sys="$tmp/sys-rh"; mkdir -p "$sys/etc/pki/ca-trust/source/anchors"
trust Linux trust MDB_TRUST_SYSROOT="$sys" >/dev/null
calls | grep -qxF "sudo cp $root $sys/etc/pki/ca-trust/source/anchors/dbpantry-mdb-web-ca.crt" \
    || vfail "Fedora: the root was not copied into the anchors: $(calls | tr '\n' ';')"
calls | grep -qxF "sudo update-ca-trust" || vfail "Fedora: update-ca-trust did not run"
calls | grep -q certutil && vfail "Fedora: certutil ran with no NSS database"
vinfo "Linux (Fedora): anchors and update-ca-trust; no NSS database, no certutil"

# --- Linux with no known store: a clear failure, not a silent success -------------------
if out="$(trust Linux trust MDB_TRUST_SYSROOT="$tmp/sys-none")"; then
    vfail "a Linux system with no trust store reported success: $out"
fi
[[ "$out" == *"no system trust store found"* ]] || vfail "no store: the failure does not say why: $out"

# --- WSL: the Windows user store ------------------------------------------------------
trust Linux trust STUB_R="6.6.36-microsoft-standard-WSL2" >/dev/null
calls | grep -qxF 'certutil.exe -user -addstore Root C:\dbp\root.crt' \
    || vfail "WSL: the root was not added to the Windows user Root store: $(calls | tr '\n' ';')"
calls | grep -q '^sudo' && vfail "WSL: sudo ran, but the browsers are Windows'"
trust Linux untrust STUB_R="6.6.36-microsoft-standard-WSL2" >/dev/null
calls | grep -qxF "certutil.exe -user -delstore Root $fp" || vfail "WSL: untrust did not delete by fingerprint"
vinfo "WSL: the Windows user Root store, removed by fingerprint"

# --- Anything else: refused, with the file to import by hand ------------------------------
out="$(trust FreeBSD trust)" && vfail "an unsupported system reported success: $out"
[[ "$out" == *"$root"* ]] || vfail "an unsupported system was not told which file to import: $out"

# No platform is ever handed a key.
for k in root.key intermediate.key; do
    [[ -s "$home/certs/web/$k" ]] || vfail "the web CA has no $k to guard"
done
trust Darwin trust >/dev/null
calls | grep -qE '\.key( |$)' && vfail "a key file was passed to a trust tool: $(calls)"
scenarios="$(grep -c . "$tmp/scenarios")"
(( scenarios >= 11 )) || vfail "only $scenarios trust scenarios ran"
vinfo "$scenarios scenarios: no key is ever handed to a trust store"
