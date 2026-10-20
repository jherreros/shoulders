package bootstrap

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestParseNameservers(t *testing.T) {
	content := "# comment\nnameserver 1.1.1.1\nnameserver 127.0.0.11\nnameserver ::1\nsearch lan\nnameserver 8.8.8.8\n"
	servers := parseNameservers(content)
	if len(servers) != 2 || servers[0] != "1.1.1.1" || servers[1] != "8.8.8.8" {
		t.Fatalf("unexpected servers: %v", servers)
	}
	if servers := parseNameservers("nameserver 127.0.0.1\n"); len(servers) != 0 {
		t.Fatalf("expected loopback to be filtered, got %v", servers)
	}
}

func TestPatchCorefileForward(t *testing.T) {
	corefile := ".:53 {\n    errors\n    forward . /etc/resolv.conf\n    cache 30\n}\n"
	patched, changed := patchCorefileForward(corefile, []string{"1.1.1.1", "8.8.8.8"})
	if !changed {
		t.Fatalf("expected a change")
	}
	if !strings.Contains(patched, "forward . 1.1.1.1 8.8.8.8") {
		t.Fatalf("unexpected patched corefile:\n%s", patched)
	}
	if strings.Contains(patched, "/etc/resolv.conf") {
		t.Fatalf("stale forward survived:\n%s", patched)
	}
	// Idempotent: already correct.
	if _, changed := patchCorefileForward(patched, []string{"1.1.1.1", "8.8.8.8"}); changed {
		t.Fatalf("expected no change on second pass")
	}
	// Empty servers fall back to public resolvers.
	patched, changed = patchCorefileForward(corefile, nil)
	if !changed || !strings.Contains(patched, "forward . 1.1.1.1 8.8.8.8") {
		t.Fatalf("expected fallback servers, got:\n%s", patched)
	}
}

// vclusterCorefile is the real CoreDNS ConfigMap shipped by vCluster, captured
// from a live vind cluster. It is the regression fixture: it already declares a
// "hosts" block, so naively appending a second one puts CoreDNS into
// CrashLoopBackOff with "plugin/hosts: this plugin can only be used once per
// Server Block".
const vclusterCorefile = `.:1053 {
    errors
    health
    ready
    rewrite name regex .*\.nodes\.vcluster\.com kubernetes.default.svc.cluster.local
    kubernetes cluster.local in-addr.arpa ip6.arpa {
        pods insecure
        fallthrough in-addr.arpa ip6.arpa
    }
    hosts /etc/coredns/NodeHosts {
        ttl 60
        reload 15s
        fallthrough
    }
    prometheus :9153
    forward . /etc/resolv.conf
    cache 30
    loop
    reload
    loadbalance
}

import /etc/coredns/custom/*.server
`

func TestPatchCorefileHostsMergesIntoExistingBlock(t *testing.T) {
	patched, changed := patchCorefileHosts(vclusterCorefile, []string{"10.90.0.3 meshb.mesh.cilium.io"})
	if !changed {
		t.Fatal("expected a change")
	}
	if !strings.Contains(patched, "10.90.0.3 meshb.mesh.cilium.io") {
		t.Fatalf("entry missing:\n%s", patched)
	}
	// The plugin must still be declared exactly once.
	if got := strings.Count(patched, "hosts /etc/coredns/NodeHosts {"); got != 1 {
		t.Fatalf("hosts plugin declared %d times, want 1:\n%s", got, patched)
	}
	// The entry must live inside the hosts block, before its closing brace.
	start := strings.Index(patched, "hosts /etc/coredns/NodeHosts {")
	end := strings.Index(patched[start:], "}")
	if end < 0 {
		t.Fatal("unterminated hosts block")
	}
	block := patched[start : start+end]
	if !strings.Contains(block, "10.90.0.3 meshb.mesh.cilium.io") {
		t.Fatalf("entry not inside hosts block:\n%s", block)
	}
	// Indentation must match the block body.
	if !strings.Contains(patched, "        10.90.0.3 meshb.mesh.cilium.io\n") {
		t.Fatalf("unexpected indentation:\n%s", patched)
	}
	// Everything else must survive.
	for _, want := range []string{"ttl 60", "reload 15s", "prometheus :9153", "import /etc/coredns/custom/*.server"} {
		if !strings.Contains(patched, want) {
			t.Fatalf("lost %q:\n%s", want, patched)
		}
	}
}

