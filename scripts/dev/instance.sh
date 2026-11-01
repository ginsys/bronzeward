#!/bin/sh
# The dev services' instance (ginsys/bronzeward#458). Source it, never run it:
#   . scripts/dev/instance.sh || exit 1
# mise run dev-db, dev-bao, dev-idp, dev-down, dev-idp-down, go-db, go-idp and browser-auth, and
# fixtures/dev-idp, take their container names, host ports and idp state directory from here.
#
# BW_DEV_INSTANCE unset or empty is the one default instance, with the names and ports the tasks
# always had. BW_DEV_INSTANCE=<name> is a named instance of 1 to 16 characters of [a-z0-9] (not
# `pg` or `groups`, which would make bw-dev-idp-<name> the default idp's own containers). Its
# containers carry the name as a suffix and its ports are the name's slot on top of a fixed base:
#   slot = cksum(name) mod 1000    (POSIX cksum: the same name gives the same ports everywhere)
#   PostgreSQL 12000+slot, OpenBao 13000+slot, identity provider 14000+slot, all on 127.0.0.1
# All are below 32768, outside the Linux ephemeral port range, and clear of the fixture's ports.
# Two names on the same slot make the second start fail on a taken port: pick another name.
# 15000 to 19999 is left to the browser tests' ports (ginsys/bronzeward#459).
#
# Exports the container names, ports and addresses below, recomputed from BW_DEV_INSTANCE on every
# source (a stale value in the environment is overwritten). BW_DEV_PFX is what a message names the
# instance with: "BW_DEV_INSTANCE=<name> " or nothing.

bw_dev_name=${BW_DEV_INSTANCE:-}
if [ -z "$bw_dev_name" ]; then
  bw_dev_sfx=
  BW_DEV_PFX=
  BW_DEV_PG_PORT=55433
  BW_DEV_BAO_PORT=58201
  BW_DEV_IDP_PORT=58280
  BW_DEV_IDP_DIR=.dev/idp
else
  case $bw_dev_name in
  pg | groups | *[!a-z0-9]*)
    echo "BW_DEV_INSTANCE=$bw_dev_name: use 1 to 16 characters of a-z and 0-9, not pg or groups" >&2
    unset bw_dev_name bw_dev_sfx
    return 1
    ;;
  esac
  if [ "${#bw_dev_name}" -gt 16 ]; then
    echo "BW_DEV_INSTANCE=$bw_dev_name: use 1 to 16 characters of a-z and 0-9, not pg or groups" >&2
    unset bw_dev_name bw_dev_sfx
    return 1
  fi
  bw_dev_sfx=-$bw_dev_name
  BW_DEV_PFX="BW_DEV_INSTANCE=$bw_dev_name "
  bw_dev_slot=$(($(printf %s "$bw_dev_name" | cksum | cut -d' ' -f1) % 1000))
  BW_DEV_PG_PORT=$((12000 + bw_dev_slot))
  BW_DEV_BAO_PORT=$((13000 + bw_dev_slot))
  BW_DEV_IDP_PORT=$((14000 + bw_dev_slot))
  BW_DEV_IDP_DIR=.dev/idp$bw_dev_sfx
  unset bw_dev_slot
fi

BW_DEV_PG_CONTAINER=bw-dev-pg$bw_dev_sfx
BW_DEV_BAO_CONTAINER=bw-dev-bao$bw_dev_sfx
BW_DEV_IDP_CONTAINER=bw-dev-idp$bw_dev_sfx
BW_DEV_IDP_PG_CONTAINER=bw-dev-idp-pg$bw_dev_sfx
BW_DEV_IDP_GROUPS_CONTAINER=bw-dev-idp-groups$bw_dev_sfx
BW_DEV_IDP_NET=bw-dev-idp$bw_dev_sfx
BW_DEV_PG_DSN="postgres://bronzeward@127.0.0.1:$BW_DEV_PG_PORT/bronzeward?sslmode=disable"
BW_DEV_BAO_ADDR="http://127.0.0.1:$BW_DEV_BAO_PORT"
BW_DEV_IDP_ISSUER="http://localhost:$BW_DEV_IDP_PORT"
export BW_DEV_PFX BW_DEV_PG_PORT BW_DEV_BAO_PORT BW_DEV_IDP_PORT BW_DEV_IDP_DIR
export BW_DEV_PG_CONTAINER BW_DEV_BAO_CONTAINER BW_DEV_IDP_CONTAINER BW_DEV_IDP_PG_CONTAINER
export BW_DEV_IDP_GROUPS_CONTAINER BW_DEV_IDP_NET BW_DEV_PG_DSN BW_DEV_BAO_ADDR BW_DEV_IDP_ISSUER
unset bw_dev_name bw_dev_sfx
