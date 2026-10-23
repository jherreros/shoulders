package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// fallbackNameservers are used when the host provides no usable upstream.
var fallbackNameservers = []string{"1.1.1.1", "8.8.8.8"}

// CoreDNSPodSelector matches CoreDNS pods across the environments Shoulders
// runs in.
//
// This selector was previously hardcoded to "k8s-app=kube-dns". That is the
// stock Kubernetes label, but vCluster labels its CoreDNS pods
// "k8s-app=vcluster-kube-dns", so the delete matched nothing, returned success,
// and EnsureClusterDNS silently patched the Corefile without ever restarting
// CoreDNS — the fix never took effect. Both labels are matched here, and the
// restart is verified, so the failure mode cannot recur silently.
const CoreDNSPodSelector = "k8s-app in (kube-dns,vcluster-kube-dns)"

// EnsureClusterDNS repairs vind's frozen upstream DNS and injects any static
// host entries. vind writes /etc/k8s-resolv.conf once at cluster creation and
// re-applies a stock CoreDNS ConfigMap forwarding to it, so any later change in
// Docker Desktop's host resolver silently breaks all in-cluster DNS.
//
// best effort in the DNS-repair sense: returns nil when already correct.
func EnsureClusterDNS(ctx context.Context, kubeconfigPath string) (changed bool, err error) {
	return EnsureClusterDNSWithHosts(ctx, kubeconfigPath, nil)
}

// EnsureClusterDNSWithHosts is EnsureClusterDNS plus a set of static
// host -> address entries written into CoreDNS.
//
// This exists for inter-cluster name resolution (e.g. Cilium ClusterMesh peers,
// which must be reached by DNS name because their certificates carry DNS SANs
// rather than IP SANs).
//
// IMPORTANT: the entries are merged into the Corefile's EXISTING "hosts" block.
// CoreDNS rejects a server block containing the hosts plugin twice
// ("plugin/hosts: this plugin can only be used once per Server Block"), and
// vCluster's stock Corefile already declares "hosts /etc/coredns/NodeHosts". A
// naive append puts CoreDNS into CrashLoopBackOff.
func EnsureClusterDNSWithHosts(ctx context.Context, kubeconfigPath string, hosts []string) (changed bool, err error) {
	clientset, err := kube.NewClientset(kubeconfigPath)
	if err != nil {
		return false, fmt.Errorf("create clientset: %w", err)
	}
	return ensureClusterDNSWithHosts(ctx, clientset, hosts)
}

func ensureClusterDNSWithHosts(ctx context.Context, clientset kubernetes.Interface, hosts []string) (changed bool, err error) {
	configMap, err := clientset.CoreV1().ConfigMaps("kube-system").Get(ctx, "coredns", metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("read coredns config: %w", err)
	}

	corefile := configMap.Data["Corefile"]
	patched := corefile
	updated := false

	if next, didChange := patchCorefileForward(patched, hostNameservers()); didChange {
		patched = next
		updated = true
	}
	if next, didChange := patchCorefileHosts(patched, hosts); didChange {
		patched = next
		updated = true
	}
	if !updated {
		return false, nil
	}

	configMap.Data["Corefile"] = patched
	if _, err := clientset.CoreV1().ConfigMaps("kube-system").Update(ctx, configMap, metav1.UpdateOptions{}); err != nil {
		return false, fmt.Errorf("update coredns config: %w", err)
	}
	if err := restartCoreDNS(ctx, clientset); err != nil {
		return true, err
	}
	return true, nil
}

// ErrCoreDNSNotRunning reports that the Corefile was patched but no CoreDNS pod
// exists to restart.
//
// It is a distinct error, not a generic failure, because whether it is a bug or
// merely too-early depends entirely on the caller. For a cluster whose CNI is
// already installed it is the silent-no-op bug R1d was about and must be loud.
// For a cluster created seconds ago it is expected, because a pod with no CNI
// cannot be scheduled, and the patched Corefile takes effect when the first
// CoreDNS pod starts.
var ErrCoreDNSNotRunning = errors.New("coredns config updated but no coredns pod is running")

// restartCoreDNS deletes the CoreDNS pods so they pick up the new Corefile, and
// fails with ErrCoreDNSNotRunning if the selector matches nothing. A silent
// no-op here is exactly the bug this function exists to avoid.
func restartCoreDNS(ctx context.Context, clientset kubernetes.Interface) error {
	pods, err := clientset.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{LabelSelector: CoreDNSPodSelector})
	if err != nil {
		return fmt.Errorf("list coredns pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("%w (selector %q); the Corefile will not take effect until CoreDNS starts",
			ErrCoreDNSNotRunning, CoreDNSPodSelector)
	}
	if err := clientset.CoreV1().Pods("kube-system").DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: CoreDNSPodSelector}); err != nil {
		return fmt.Errorf("restart coredns: %w", err)
	}
	return nil
}

