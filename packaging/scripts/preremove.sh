#!/bin/sh
set -e
# On upgrade, deb passes "upgrade" and rpm passes 1: keep the service running.
case "$1" in
    remove|0)
        if [ -d /run/systemd/system ]; then
            systemctl disable --now sdmetrics-exporter.service || true
        fi
        ;;
esac