func TestPatchCorefileHostsIsIdempotent(t *testing.T) {
	entries := []string{"10.90.0.2 mesha.mesh.cilium.io", "10.90.0.3 meshb.mesh.cilium.io"}
	once, changed := patchCorefileHosts(vclusterCorefile, entries)
	if !changed {
		t.Fatal("expected first pass to change the corefile")
	}
	twice, changed := patchCorefileHosts(once, entries)
	if changed {
		t.Fatalf("expected no change on second pass:\n%s", twice)
	}
	if once != twice {
		t.Fatalf("corefile drifted between passes")
	}
}

func TestPatchCorefileHostsCreatesBlockWhenAbsent(t *testing.T) {
	corefile := ".:53 {\n    errors\n    forward . /etc/resolv.conf\n    cache 30\n}\n"
	patched, changed := patchCorefileHosts(corefile, []string{"10.90.0.3 meshb.mesh.cilium.io"})
	if !changed {
		t.Fatal("expected a change")
	}
	if !strings.Contains(patched, "meshb.mesh.cilium.io") {
		t.Fatalf("entry missing:\n%s", patched)
	}
	if strings.Count(patched, "hosts {") != 1 {
		t.Fatalf("expected exactly one hosts block:\n%s", patched)
	}
	// Must be inserted before forward so it takes effect.
	if strings.Index(patched, "hosts {") > strings.Index(patched, "forward . ") {
		t.Fatalf("hosts block placed after forward:\n%s", patched)
	}
}

func TestPatchCorefileHostsNoopCases(t *testing.T) {
	if _, changed := patchCorefileHosts(vclusterCorefile, nil); changed {
		t.Fatal("nil entries should not change the corefile")
	}
	if _, changed := patchCorefileHosts(vclusterCorefile, []string{"", "   "}); changed {
		t.Fatal("blank entries should not change the corefile")
	}
	// Malformed (no address) entries are dropped rather than injected.
	if _, changed := patchCorefileHosts(vclusterCorefile, []string{"just-a-hostname"}); changed {
		t.Fatal("malformed entry should be dropped")
	}
}

func TestNormaliseHostEntries(t *testing.T) {
	got := normaliseHostEntries([]string{
		"  10.0.0.2   host-a  ",
		"10.0.0.2 host-a",
		"10.0.0.3 host-b extra-ignored",
		"",
	})
	want := []string{"10.0.0.2 host-a", "10.0.0.3 host-b"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

// TestRestartCoreDNSSelectsVclusterLabel is the regression test for the bug
// found on 2026-10-18: the selector was "k8s-app=kube-dns" while vCluster
// labels CoreDNS "k8s-app=vcluster-kube-dns", so the restart matched nothing and
// the Corefile change silently never took effect.
func TestRestartCoreDNSSelectsVclusterLabel(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "coredns-6dc6dfcb8f-7v76f",
			Namespace: "kube-system",
			Labels:    map[string]string{"k8s-app": "vcluster-kube-dns"},
		},
	})

	if err := restartCoreDNS(context.Background(), clientset); err != nil {
		t.Fatalf("expected restart to succeed for vcluster-labelled CoreDNS: %v", err)
	}

	deleted := false
	for _, action := range clientset.Actions() {
		if action.GetVerb() != "delete-collection" {
			continue
		}
		if action.(k8stesting.DeleteCollectionActionImpl).ListRestrictions.Labels.String() == CoreDNSPodSelector {
			deleted = true
		}
	}
	if !deleted {
		t.Fatal("expected a delete-collection using CoreDNSPodSelector")
	}
}

func TestRestartCoreDNSSelectsStockLabel(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "coredns-abc123",
			Namespace: "kube-system",
			Labels:    map[string]string{"k8s-app": "kube-dns"},
		},
	})
	if err := restartCoreDNS(context.Background(), clientset); err != nil {
		t.Fatalf("expected restart to succeed for stock-labelled CoreDNS: %v", err)
	}
}

// TestRestartCoreDNSFailsLoudlyWhenNothingMatches guards the silent-failure mode:
// if the label ever drifts again, the caller must be told rather than assuming
// the Corefile took effect.
func TestRestartCoreDNSFailsLoudlyWhenNothingMatches(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "coredns-mystery",
			Namespace: "kube-system",
			Labels:    map[string]string{"app": "something-else"},
		},
	})
	err := restartCoreDNS(context.Background(), clientset)
	if err == nil {
		t.Fatal("expected an error when no CoreDNS pods match the selector")
	}
	if !strings.Contains(err.Error(), CoreDNSPodSelector) {
		t.Fatalf("error should name the selector, got: %v", err)
	}
}

