package scrape

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// unitSuffixes are the unit types a process can run in. Slices group
// units and are only used when nothing more specific is found.
var unitSuffixes = []string{".service", ".scope", ".socket", ".mount", ".swap", ".device", ".automount", ".path", ".timer"}

// UnitFromCgroupPath returns the systemd unit that owns a cgroup path such
// as /system.slice/systemd-networkd.service, or "" if the path is not in
// a unit. For nested paths the innermost unit wins, so a user service
// (/user.slice/user-1000.slice/user@1000.service/app.slice/foo.service)
// resolves to foo.service, and a sub-cgroup a service created for itself
// (/system.slice/foo.service/worker) to foo.service.
func UnitFromCgroupPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		for _, suf := range unitSuffixes {
			if strings.HasSuffix(parts[i], suf) && len(parts[i]) > len(suf) {
				return parts[i]
			}
		}
	}
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.HasSuffix(parts[i], ".slice") && parts[i] != "-.slice" {
			return parts[i]
		}
	}
	return ""
}

// UnitFromProcCgroup returns the unit owning a process, given the
// contents of /proc/<pid>/cgroup. It prefers the unified (v2) hierarchy
// line "0::/path" and falls back to the v1 "name=systemd" hierarchy.
func UnitFromProcCgroup(content string) string {
	var v1 string
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		id, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		controllers, path, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		switch {
		case id == "0" && controllers == "":
			return UnitFromCgroupPath(path)
		case controllers == "name=systemd":
			v1 = path
		}
	}
	if v1 != "" {
		return UnitFromCgroupPath(v1)
	}
	return ""
}

// ErrActivated reports that a source's socket was created by the service
// manager (socket activation), so its peer credentials name PID 1 rather
// than the service that answers on it.
var ErrActivated = errors.New("scrape: socket created by the service manager (socket activation); unit unknown")

// ErrNoUnit reports that a source's process is not in a systemd unit,
// for example because it runs in a container's own cgroup tree.
var ErrNoUnit = errors.New("scrape: process is not in a systemd unit")

const managerSource = "io.systemd.Manager"

// resolveUnit reads the cgroup of pid under procRoot.
func resolveUnit(procRoot string, pid int32, source string) (string, error) {
	if pid <= 0 {
		return "", errors.New("scrape: no peer PID")
	}
	b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(int(pid)), "cgroup"))
	if err != nil {
		return "", fmt.Errorf("scrape: resolving unit: %w", err)
	}
	unit := UnitFromProcCgroup(string(b))
	if unit == "" {
		return "", fmt.Errorf("pid %d: %w", pid, ErrNoUnit)
	}
	if pid == 1 && source != managerSource {
		// PID 1 publishes io.systemd.Manager itself; any other source whose
		// socket PID 1 created was socket-activated, systemd's own included.
		return "", ErrActivated
	}
	return unit, nil
}
