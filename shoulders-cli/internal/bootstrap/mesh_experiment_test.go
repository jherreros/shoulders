package bootstrap

// ClusterMesh feasibility experiment (R1a).
//
// This is a THROWAWAY harness, not shipped code. It exists to answer one
// question before the multi-cluster target is sequenced:
//
//   Does Cilium ClusterMesh work between two vCluster-in-Docker (vind)
//   clusters, given that every vCluster almost certainly gets the SAME
//   Kubernetes-assigned pod CIDR (k3s default 10.244.0.0/16)?
//
// Cilium's ClusterMesh prerequisites say "PodCIDR ranges in all clusters and
// all nodes must be non-conflicting and unique IP addresses", so on paper this
// should fail. The hypothesis is that it works anyway because:
//
//  1. vind always uses private nodes (Docker containers, own netns), so the
//     overlap never becomes a host-level routing conflict, and
//  2. ClusterMesh identifies remote endpoints by (cluster ID, IP), so flat IP
//     uniqueness is only genuinely required in NATIVE ROUTING mode.
//
// See docs/multi-cluster-target.md R1a.
//
// WHY A GO TEST: vCluster-in-Docker is created through the loft-sh/vcluster Go
// library (see EnsureVindCluster), not a CLI binary, and this package is
// internal/ so nothing outside the module can import it. A test is the only way
// to reuse the exact same creation path the product uses, without shipping a new
// command.
//
// WHY NOT `shoulders up`: that installs the whole platform (Flux, Crossplane,
// CNPG, Garage, Headlamp...) — ~37 pods per cluster, ~74 for two, plus the image
// pulls that the roadmap documents as the DiskPressure trigger. None of that is
// needed to test the mesh. This harness creates BARE clusters (~5 pods each) and
// leaves the Cilium/ClusterMesh half to the shell script, so that step is
// visible live rather than buried in a Go test.
//
// USAGE:
//	SHOULDERS_MESH_EXPERIMENT=1 go test ./internal/bootstrap/ -run TestMeshExperiment -v -timeout 40m
//
// CLEANUP:
//	go test ./internal/bootstrap/ -run TestMeshExperimentCleanup -v
//
// CLILIUM VALUES (the second half, run by hand so it is visible live).
// Derived from 2-addons/manifests/helm-releases/cilium.yaml, with three
// additions ClusterMesh requires and the repo does not yet have:
//
//	cluster:
//	  name: mesha          # per cluster; <=32 chars, lowercase alnum + dashes
//	  id: 1                # per cluster; 1-255, MUST match maxConnectedClusters
//	clustermesh:
//	  useAPIServer: true
//	  apiserver:
//	    service:
//	      type: NodePort   # avoids LoadBalancer port contention with everything else
//	    tls:
//	      auto:
//	        enabled: true
//	        method: helm   # the only viable option in a vCluster: no CronJob,
//	                       # no cert-manager. Regenerates on every Helm upgrade.
//	  config:
//	    enabled: true
//	    clusters:          # omits the local cluster; Cilium ignores self
//	      mesha: { address: <cp-a>, port: 2379 }
//	      meshb: { address: <cp-b>, port: 2379 }
//	routingMode: tunnel   # NOT native. Tunnel/encapsulation is the whole reason
//	                       # overlapping pod CIDRs are expected to be tolerable:
//	                       # traffic tunnels to the remote NODE ip and the remote
//	                       # cluster decapsulates and routes locally, so identity
//	                       # is (cluster ID, ip) rather than a flat address space.
//	protocol: vxlan
//
// Note the repo's cilium.yaml pins chart 1.20.2 and does NOT set routingMode or
// protocol. If the experiment passes only because of tunnel mode, that needs to
// become an explicit pin in the repo — otherwise a future Cilium default change
// could silently reintroduce native routing and break the mesh.
//
// WHAT THE EXPERIMENT MUST RECORD (this is the actual evidence):
//  1. `kubectl --context mesha get nodes -o wide` and the same for meshb — do the
//     allocated pod CIDRs actually collide? Capture the real values.
//  2. `cilium clustermesh status --wait` on both clusters.
//  3. `cilium connectivity test --context mesha --multi-cluster meshb`.
//  4. A cross-cluster CiliumNetworkPolicy. The cluster selector is
//     `k8s:io.cilium.k8s.policy.cluster`; endpoints carry no
//     `k8s:io.cilium.network-name` label (the original experiment used that
//     name and was wrong — docs/multi-cluster-evidence.md §19). Note that Cilium
//     1.19+ scopes policy to the local cluster by default; a cross-cluster rule
//     has to name the remote cluster explicitly.
//
// VERDICT: if 1 shows identical CIDRs and 2-4 still pass, R1a is downgraded to a
// non-issue and D11 stands. If 2 fails on CIDR conflict, fall back per the table
// in docs/multi-cluster-target.md R1a (per-cluster --cluster-cidr, then
// Submariner/Globalnet).

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
	"github.com/jherreros/shoulders/shoulders-cli/internal/manifests"
)

