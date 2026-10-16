#!/bin/sh
# Create the sdmetrics group and pick up the unit; the service is not
# started until configured: systemctl enable --now sdmetrics-exporter
set -e
if command -v systemd-sysusers >/dev/null 2>&1; then
    systemd-sysusers sdmetrics.conf || true
fi
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
    systemctl try-restart sdmetrics-exporter.service || true
fi