// SeedCoreDNSHosts adds host entries to the Corefile without requiring a running
// CoreDNS pod, and reports whether one was restarted.
//
// This exists because of a genuine ordering constraint: CoreDNS cannot become
// Ready before the CNI is installed, because a pod with no network cannot be
// scheduled. So anything that must seed DNS *before* Cilium — which is exactly
// what a mesh does, since peers are dialled by name — cannot wait for CoreDNS.
// Patching the ConfigMap early is still correct: CoreDNS reads its Corefile at
// startup, so the entries are live the moment its first pod comes up.
//
// The restart is therefore best-effort here, unlike EnsureClusterDNSWithHosts,
// which fails loudly because by then a CoreDNS pod is always expected to exist.
func SeedCoreDNSHosts(ctx context.Context, kubeconfigPath string, hosts []string) (restarted bool, err error) {
	clientset, err := kube.NewClientset(kubeconfigPath)
	if err != nil {
		return false, fmt.Errorf("create clientset: %w", err)
	}
	return seedCoreDNSHosts(ctx, clientset, hosts)
}

func seedCoreDNSHosts(ctx context.Context, clientset kubernetes.Interface, hosts []string) (restarted bool, err error) {
	configMap, err := clientset.CoreV1().ConfigMaps("kube-system").Get(ctx, "coredns", metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("read coredns config: %w", err)
	}

	patched, changed := patchCorefileHosts(configMap.Data["Corefile"], hosts)
	if !changed {
		return false, nil
	}
	configMap.Data["Corefile"] = patched
	if _, err := clientset.CoreV1().ConfigMaps("kube-system").Update(ctx, configMap, metav1.UpdateOptions{}); err != nil {
		return false, fmt.Errorf("update coredns config: %w", err)
	}

	pods, err := clientset.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{LabelSelector: CoreDNSPodSelector})
	if err != nil {
		// The ConfigMap is already patched, which is the part that matters.
		return false, nil
	}
	if len(pods.Items) == 0 {
		return false, nil
	}
	if err := clientset.CoreV1().Pods("kube-system").DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: CoreDNSPodSelector}); err != nil {
		return false, fmt.Errorf("restart coredns: %w", err)
	}
	return true, nil
}

// WaitForCoreDNS blocks until at least one CoreDNS pod exists and is Ready.
//
// Seeding the Corefile right after a cluster is created races the scheduler:
// the ConfigMap can be patched before CoreDNS has been scheduled at all, and
// restartCoreDNS then correctly refuses to continue because nothing matched the
// selector. Callers that must patch CoreDNS early — the topology install seeds
// mesh peer names this way — wait here first.
func WaitForCoreDNS(ctx context.Context, kubeconfigPath string, timeout time.Duration) error {
	clientset, err := kube.NewClientset(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("create clientset: %w", err)
	}
	deadline := time.Now().Add(timeout)
	var last string
	for {
		pods, err := clientset.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{LabelSelector: CoreDNSPodSelector})
		if err != nil {
			return fmt.Errorf("list coredns pods: %w", err)
		}
		for _, pod := range pods.Items {
			if podReady(&pod) {
				return nil
			}
			last = fmt.Sprintf("%s is %s", pod.Name, pod.Status.Phase)
		}
		if len(pods.Items) > 0 {
			last = fmt.Sprintf("%d pod(s), none ready yet", len(pods.Items))
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("coredns did not become ready within %s (%s)", timeout, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// podReady reports whether a pod has at least one container passing its readiness
// probe.
func podReady(pod *corev1.Pod) bool {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Ready {
			return true
		}
	}
	return false
}

// hostNameservers reads the machine's /etc/resolv.conf, skipping loopback
// entries that are meaningless inside the cluster. Falls back to public
// resolvers when nothing usable remains.
func hostNameservers() []string {
	content, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return fallbackNameservers
	}
	servers := parseNameservers(string(content))
	if len(servers) == 0 {
		return fallbackNameservers
	}
	return servers
}

func parseNameservers(content string) []string {
	servers := []string{}
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 || fields[0] != "nameserver" {
			continue
		}
		ip := strings.TrimSpace(fields[1])
		if ip == "" || strings.HasPrefix(ip, "127.") || ip == "::1" {
			continue
		}
		servers = append(servers, ip)
	}
	return servers
}