// meshVindValues returns per-cluster vCluster values.
//
// CIDR MODE (env MESH_CIDR_MODE):
//
//	"default" (absent) - omit networking.podCIDR entirely, so the cluster gets
//	    the vCluster chart default. EVIDENCE-GATHERING MODE: this is what the
//	    product does today.
//	"distinct" - set networking.podCIDR per cluster. This is the candidate fix.
//
// CONFIRMED FROM THE PINNED SOURCE (loft-sh/vcluster v0.37.2, chart 0.33.1):
//
//	chart/values.yaml:1169 -> networking.podCIDR: "10.244.0.0/16"
//	pkg/k8s/k8s.go:182    -> args = append(args, "--cluster-cidr="+PodCIDR)
//	chart comment         -> "should only be set if privateNodes.enabled is true"
//
// vind forces private nodes (worker nodes are Docker containers), so that
// default applies to every cluster. Since 1-cluster/vind-config*.yaml never sets
// networking.podCIDR, EVERY vind cluster the product creates today is allocated
// the identical pod CIDR 10.244.0.0/16. The collision is therefore a certainty,
// not a risk to be assessed.
// meshVindValues renders cluster values through the PRODUCT code path
// (manifests.VindValues) rather than hand-rolling them, so this experiment
// validates the shipped renderer and not a private copy of it.
//
// This also exercises per-cluster host ports (target-doc risk R3): the two
// experiment clusters get 80/443 and 8080/8443 respectively, which is what
// makes two vind clusters able to coexist at all.
func meshVindValues(t *testing.T, name string) []byte {
	t.Helper()
	mesh := config.MeshConfig{Enabled: true, Network: os.Getenv("MESH_DOCKER_NETWORK")}
	if mesh.Network == "" {
		mesh.Network = config.DefaultMeshNetwork
	}

	// Resolved through the real topology model so this also exercises config
	// resolution, not just rendering. The hub keeps 80/443 and the spoke takes
	// 8080/8443, which is what lets two vind clusters coexist at all.
	cfg := &config.Config{
		Platform: config.PlatformConfig{Profile: config.ProfileSmall, Mesh: mesh},
		Clusters: []config.ClusterEntry{
			{Name: meshHubName, Role: config.RoleHub},
			{Name: meshSpokeName, Role: config.RoleSpoke, Ports: &config.PortMapping{HTTP: 8080, HTTPS: 8443}},
		},
	}
	cfg.ApplyDefaults()

	cluster, err := cfg.ResolveCluster(name)
	if err != nil {
		t.Fatalf("resolve cluster %s: %v", name, err)
	}

	rendered, err := manifests.VindValues(config.ProfileSmall, *cluster)
	if err != nil {
		t.Fatalf("render vind values for %s: %v", name, err)
	}

	// The worker-node list MUST survive: the product's small profile creates
	// control-plane + 1 worker, so dropping it would validate a topology the
	// product never builds. An earlier revision of this harness deleted it and
	// therefore proved the mesh on single-node clusters only.
	var values map[string]interface{}
	if err := yaml.Unmarshal(rendered, &values); err != nil {
		t.Fatalf("parse rendered values for %s: %v", name, err)
	}
	docker, ok := values["experimental"].(map[string]interface{})["docker"].(map[string]interface{})
	if !ok || docker["network"] == nil {
		t.Fatalf("shared network missing from rendered values for %s:\n%s", name, rendered)
	}
	if _, ok := docker["nodes"]; !ok {
		t.Fatalf("worker node list missing from rendered values for %s:\n%s", name, rendered)
	}
	return rendered
}

// meshClusters are the two throwaway cluster names. Keep them short: vCluster
// appends suffixes to container names and long names get unwieldy in kubectl.
//
// NOTE ON PORTS: meshVindValues deliberately omits experimental.docker.ports.
// 1-cluster/vind-config-small.yaml hardcodes 80:80 and 443:443, so two clusters
// physically cannot coexist. That is target-doc risk R3 biting in practice, and
// the mesh needs no ingress at all.
const (
	meshHubName   = "mesha"
	meshSpokeName = "meshb"
)

var meshClusters = []string{meshHubName, meshSpokeName}

func meshExperimentEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("SHOULDERS_MESH_EXPERIMENT") != "1" {
		t.Skip("set SHOULDERS_MESH_EXPERIMENT=1 to run the ClusterMesh feasibility experiment")
	}
}

// TestMeshExperiment creates the two bare vind clusters the mesh test runs
// against. It deliberately does NOT install Cilium — see mesh-experiment.sh for
// the ClusterMesh half, which is run separately so it can be watched live.
func TestMeshExperiment(t *testing.T) {
	meshExperimentEnabled(t)

	mode := os.Getenv("MESH_CIDR_MODE")
	if mode == "" {
		mode = "default"
	}
	t.Logf("CIDR mode: %s", mode)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	for _, name := range meshClusters {
		name := name
		t.Run("create_"+name, func(t *testing.T) {
			t.Logf("creating bare vind cluster %q via the product renderer (no host ports bound)", name)
			if err := EnsureVindCluster(ctx, name, meshVindValues(t, name), nil, ""); err != nil {
				t.Fatalf("create %s: %v", name, err)
			}
			t.Logf("cluster %q created", name)
		})
	}
}

// TestMeshExperimentCleanup removes the experiment clusters.
//
// Cleanup is intentionally a separate, separately-invoked test so that it can be
// run WITHOUT the SHOULDERS_MESH_EXPERIMENT gate — otherwise a failed
// experiment would leave two clusters eating disk, which is precisely the
// DiskPressure risk this project keeps running into.
func TestMeshExperimentCleanup(t *testing.T) {
	if os.Getenv("SHOULDERS_MESH_EXPERIMENT_CLEANUP") != "1" {
		t.Skip("set SHOULDERS_MESH_EXPERIMENT_CLEANUP=1 to tear down the experiment clusters")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	for _, name := range meshClusters {
		t.Run("delete_"+name, func(t *testing.T) {
			if err := DeleteVindCluster(ctx, name); err != nil {
				t.Errorf("delete %s: %v", name, err)
				return
			}
			fmt.Printf("deleted %s\n", name)
		})
	}
}
