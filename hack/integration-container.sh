#!/usr/bin/env bash
# Runs the integration tests in a container that boots systemd as PID 1,
# as CI does. Needs docker and a cgroup v2 host (systemd v258+ refuses
# cgroup v1).
#
#   hack/integration-container.sh                 # fedora:rawhide
#   IMAGE=archlinux:latest hack/integration-container.sh
set -euo pipefail

IMAGE=${IMAGE:-fedora:rawhide}
NAME=sdmetrics-it-$$
cd "$(dirname "$0")/.."

mkdir -p bin
CGO_ENABLED=0 go build -o bin/queued ./examples/queued
CGO_ENABLED=0 go test -c -tags integration -o bin/integration.test ./integration/

docker build --build-arg IMAGE="$IMAGE" -t sdmetrics-systemd integration/images
docker run -d --name "$NAME" --privileged --cgroupns=host \
    -v /sys/fs/cgroup:/sys/fs/cgroup:rw -v "$PWD/bin:/sdm:ro" sdmetrics-systemd >/dev/null
trap 'docker rm -f "$NAME" >/dev/null' EXIT

for _ in $(seq 1 60); do
    state=$(docker exec "$NAME" systemctl is-system-running 2>/dev/null || true)
    case "$state" in running | degraded) break ;; esac
    sleep 1
done
docker exec "$NAME" systemctl --version | head -1
docker exec "$NAME" ls -l /run/systemd/report/
docker exec -e SDMETRICS_QUEUED=/sdm/queued "$NAME" /sdm/integration.test -test.v -test.count=1
