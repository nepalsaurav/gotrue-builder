package gotruectl

import (
	"encoding/json"
	"fmt"
	"strings"
)

const managedByLabel = "managed-by=gotruectl"

func dockerAvailable() error {
	if _, err := runCapture("", "docker", "info"); err != nil {
		return fmt.Errorf("docker is not available (is it installed and running?): %w", err)
	}
	return nil
}

// containerState reports whether a container with the given name exists at
// all, and, separately, whether it's currently running. A container that
// exists but is stopped shows up in `docker ps -a` but not `docker ps`.
func containerState(name string) (exists bool, running bool, err error) {
	nameFilter := fmt.Sprintf("name=^/%s$", name)

	all, err := runCapture("", "docker", "ps", "-a", "-q", "--filter", nameFilter)
	if err != nil {
		return false, false, err
	}
	if all == "" {
		return false, false, nil
	}

	live, err := runCapture("", "docker", "ps", "-q", "--filter", nameFilter)
	if err != nil {
		return true, false, err
	}
	return true, live != "", nil
}

func ensureNetwork(name string) error {
	out, err := runCapture("", "docker", "network", "ls", "-q", "--filter", "name=^"+name+"$")
	if err != nil {
		return fmt.Errorf("checking network %s: %w", name, err)
	}
	if out != "" {
		return nil
	}
	if err := runInherit("", "docker", "network", "create", name); err != nil {
		return fmt.Errorf("creating network %s: %w", name, err)
	}
	return nil
}

func ensureVolume(name string) error {
	out, err := runCapture("", "docker", "volume", "ls", "-q", "--filter", "name=^"+name+"$")
	if err != nil {
		return fmt.Errorf("checking volume %s: %w", name, err)
	}
	if out != "" {
		return nil
	}
	if err := runInherit("", "docker", "volume", "create", name); err != nil {
		return fmt.Errorf("creating volume %s: %w", name, err)
	}
	return nil
}

// dockerExecCapture runs `docker exec <container> <args...>` and returns
// trimmed stdout — used for psql queries where we need the result.
func dockerExecCapture(container string, args ...string) (string, error) {
	full := append([]string{"exec", container}, args...)
	return runCapture("", "docker", full...)
}

// dockerExecInherit runs `docker exec <container> <args...>` with stdio
// connected to the current process — used for statements whose output the
// operator should just see (or that produce none).
func dockerExecInherit(container string, args ...string) error {
	full := append([]string{"exec", container}, args...)
	return runInherit("", "docker", full...)
}

type containerInfo struct {
	ID         string `json:"ID"`
	Names      string `json:"Names"`
	Image      string `json:"Image"`
	Status     string `json:"Status"`
	State      string `json:"State"`
	Ports      string `json:"Ports"`
	RunningFor string `json:"RunningFor"`
	Labels     string `json:"Labels"`
}

func (c containerInfo) label(key string) string {
	for _, kv := range strings.Split(c.Labels, ",") {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 && parts[0] == key {
			return parts[1]
		}
	}
	return ""
}

func dockerPull(image string) error {
	if err := runInherit("", "docker", "pull", image); err != nil {
		return fmt.Errorf("pulling %s: %w", image, err)
	}
	return nil
}

func dockerRename(oldName, newName string) error {
	if err := runInherit("", "docker", "rename", oldName, newName); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", oldName, newName, err)
	}
	return nil
}

// dockerHostPort returns the host port a container's given container-port/proto
// (e.g. "9999/tcp") is currently published on, read back from Docker itself
// rather than tracked separately — the container must be running.
func dockerHostPort(container, containerPort string) (string, error) {
	out, err := runCapture("", "docker", "port", container, containerPort)
	if err != nil {
		return "", fmt.Errorf("reading port mapping for %s: %w", container, err)
	}
	// e.g. "0.0.0.0:19999\n[::]:19999" — take the first line's port.
	first := strings.TrimSpace(strings.Split(out, "\n")[0])
	colon := strings.LastIndex(first, ":")
	if colon == -1 {
		return "", fmt.Errorf("unexpected `docker port` output for %s: %q", container, out)
	}
	return first[colon+1:], nil
}

// listContainersByLabel lists (including stopped) containers matching every
// given label filter, parsed from `docker ps`'s one-JSON-object-per-line
// format rather than hand-splitting tab-separated columns.
func listContainersByLabel(labels ...string) ([]containerInfo, error) {
	filters := make([]string, 0, len(labels))
	for _, l := range labels {
		filters = append(filters, "label="+l)
	}
	return listContainers(filters...)
}

// listAllContainers lists every container on the host (including stopped),
// with no filter — used by `status` to find GoTrue instances regardless of
// who created them.
func listAllContainers() ([]containerInfo, error) {
	return listContainers()
}

func listContainers(filters ...string) ([]containerInfo, error) {
	args := []string{"ps", "-a", "--format", "{{json .}}"}
	for _, f := range filters {
		args = append(args, "--filter", f)
	}
	out, err := runCapture("", "docker", args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}

	var containers []containerInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c containerInfo
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("parsing docker ps output: %w", err)
		}
		containers = append(containers, c)
	}
	return containers, nil
}
