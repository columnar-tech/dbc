#!/bin/bash
case "$1" in
  "search")
    echo '{"schema_version":1,"kind":"search.results","payload":{"drivers":[{"driver":"test","description":"Test driver"}]}}'
    ;;
  "slow")
    sleep 10
    echo '{"schema_version":1,"kind":"install.status","payload":{"status":"installed"}}'
    ;;
  "stream")
    for i in 1 2 3 4 5; do
      echo "{\"schema_version\":1,\"kind\":\"install.progress\",\"payload\":{\"event\":\"download.progress\",\"driver\":\"test\",\"bytes\":$((i*100)),\"total\":500}}"
      sleep 0.2
    done
    echo '{"schema_version":1,"kind":"install.status","payload":{"status":"installed","driver":"test","version":"1.0.0","location":"/tmp"}}'
    ;;
  "exit-nonzero")
    echo '{"schema_version":1,"kind":"error","payload":{"code":"not_found","message":"driver not found"}}' >&2
    exit 1
    ;;
  *)
    echo '{"schema_version":1,"kind":"error","payload":{"code":"unknown","message":"unknown command"}}'
    exit 1
    ;;
esac
