package cmd

// THROWAWAY end-to-end validation of the topology orchestration.
//
// Exercises the real product functions — ensureClusterExists, prepareMesh,
// seedSharedCA and the Cilium ClusterMesh options — against live vind clusters,
// then asserts the mesh actually forms and cross-cluster traffic flows.
//
// Not shipped as a normal test: it needs Docker and several minutes. Run with:
//
//	SHOULDERS_MESH_E2E=1 go test ./cmd/ -run TestTopologyOrchestrationE2E -v -timeout 60m

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
)

const e2eCiliumVersion = "1.20.2"

const (
	e2eHub   = "e2ehub"
	e2eSpoke = "e2espoke"
)

func e2eClusters(t *testing.T) []*config.ResolvedCluster {
	t.Helper()
	return []*config.ResolvedCluster{
		clusterNamed(e2eHub, config.RoleHub, true, config.PortMapping{HTTP: 80, HTTPS: 443}),
		clusterNamed(e2eSpoke, config.RoleSpoke, true, config.PortMapping{HTTP: 8080, HTTPS: 8443}),
		// Pinned to the version the ClusterMesh values in
		// docs/multi-cluster-evidence.md were measured against.
	}
}

func TestTopologyOrchestrationE2E(t *testing.T) {
	if os.Getenv("SHOULDERS_MESH_E2E") != "1" {
		t.Skip("set SHOULDERS_MESH_E2E=1 to run the topology orchestration e2e test")
	}
	ctx := context.Background()
	clusters := e2eClusters(t)

	// The hub must be first: it becomes the Cilium trust anchor.
	if clusters[0].Role != config.RoleHub {
		t.Fatalf("hub must lead the install order")
	}
	clusters[0].PlatformConfig.Cilium.Version = e2eCiliumVersion
	clusters[1].PlatformConfig.Cilium.Version = e2eCiliumVersion
	if err := validateMeshedTopology(clusters); err != nil {
		t.Fatalf("topology rejected: %v", err)
	}

	plan, err := newTopologyPlan(clusters)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	t.Logf("stage 1+2+3: create clusters, resolve addresses, seed peer DNS")
	if err := prepareMesh(ctx, clusters, plan); err != nil {
		t.Fatalf("prepareMesh: %v", err)
	}
	for name, address := range plan.addresses {
		t.Logf("  %s -> %s", name, address)
	}
	if plan.addresses[e2eHub] == "" || plan.addresses[e2eSpoke] == "" {
		t.Fatal("addresses not resolved for both clusters")
	}

	// Install the hub, then harvest its Cilium CA.
	t.Logf("installing hub %s", e2eHub)
	selectCluster(clusters[0])
	hubOptions := plan.optionsFor(clusters[0])
	if hubOptions == nil || len(hubOptions.Peers) != 1 {
		t.Fatalf("hub mesh options wrong: %+v", hubOptions)
	}
	if err := bootstrap.EnsureCilium(kubeconfig, clusters[0].CiliumVersion(),
		withMesh(bootstrap.CiliumOptionsForProfile(clusters[0].Profile), hubOptions)); err != nil {
		t.Fatalf("hub cilium: %v", err)
	}

	ca, found, err := bootstrap.CiliumCA(ctx, kubeconfig)
	if err != nil || !found {
		t.Fatalf("no cilium CA in the hub: found=%v err=%v", found, err)
	}
	plan.ca = ca
	t.Log("harvested the hub Cilium trust anchor")

	t.Logf("installing spoke %s with the shared trust anchor", e2eSpoke)
	selectCluster(clusters[1])
	if err := seedSharedCA(ctx, clusters[1], plan); err != nil {
		t.Fatalf("seedSharedCA: %v", err)
	}
	spokeOptions := plan.optionsFor(clusters[1])
	if err := bootstrap.EnsureCilium(kubeconfig, clusters[1].CiliumVersion(),
		withMesh(bootstrap.CiliumOptionsForProfile(clusters[1].Profile), spokeOptions)); err != nil {
		t.Fatalf("spoke cilium: %v", err)
	}

	waitForMesh(t, clusters[0])

	t.Log("cross-cluster pod to pod")
	if err := probeCrossCluster(t, ctx, clusters); err != nil {
		t.Fatalf("cross-cluster traffic: %v", err)
	}
}

