#!/usr/bin/env bash
# verify: metrics profile exposes exporters and EMBED toggles work both ways
# tags: metrics
# phase: 4

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need_runtime

# SPEC section 16: "EMBED toggles verified on and off". The off case matters as
# much as the on case: SPEC section 6.1 requires embedded helpers to be OFF
# under compose, because compose runs them as separate services. An embedded
# exporter that silently stays on would double-scrape and contradict the
# one-service-per-container rule.

img="$(image_name pg)"
need_image "$img"

# --- EMBED off (the compose default) -----------------------------------------
off="mdb-verify-embed-off-$$"
track_container "$off"
mdb_ctr run -d --name "$off" -e POSTGRES_PASSWORD=mdb-throwaway-verify "$img" >/dev/null \
    || vfail "container failed to start"
wait_ready 60 "postgres to accept connections" mdb_ctr exec "$off" pg_isready -U postgres

if mdb_ctr exec "$off" sh -c 'ps -eo comm | grep -q exporter' 2>/dev/null; then
    vfail "an exporter is running with MDB_PG_EMBED_EXPORTER unset; it must default off"
fi
vinfo "embedded exporter correctly off by default"

# --- EMBED on (the standalone docker run case) -------------------------------
on="mdb-verify-embed-on-$$"
track_container "$on"
mdb_ctr run -d --name "$on" \
    -e POSTGRES_PASSWORD=mdb-throwaway-verify \
    -e MDB_PG_EMBED_EXPORTER=true \
    "$img" >/dev/null || vfail "container failed to start with the exporter embedded"
wait_ready 60 "postgres to accept connections" mdb_ctr exec "$on" pg_isready -U postgres

# curl, not wget: the PostgreSQL image is Debian-based and ships curl, while
# wget is absent. Probing with a tool the image does not have reports a broken
# exporter when the exporter is fine.
# shellcheck disable=SC2016  # evaluated by the subshell, not here
wait_for 60 "the embedded exporter to serve metrics" \
    mdb_ctr exec "$on" sh -c 'curl -sf http://127.0.0.1:9187/metrics >/dev/null'

metrics="$(mdb_ctr exec "$on" curl -sf http://127.0.0.1:9187/metrics 2>/dev/null || true)"
printf '%s' "$metrics" | grep -q '^pg_up' \
    || vfail "embedded exporter did not serve pg_up"
vinfo "embedded exporter serves metrics on 9187"

# --- the metrics profile under compose ---------------------------------------
cd "$MDB_ROOT" || exit 1
need_file "$MDB_ROOT/docker-compose.yml"

mdb_up PROFILES=pg,metrics || vfail "make up PROFILES=pg,metrics failed"
add_cleanup 'make down'

# shellcheck disable=SC2016  # evaluated by the subshell, not here
wait_for 60 "the pg-exporter service" bash -c \
    'mdb_service_running pg-exporter'

# Exporters must stay on the internal network, unpublished (SPEC section 7).
# Read through `port`, not `compose ps --format '{{.Ports}}'`: the ps template
# fields differ between Compose implementations, `port` does not (D-60).
published=""
for svc in pg-exporter mysql-exporter; do
    for id in $(mdb_compose ps -q "$svc" 2>/dev/null); do
        p="$(mdb_ctr port "$id" 2>/dev/null || true)"
        [[ -n "$p" ]] && published+="$svc: $p "
    done
done
[[ -z "$published" ]] || vfail "an exporter is published to the host: $published"
vinfo "exporters reachable internally, published to nothing"
