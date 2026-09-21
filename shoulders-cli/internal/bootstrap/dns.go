package bootstrap

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fallbackNameservers are used when the host provides no usable upstream.
var fallbackNameservers = []string{"1.1.1.1", "8.8.8.8"}

// EnsureClusterDNS repairs vind's frozen upstream DNS. vind writes
// /etc/k8s-resolv.conf once at cluster creation and re-applies a stock
// CoreDNS ConfigMap forwarding to it, so any later change in Docker
// Desktop's host resolver silently breaks all in-cluster DNS. This repoints
// the CoreDNS forward to the machine's current nameservers (public
// fallbacks when the host offers none usable) and restarts CoreDNS when
// the ConfigMap changed. Best effort: returns nil when already correct.
func EnsureClusterDNS(ctx context.Context, kubeconfigPath string) (changed bool, err error) {
	servers := hostNameservers()
	clientset, err := kube.NewClientset(kubeconfigPath)
	if err != nil {
		return false, fmt.Errorf("create clientset: %w", err)
	}
	configMap, err := clientset.CoreV1().ConfigMaps("kube-system").Get(ctx, "coredns", metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("read coredns config: %w", err)
	}
	corefile := configMap.Data["Corefile"]
	patched, updated := patchCorefileForward(corefile, servers)
	if !updated {
		return false, nil
	}
	configMap.Data["Corefile"] = patched
	if _, err := clientset.CoreV1().ConfigMaps("kube-system").Update(ctx, configMap, metav1.UpdateOptions{}); err != nil {
		return false, fmt.Errorf("update coredns config: %w", err)
	}
	if err := clientset.CoreV1().Pods("kube-system").DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: "k8s-app=kube-dns"}); err != nil {
		return true, fmt.Errorf("restart coredns: %w", err)
	}
	return true, nil
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

// patchCorefileForward replaces a `forward . /etc/resolv.conf` line with
// explicit upstreams. Reports whether anything changed.
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
	return strings.Join(lines, "\n"), changed
}