// TestTopologyTiersAreInstalled asserts that the hub has the platform tier and
// the spoke has only the workload tier.
//
// The tier is what makes a topology more than a list of clusters, and a
// regression in it is silent: the spoke would install the hub's addons, or
// miss its own, and the cluster would still look healthy.
//
//	SHOULDERS_MESH_E2E=1 go test ./cmd/ -run TestTopologyTiersAreInstalled
func TestTopologyTiersAreInstalled(t *testing.T) {
	if os.Getenv("SHOULDERS_MESH_E2E") != "1" {
		t.Skip("set SHOULDERS_MESH_E2E=1 to run the topology tier test")
	}
	ctx := context.Background()
	clusters := e2eClusters(t)
	clusters[0].PlatformConfig.Cilium.Version = e2eCiliumVersion
	clusters[1].PlatformConfig.Cilium.Version = e2eCiliumVersion

	plan, err := newTopologyPlan(clusters)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := prepareMesh(ctx, clusters, plan); err != nil {
		t.Fatalf("prepareMesh: %v", err)
	}

	// Install each cluster with its tier.
	for _, cluster := range clusters {
		selectCluster(cluster)
		options := bootstrap.CiliumOptionsForProfile(cluster.Profile)
		options.ContextName = targetClusterContextName()
		options.Mesh = plan.optionsFor(cluster)
		if err := bootstrap.EnsureCilium(kubeconfig, cluster.CiliumVersion(), options); err != nil {
			t.Fatalf("%s cilium: %v", cluster.Name, err)
		}
	}

	// The hub must have the platform tier, and the workload tier's own
	// telemetry agent must not be duplicated there.
	selectCluster(clusters[0])
	hubReleases := helmReleaseNames(t, clusters[0].Name)
	for _, want := range []string{"dex", "headlamp", "kube-prometheus-stack", "kyverno"} {
		if !hubReleases[want] {
			t.Errorf("the hub is missing the platform-tier release %q; it has %v", want, hubReleases)
		}
	}
	for _, unwanted := range []string{"alloy-agent", "kube-state-metrics"} {
		if hubReleases[unwanted] {
			t.Errorf("the hub installed the workload-tier release %q; the split is symmetric", unwanted)
		}
	}

	// The spoke must not have the backends ...
	selectCluster(clusters[1])
	spokeReleases := helmReleaseNames(t, clusters[1].Name)
	for _, unwanted := range []string{"dex", "kube-prometheus-stack", "loki", "tempo", "alloy"} {
		if spokeReleases[unwanted] {
			t.Errorf("the spoke installed the platform-tier release %q", unwanted)
		}
	}
	// ... but policy, its own Headlamp and its own telemetry path are per
	// cluster, so they must be there.
	for _, want := range []string{"cnpg", "crossplane", "garage", "kyverno", "headlamp", "kube-state-metrics", "alloy-agent"} {
		if !spokeReleases[want] {
			t.Errorf("the spoke is missing the workload-tier release %q; it has %v", want, spokeReleases)
		}
	}
}

// helmReleaseNames lists the HelmReleases installed in a cluster.
func helmReleaseNames(t *testing.T, clusterName string) map[string]bool {
	t.Helper()
	out, err := exec.Command("kubectl", "--context", targetClusterContextName(),
		"-A", "get", "helmrelease", "-o", "jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		t.Fatalf("list helmreleases in %s: %v", clusterName, err)
	}
	names := map[string]bool{}
	for _, name := range strings.Fields(string(out)) {
		names[name] = true
	}
	return names
}

func withMesh(options bootstrap.CiliumOptions, mesh *bootstrap.ClusterMeshOptions) bootstrap.CiliumOptions {
	options.Mesh = mesh
	options.ContextName = targetClusterContextName()
	return options
}

func kubeContext() string {
	ctx, err := kube.CurrentContext(kubeconfig)
	if err != nil {
		return ""
	}
	return ctx
}

func waitForMesh(t *testing.T, cluster *config.ResolvedCluster) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Minute)
	var last string
	for time.Now().Before(deadline) {
		selectCluster(cluster)
		out, err := exec.Command("cilium", "clustermesh", "status",
			"--context", kubeContext()).CombinedOutput()
		last = string(out)
		if err == nil && strings.Contains(last, "All 2 nodes are connected") {
			t.Logf("clustermesh status:\n%s", last)
			return
		}
		time.Sleep(20 * time.Second)
	}
	t.Fatalf("clusters did not join the mesh within the deadline; last status:\n%s", last)
}

// probeCrossCluster deploys a pod in each cluster and pings across the mesh.
func probeCrossCluster(t *testing.T, ctx context.Context, clusters []*config.ResolvedCluster) error {
	podIP := map[string]string{}
	for _, cluster := range clusters {
		selectCluster(cluster)
		execCmd(ctx, "kubectl", "-n", "meshtest-e2e", "delete", "pod", "probe-"+cluster.Name,
			"--ignore-not-found")
		execCmd(ctx, "kubectl", "create", "namespace", "meshtest-e2e")
		execCmd(ctx, "kubectl", "-n", "meshtest-e2e", "run", "probe-"+cluster.Name,
			"--image=busybox:1.36", "--restart=Never", "--command", "--", "sleep", "900")
	}
	time.Sleep(45 * time.Second)
	for _, cluster := range clusters {
		selectCluster(cluster)
		ip := strings.TrimSpace(execCmd(ctx, "kubectl", "-n", "meshtest-e2e",
			"get", "pod", "probe-"+cluster.Name, "-o", "jsonpath={.status.podIP}"))
		if ip == "" {
			return fmt.Errorf("probe pod did not get an IP in %s", cluster.Name)
		}
		podIP[cluster.Name] = ip
		t.Logf("  %s probe at %s", cluster.Name, ip)
	}

	// Ping hub -> spoke and spoke -> hub.
	for _, pair := range [][2]string{
		{clusters[0].Name, clusters[1].Name},
		{clusters[1].Name, clusters[0].Name},
	} {
		selectCluster(clusterByName(clusters, pair[0]))
		out := execCmd(ctx, "kubectl", "-n", "meshtest-e2e", "exec", "probe-"+pair[0],
			"--", "ping", "-c", "3", "-W", "4", podIP[pair[1]])
		t.Logf("ping %s -> %s:\n%s", pair[0], pair[1], out)
		if !strings.Contains(out, "0% packet loss") {
			return fmt.Errorf("cross-cluster ping %s -> %s failed:\n%s", pair[0], pair[1], out)
		}
	}
	return nil
}

func clusterByName(clusters []*config.ResolvedCluster, name string) *config.ResolvedCluster {
	for _, cluster := range clusters {
		if cluster.Name == name {
			return cluster
		}
	}
	return nil
}

// execCmd runs a command, returning combined output. Failures are returned in
// the output rather than as an error so diagnostics stay visible.
func execCmd(ctx context.Context, name string, args ...string) string {
	out, _ := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out)
}
