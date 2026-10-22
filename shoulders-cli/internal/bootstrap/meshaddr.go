package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// ClusterMeshAddress returns the address other clusters use to reach a cluster's
// ClusterMesh API server.
//
// vCluster runs the whole cluster (control plane and node) inside a single
// container, so the container address is both the node InternalIP and the only
// routable address. Peers dial this over the shared Docker network.
//
// Returned in "<address>:<port>" form, ready for the Cilium clusters map.
func ClusterMeshAddress(ctx context.Context, clusterName string, port int) (string, error) {
	container := controlPlanePrefix + clusterName
	docker, err := exec.LookPath("docker")
	if err != nil {
		return "", fmt.Errorf("find docker: %w", err)
	}
	out, err := exec.CommandContext(ctx, docker, "inspect", "--format", "{{json .NetworkSettings.Networks}}", container).Output()
	if err != nil {
		return "", fmt.Errorf("inspect container %q: %w", container, err)
	}
	var networks map[string]struct {
		IPAddress string `json:"IPAddress"`
	}
	if err := json.Unmarshal(out, &networks); err != nil {
		return "", fmt.Errorf("parse docker inspect output: %w", err)
	}
	for _, network := range networks {
		if network.IPAddress != "" {
			return fmt.Sprintf("%s:%d", network.IPAddress, port), nil
		}
	}
	return "", fmt.Errorf("container %q has no IP address on any network", container)
}
