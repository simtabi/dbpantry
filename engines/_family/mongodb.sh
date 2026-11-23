#!/usr/bin/env bash
# shellcheck shell=bash
#
# Family hooks: MongoDB, and FerretDB (SPEC section 22.2).
#
# One implementation for both, because FerretDB speaks the MongoDB wire
# protocol: the same mongosh, the same mongodump, the same everything below.
# That is the descriptor abstraction paying for itself — a second document
# engine cost a descriptor file and nothing here.

_mongo_uri() {
    # _mdb_pw, not pw: `local pw` would shadow a caller's own $pw, and a
    # caller whose secret() returns "$pw" would read this empty local instead.
    local _mdb_pw user host
    _mdb_pw="$(secret "$MDB_ENGINE_ROOT_SECRET")"
    user="${MDB_MONGODB_ROOT_USER:-root}"
    if [ "${IN_CONTAINER:-0}" = "1" ]; then host="$MDB_ENGINE_NAME"; else host="127.0.0.1"; fi
    # authSource=admin because the root user is created in the admin database,
    # not in whichever database is being addressed.
    printf 'mongodb://%s:%s@%s:%s/?authSource=admin' \
        "$user" "$_mdb_pw" "$host" "$MDB_ENGINE_PORT"
}

_mongosh() {
    engine_exec "$MDB_ENGINE_NAME" mongosh "$(_mongo_uri)" --quiet "$@"
}

hook_ping() {
    _mongosh --eval 'db.adminCommand({ping:1}).ok' 2>/dev/null | grep -q 1
}

# Mongo's own bookkeeping databases are excluded, the same way the SQL families
# exclude information_schema and friends.
hook_list_databases() {
    _mongosh --eval \
        'db.adminCommand({listDatabases:1}).databases
           .map(d => d.name)
           .filter(n => !["admin","local","config"].includes(n))
           .join("\n")' 2>/dev/null | tr -d '\r' | grep -v '^$'
}

# --archive gives a single file like every other engine's artefact, rather
# than a directory tree that would need its own handling downstream.
#
# Compression is left to the generic layer rather than using mongodump --gzip.
# Mixing the two produced a file NAMED .zst that CONTAINED gzip, so restore
# unzstd'd it into garbage and mongorestore failed with "gzip: invalid header"
# while still exiting 0. Whatever compresses the artefact must be the same
# thing that decompresses it.
hook_dump_database() {
    local db="$1" out="$2"
    if [ -n "$(compress_ext)" ]; then
        engine_exec "$MDB_ENGINE_NAME" mongodump --uri "$(_mongo_uri)" \
            --db "$db" --archive \
            | zstd -q -T0 -"${MDB_BACKUP_ZSTD_LEVEL:-9}" -o "$out" -f
    else
        engine_exec "$MDB_ENGINE_NAME" mongodump --uri "$(_mongo_uri)" \
            --db "$db" --archive > "$out"
    fi
}

# No equivalent of pg_dumpall --globals-only: users live in the admin database,
# which is dumped as an ordinary database when included.
hook_dump_globals() { return 0; }

hook_recreate_database() {
    local db="$1"
    _mongosh --eval "db.getSiblingDB('$db').dropDatabase()" >/dev/null 2>&1
}

# --drop so a restore replaces rather than merges; merging into existing
# collections would silently produce a database that is neither the backup nor
# what was there before.
hook_restore_database() {
    local db="$1"
    engine_exec "$MDB_ENGINE_NAME" mongorestore --uri "$(_mongo_uri)" \
        --archive --drop --nsInclude "${db}.*"
}

hook_object_count() {
    local db="$1"
    _mongosh --eval "db.getSiblingDB('$db').getCollectionNames().length" 2>/dev/null | tr -d ' \r'
}

hook_row_count() {
    local db="$1"
    _mongosh --eval \
        "db.getSiblingDB('$db').getCollectionNames()
           .reduce((n, c) => n + db.getSiblingDB('$db')[c].countDocuments(), 0)" \
        2>/dev/null | tr -d ' \r'
}

# No credentials at all must be refused. MongoDB runs wide open unless a root
# user is supplied, so this is the assertion that catches a regression there.
hook_auth_enforced() {
    if engine_exec "$MDB_ENGINE_NAME" mongosh --quiet \
        --eval 'db.adminCommand({listDatabases:1}).ok' >/dev/null 2>&1; then
        return 1
    fi
    return 0
}

# Provision a project: database, owner user, read-only companion.
#
# MongoDB creates a database lazily -- it does not exist until something is
# written -- so an explicit marker collection is created. Without it the
# database is absent from listDatabases, which makes `make backup-all` skip a
# project that was just provisioned.
#
# Users are created IN the project database rather than in admin, so the
# credential is scoped to it and authSource is the database itself.
hook_provision_project() {
    local db="$1" user="$2" pw="$3" ro_pw="${5:-$3}"

    _mongosh --eval "
        const db2 = db.getSiblingDB('${db}');
        db2.createCollection('_mdb_provisioned');
        const mk = (u, p, r) => {
            try { db2.createUser({user: u, pwd: p, roles: [{role: r, db: '${db}'}]}); }
            catch (e) {
                if (e.codeName !== 'Location51003' && !/already exists/.test(e.message)) throw e;
                db2.updateUser(u, {pwd: p, roles: [{role: r, db: '${db}'}]});
            }
        };
        mk('${user}', '${pw}', 'readWrite');
        mk('${user}_readonly', '${ro_pw}', 'read');
    " >/dev/null 2>&1 || return 1
}

# Drop a project: both users, then the database. A user that is already gone
# is not an error, so a half-dropped project can be dropped again.
hook_drop_project() {
    local db="$1" user="$2"
    _mongosh --eval "
        const db2 = db.getSiblingDB('${db}');
        for (const u of ['${user}', '${user}_readonly']) {
            try { db2.dropUser(u); }
            catch (e) { if (e.codeName !== 'UserNotFound' && !/not found/.test(e.message)) throw e; }
        }
        db2.dropDatabase();
    " >/dev/null 2>&1 || return 1
}
