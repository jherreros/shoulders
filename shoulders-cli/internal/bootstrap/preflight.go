package bootstrap

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/docker/docker/api/types/container"
)

// HostPortOwner returns the name of the running Docker container publishing
// the given host port, or "" when the port is free. Only running containers
// can hold a port binding, so stopped containers are ignored.
func HostPortOwner(ctx context.Context, port int) (string, error) {
	cli, err := dockerClient()
	if err != nil {
		return "", fmt.Errorf("create docker client: %w", err)
	}
	defer cli.Close() //nolint:errcheck // best-effort cleanup

	containers, err := cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list containers: %w", err)
	}
	for _, c := range containers {
		for _, p := range c.Ports {
			if int(p.PublicPort) == port {
				name := ""
				if len(c.Names) > 0 {
					name = strings.TrimPrefix(c.Names[0], "/")
				}
				return name, nil
			}
		}
	}
	return "", nil
}

// CheckHostPortsAvailable fails fast when a host port needed by the vind
// cluster (80/443) is already held by another container. The error names the
// squatting container and points at the remedy instead of surfacing a raw
// `docker … Bind for 0.0.0.0:80 failed` mid-install. Containers belonging to
// the target cluster itself are ignored so re-running `up` stays idempotent.
func CheckHostPortsAvailable(ctx context.Context, clusterName string, ports []int) error {
	for _, port := range ports {
		owner, err := HostPortOwner(ctx, port)
		if err != nil {
			return err
		}
		if owner != "" && !isOwnControlPlane(owner, clusterName) {
			return portConflictError(port, owner)
		}
	}
	return nil
}

// isOwnControlPlane reports whether a container is the control-plane of the
// cluster being created (vind names them "vcluster.cp.<cluster>").
func isOwnControlPlane(owner, clusterName string) bool {
	return owner == controlPlanePrefix+clusterName
}

// portConflictError builds the remedy for a held host port. vind
// control-plane containers are named "vcluster.cp.<cluster>", so the owning
// cluster can be suggested for removal directly; anything else falls back to
// `shoulders cluster list`.
func portConflictError(port int, owner string) error {
	if cluster, ok := strings.CutPrefix(owner, controlPlanePrefix); ok && cluster != "" {
		return fmt.Errorf("host port %d is already allocated by container %q (cluster %q); run 'shoulders down --name %s' to remove it, then retry", port, owner, cluster, cluster)
	}
	return fmt.Errorf("host port %d is already allocated by container %q; stop or remove it (see 'shoulders cluster list'), then retry", port, owner)
}

const (
	// ImageBuildDiskMinimum is the free host disk required for image build
	// and load operations (image layers plus builder cache).
	ImageBuildDiskMinimum = 5 << 30

	minDockerCPUs     = 8
	minDockerMemoryGB = 8
)

// ProfileDiskMinimum returns the free host disk required for `up` with the
// given profile. A full Docker disk causes the disk-pressure death spiral
// (evictions → dead admission webhooks → unhealable platform), so this is a
// hard failure, not a warning. Numbers match the documented profile minimums.
func ProfileDiskMinimum(profile string) uint64 {
	switch profile {
	case "large":
		return 60 << 30
	case "medium":
		return 40 << 30
	default:
		return 25 << 30
	}
}

// CheckDiskHeadroom fails when the filesystem holding the user's home (where
// Docker Desktop keeps its VM disk) has less than minBytes free.
func CheckDiskHeadroom(minBytes uint64) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	free, err := freeBytes(home)
	if err != nil {
		return nil
	}
	if free >= minBytes {
		return nil
	}
	return fmt.Errorf("only %.1f GiB free disk (need %.0f GiB): free space with 'docker builder prune' and 'docker image prune', enlarge the Docker Desktop disk image, then retry",
		float64(free)/(1<<30), float64(minBytes)/(1<<30))
}

// DockerResourceWarning describes under-provisioned Docker Desktop resources
// (CPUs, memory) without failing: returns "" when allocation is sufficient.
func DockerResourceWarning(ctx context.Context) (string, error) {
	cli, err := dockerClient()
	if err != nil {
		return "", fmt.Errorf("create docker client: %w", err)
	}
	defer cli.Close() //nolint:errcheck // best-effort cleanup

	info, err := cli.Info(ctx)
	if err != nil {
		return "", fmt.Errorf("query docker info: %w", err)
	}
	return dockerResourceWarning(info.NCPU, info.MemTotal), nil
}

func dockerResourceWarning(ncpu int, memBytes int64) string {
	var parts []string
	if ncpu > 0 && ncpu < minDockerCPUs {
		parts = append(parts, fmt.Sprintf("only %d Docker CPUs (recommend %d+)", ncpu, minDockerCPUs))
	}
	if memBytes > 0 && memBytes < int64(minDockerMemoryGB)<<30 {
		parts = append(parts, fmt.Sprintf("only %.1f GiB Docker memory (recommend %d+ GiB)", float64(memBytes)/(1<<30), minDockerMemoryGB))
	}
	if len(parts) == 0 {
		return ""
	}
	return "under-provisioned Docker Desktop (" + strings.Join(parts, ", ") + "): expect slow reconciliation; see the Profiles guide for minimums"
}
