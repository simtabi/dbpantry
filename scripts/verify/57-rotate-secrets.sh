#!/usr/bin/env bash
# verify: rotate-secrets changes every MySQL-family password it names, at every host, and the new ones work (scratch stack)
# tags: security
# phase: 4

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# rotate-secrets changes passwords IN the database and in secrets/, so this
# never runs against the operator's: scratch_stack gives it its own checkout
# copy, project, secrets, volumes, subnet and ports. The operator's secrets/ is
# hashed before and after, and any change fails the check.
#
# The bug it pins: root logged in with the USER's new password, so every
# MySQL-family project user failed to rotate, kept its old password, and the
# run still exited 0. Only 'root'@'localhost' was changed, leaving 'root'@'%'
# on the old password over TCP, and read-only companions were never touched.

secrets_sum() {
    if [[ -d "$MDB_ROOT/secrets" ]]; then
        find "$MDB_ROOT/secrets" -type f -exec shasum -a 256 {} + 2>/dev/null | sort
    fi
}
real_before="$(secrets_sum)"

scratch_stack "mysql,mariadb"
[[ "$SCRATCH_DIR/secrets" != "$MDB_ROOT/secrets" ]] || vfail "refusing: scratch secrets are the operator's"
scratch_up || vfail "make up failed in the scratch stack"
wait_for 180 "scratch mysql and mariadb healthy" scratch_healthy mysql mariadb

# Log in AS a user with a password, over TCP inside the container, so the
# 'user'@'%' row is the one tested (a socket login would match 'localhost').
login() {
    local engine="$1" client="$2" user="$3" pw="$4"
    scratch_exec "$engine" env MYSQL_PWD="$pw" "$client" -u"$user" -h127.0.0.1 --protocol=tcp \
        -N -B -e 'SELECT 1' 2>/dev/null | tr -d '\r' | grep -qx 1
}
root_sock() {
    local engine="$1" client="$2"
    scratch_exec "$engine" env MYSQL_PWD="$(cat "$SCRATCH_DIR/secrets/${engine}_root_password.txt")" \
        "$client" -uroot --protocol=socket -N -B -e 'SELECT 1' 2>/dev/null | tr -d '\r' | grep -qx 1
}
wait_ready 120 "scratch mysql ready" root_sock mysql mysql
wait_ready 120 "scratch mariadb ready" root_sock mariadb mariadb

# Old passwords, by engine and principal (bash 3.2 has no associative arrays).
old_get() { local v="old_$1_$2"; printf '%s' "${!v}"; }
root_tcp_hosts() {
    scratch_exec "$1" env MYSQL_PWD="$(cat "$SCRATCH_DIR/secrets/${1}_root_password.txt")" "$1" -uroot \
        --protocol=socket -N -B -e "SELECT count(*) FROM mysql.user WHERE User='root' AND Host IN ('%','127.0.0.1')" \
        2>/dev/null | tr -d '\r'
}
for engine in mysql mariadb; do
    client="$engine"
    scratch_run scripts/new-project --name rotchk --engine "$engine" >/dev/null 2>&1 \
        || vfail "new-project failed for $engine in the scratch stack"
    for f in root rotchk_user rotchk_user_readonly; do
        printf -v "old_${engine}_${f}" '%s' "$(cat "$SCRATCH_DIR/secrets/${engine}_${f}_password.txt")"
    done
    login "$engine" "$client" rotchk_user "$(old_get "$engine" rotchk_user)" \
        || vfail "$engine: the project user cannot log in before rotation"
done

out="$(scratch_run scripts/rotate-secrets --yes 2>&1)" || vfail "rotate-secrets failed: $(tail -5 <<<"$out")"

for engine in mysql mariadb; do
    client="$engine"
    tcp_root="$(root_tcp_hosts "$engine")"
    for f in root rotchk_user rotchk_user_readonly; do
        new="$(cat "$SCRATCH_DIR/secrets/${engine}_${f}_password.txt")"
        [[ "$new" != "$(old_get "$engine" "$f")" ]] || vfail "$engine: $f was not rotated (its file is unchanged)"
        if [[ "$f" == root && "${tcp_root:-0}" == 0 ]]; then
            vinfo "$engine: root has no TCP host row; socket login only"
            continue
        fi
        login "$engine" "$client" "$f" "$new" \
            || vfail "$engine: $f cannot log in over TCP with the NEW password"
        if login "$engine" "$client" "$f" "$(old_get "$engine" "$f")"; then
            vfail "$engine: $f still logs in over TCP with the OLD password"
        fi
    done
    root_sock "$engine" "$client" || vfail "$engine: root cannot log in on the socket with the new password"
    vinfo "$engine: root, owner and read-only rotated; old passwords refused"
done

[[ "$(secrets_sum)" == "$real_before" ]] || vfail "the operator's secrets/ changed during the check"
vinfo "operator secrets/ unchanged"
