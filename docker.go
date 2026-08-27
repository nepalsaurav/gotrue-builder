package main

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

// listContainersByLabel lists (including stopped) containers matching every
// given label filter, parsed from `docker ps`'s one-JSON-object-per-line
// format rather than hand-splitting tab-separated columns.
func listContainersByLabel(labels ...string) ([]containerInfo, error) {
	args := []string{"ps", "-a", "--format", "{{json .}}"}
	for _, l := range labels {
		args = append(args, "--filter", "label="+l)
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