// --- CoreDNS restart semantics ------------------------------------------------

const corednsConfigMap = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: coredns
  namespace: kube-system
data:
  Corefile: |
    .:53 {
        hosts /etc/coredns/NodeHosts {
            10.90.0.2 hub.mesh.cilium.io
            fallthrough
        }
        forward . 1.1.1.1
    }
`

func coreDNSPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "kube-system",
			Labels:    map[string]string{"k8s-app": "kube-dns"},
		},
	}
}

// A cluster with no CNI has no schedulable CoreDNS pod. That must be reported
// distinctly rather than as a generic failure, because the Corefile patch is
// still correct and takes effect on the first pod start.
func TestRestartCoreDNSReportsNotRunningWhenNoPodsExist(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	err := restartCoreDNS(context.Background(), clientset)
	if !errors.Is(err, ErrCoreDNSNotRunning) {
		t.Fatalf("expected ErrCoreDNSNotRunning, got %v", err)
	}
	if errors.Is(err, ErrCoreDNSNotRunning) == false {
		t.Fatal("errors.Is must match so callers can distinguish this case")
	}
}

func TestSeedCoreDNSHostsSucceedsWithoutCoreDNSPods(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	if err := createCoreDNSConfigMap(clientset); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	restarted, err := seedCoreDNSHosts(context.Background(), clientset, []string{"10.90.0.4 spoke-a.mesh.cilium.io"})
	if err != nil {
		t.Fatalf("seeding before CoreDNS runs must succeed: %v", err)
	}
	if restarted {
		t.Fatal("nothing could have been restarted")
	}
	// The point of the whole function: the entry is in the ConfigMap already.
	assertCorefileContains(t, clientset, "spoke-a.mesh.cilium.io")
}

func TestSeedCoreDNSHostsRestartsRunningCoreDNS(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	if err := createCoreDNSConfigMap(clientset); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	clientset.CoreV1().Pods("kube-system").Create(context.Background(), coreDNSPod("coredns-1"), metav1.CreateOptions{})

	restarted, err := seedCoreDNSHosts(context.Background(), clientset, []string{"10.90.0.4 spoke-a.mesh.cilium.io"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !restarted {
		t.Fatal("a running CoreDNS should have been restarted to pick up the entry")
	}
	assertCorefileContains(t, clientset, "spoke-a.mesh.cilium.io")
}

func TestSeedCoreDNSHostsIsIdempotent(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	if err := createCoreDNSConfigMap(clientset); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	hosts := []string{"10.90.0.4 spoke-a.mesh.cilium.io"}
	if _, err := seedCoreDNSHosts(context.Background(), clientset, hosts); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	restarted, err := seedCoreDNSHosts(context.Background(), clientset, hosts)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if restarted {
		t.Fatal("re-seeding identical entries must be a no-op, not a restart")
	}
}

// A mesh seeds DNS before Cilium exists; EnsureClusterDNSWithHosts is used after
// it does, and there a missing pod IS the R1d silent-no-op bug.
func TestEnsureClusterDNSWithHostsStillFailsLoudlyWithoutPods(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	if err := createCoreDNSConfigMap(clientset); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	_, err := ensureClusterDNSWithHosts(context.Background(), clientset, []string{"10.90.0.4 spoke-a.mesh.cilium.io"})
	if !errors.Is(err, ErrCoreDNSNotRunning) {
		t.Fatalf("expected the not-running error to stay loud, got %v", err)
	}
}

func createCoreDNSConfigMap(clientset *fake.Clientset) error {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "coredns", Namespace: "kube-system"},
		Data: map[string]string{
			"Corefile": `.:53 {
    hosts /etc/coredns/NodeHosts {
        10.90.0.2 hub.mesh.cilium.io
        fallthrough
    }
    forward . 1.1.1.1
}
`,
		},
	}
	_, err := clientset.CoreV1().ConfigMaps("kube-system").Create(context.Background(), configMap, metav1.CreateOptions{})
	return err
}

func assertCorefileContains(t *testing.T, clientset *fake.Clientset, want string) {
	t.Helper()
	configMap, err := clientset.CoreV1().ConfigMaps("kube-system").Get(context.Background(), "coredns", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read coredns configmap: %v", err)
	}
	if !strings.Contains(configMap.Data["Corefile"], want) {
		t.Fatalf("Corefile does not contain %q:\n%s", want, configMap.Data["Corefile"])
	}
}

// --- Corefile grammar ---------------------------------------------------------
//
// The hosts plugin parses records first and directives (fallthrough, reload,
// ttl) last:
//
//	hosts [FILE] [ZONES...] {
//	    [INLINE]
//	    fallthrough [ZONES...]
//	    reload DURATION
//	    ttl SECONDS
//	}
//
// A record placed after a directive is read as a directive property and the
// plugin fails to parse, which crash-loops CoreDNS and costs the cluster DNS
// altogether.

const vindCorefile = `.:53 {
    errors
    health {
       lameduck 5s
    }
    hosts /etc/coredns/NodeHosts {
        ttl 60
        reload 15s
        fallthrough
    }
    forward . 1.1.1.1
    cache 30
}
`

// hostsRecordsPrecedeDirectives models the part of the hosts plugin that
// matters: once a directive is seen, anything further is a property, not a
// record. It reports the first line that would be misparsed.
func hostsRecordsPrecedeDirectives(corefile string) (int, string) {
	inBlock := false
	sawDirective := false
	for i, line := range strings.Split(corefile, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inBlock {
			if strings.HasPrefix(trimmed, "hosts ") || trimmed == "hosts {" {
				inBlock = true
			}
			continue
		}
		if trimmed == "}" {
			inBlock = false
			sawDirective = false
			continue
		}
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "fallthrough") || strings.HasPrefix(trimmed, "reload") || strings.HasPrefix(trimmed, "ttl") {
			sawDirective = true
			continue
		}
		// An address line. Before any directive it is a record; after one, the
		// hosts plugin reads it as a directive property and fails to parse.
		if sawDirective {
			return i + 1, trimmed
		}
		if net.ParseIP(strings.Fields(trimmed)[0]) == nil {
			return i + 1, trimmed
		}
	}
	return 0, ""
}

func TestPatchCorefileHostsKeepsRecordsBeforeDirectives(t *testing.T) {
	patched, changed := patchCorefileHosts(vindCorefile, []string{"10.90.0.4 e2espoke.mesh.cilium.io"})
	if !changed {
		t.Fatal("expected the Corefile to change")
	}
	if line, offending := hostsRecordsPrecedeDirectives(patched); line != 0 {
		t.Fatalf("Corefile line %d (%q) is not valid inside the hosts block:\n%s", line, offending, patched)
	}
	if !strings.Contains(patched, "10.90.0.4 e2espoke.mesh.cilium.io") {
		t.Fatalf("entry missing:\n%s", patched)
	}
}

func TestPatchCorefileHostsRepeatedSeedsStayValid(t *testing.T) {
	// Seeding repeatedly is the normal case: every cluster adds a peer on each
	// pass. Records must keep landing above the directives.
	corefile := vindCorefile
	for i := 1; i <= 4; i++ {
		var changed bool
		corefile, changed = patchCorefileHosts(corefile, []string{
			"10.90.0." + string(rune('0'+i)) + " cluster" + string(rune('a'+i)) + ".mesh.cilium.io",
		})
		if !changed {
			t.Fatalf("seed %d made no change", i)
		}
		if line, offending := hostsRecordsPrecedeDirectives(corefile); line != 0 {
			t.Fatalf("after seed %d, line %d (%q) is invalid:\n%s", i, line, offending, corefile)
		}
	}
	if strings.Count(corefile, "hosts /etc/coredns/NodeHosts") != 1 {
		t.Fatalf("a second hosts block was created; CoreDNS allows the plugin once per server block:\n%s", corefile)
	}
}

func TestPatchCorefileHostsIsIdempotentOnSameEntry(t *testing.T) {
	entry := []string{"10.90.0.4 e2espoke.mesh.cilium.io"}
	once, _ := patchCorefileHosts(vindCorefile, entry)
	twice, changed := patchCorefileHosts(once, entry)
	if changed {
		t.Fatalf("re-applying the same entry must be a no-op:\n%s", twice)
	}
	if strings.Count(twice, "10.90.0.4 e2espoke.mesh.cilium.io") != 1 {
		t.Fatalf("entry duplicated:\n%s", twice)
	}
}