func patchCorefileForward(corefile string, servers []string) (string, bool) {
	if len(servers) == 0 {
		servers = fallbackNameservers
	}
	desired := "forward . " + strings.Join(servers, " ")
	lines := strings.Split(corefile, "\n")
	changed := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "forward . ") {
			continue
		}
		if trimmed == desired {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		lines[i] = indent + desired
		changed = true
	}
	if !changed {
		return corefile, false
	}
	return strings.Join(lines, "\n"), true
}

// patchCorefileHosts injects static host entries into the Corefile's existing
// "hosts" block, creating the block only if one does not already exist.
//
// It is idempotent: entries already present produce no change.
func patchCorefileHosts(corefile string, entries []string) (string, bool) {
	entries = normaliseHostEntries(entries)
	if len(entries) == 0 {
		return corefile, false
	}

	lines := strings.Split(corefile, "\n")
	start := hostsBlockStart(lines)
	if start < 0 {
		return insertHostsBlock(lines, entries)
	}
	return mergeIntoHostsBlock(lines, start, entries)
}

// normaliseHostEntries accepts "ip host" pairs, drops blanks and duplicates, and
// preserves the caller's order.
func normaliseHostEntries(entries []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		fields := strings.Fields(entry)
		if len(fields) < 2 {
			continue
		}
		normalised := fields[0] + " " + fields[1]
		if seen[normalised] {
			continue
		}
		seen[normalised] = true
		out = append(out, normalised)
	}
	return out
}

// hostsBlockStart returns the index of the line declaring a "hosts" block, or
// -1. The plugin may be declared as "hosts /etc/coredns/NodeHosts {" or
// "hosts {".
func hostsBlockStart(lines []string) int {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "hosts ") && trimmed != "hosts {" {
			continue
		}
		// Ignore unrelated directives that merely start with "hosts".
		if strings.HasSuffix(trimmed, "{") {
			return i
		}
	}
	return -1
}

// hostsBlockEnd returns the index of the closing "}" for the block opened at
// start, or len(lines) when the block is unterminated.
func hostsBlockEnd(lines []string, start int) int {
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "}" {
			return i
		}
	}
	return len(lines)
}

// mergeIntoHostsBlock inserts entries immediately before the closing brace of an
// existing hosts block, so the plugin is never declared twice.
func mergeIntoHostsBlock(lines []string, start int, entries []string) (string, bool) {
	end := hostsBlockEnd(lines, start)
	existing := strings.Join(lines[start:end], "\n")

	var fresh []string
	for _, entry := range entries {
		if !strings.Contains(existing, entry) {
			fresh = append(fresh, entry)
		}
	}
	if len(fresh) == 0 {
		return strings.Join(lines, "\n"), false
	}

	// Match the indentation of the block's own body lines.
	indent := "        "
	for i := start + 1; i < end; i++ {
		trimmed := strings.TrimLeft(lines[i], " \t")
		if trimmed != "" && trimmed != "}" {
			indent = lines[i][:len(lines[i])-len(trimmed)]
			break
		}
	}

	inserted := make([]string, 0, len(fresh))
	for _, entry := range fresh {
		inserted = append(inserted, indent+entry)
	}

	merged := make([]string, 0, len(lines)+len(inserted))
	// Records must be inserted directly after the opening line, NOT at the end
	// of the block.
	//
	// The hosts plugin's grammar is:
	//
	//	hosts [FILE] [ZONES...] {
	//	    [INLINE]          <- records
	//	    fallthrough ...
	//	    reload DURATION
	//	    ttl SECONDS
	//	}
	//
	// Everything after the first directive is parsed as a directive *property*,
	// so an address appended to the end of a block that already declares
	// "fallthrough" makes CoreDNS fail to start:
	//
	//	plugin/hosts: Corefile:14 - Error during parsing: unknown property '10.90.0.2'
	//
	// That is a crash loop, not a degraded lookup: the cluster loses DNS
	// entirely, which is why mesh peers became unresolvable.
	merged = append(merged, lines[:start+1]...)
	merged = append(merged, inserted...)
	merged = append(merged, lines[start+1:end]...)
	merged = append(merged, lines[end:]...)
	return strings.Join(merged, "\n"), true
}

// insertHostsBlock adds a hosts block ahead of the forward directive when the
// Corefile has none.
func insertHostsBlock(lines []string, entries []string) (string, bool) {
	block := []string{"    hosts {"}
	for _, entry := range entries {
		block = append(block, "        "+entry)
	}
	block = append(block, "        fallthrough", "    }")

	// Prefer inserting directly before "forward"; otherwise prepend inside the
	// first server block.
	insertAt := len(lines)
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "forward . ") {
			insertAt = i
			break
		}
	}

	out := make([]string, 0, len(lines)+len(block))
	out = append(out, lines[:insertAt]...)
	out = append(out, block...)
	out = append(out, lines[insertAt:]...)
	return strings.Join(out, "\n"), true
}
