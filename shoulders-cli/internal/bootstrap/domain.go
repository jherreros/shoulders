package bootstrap

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/manifests"
)

type DexTLSMaterial struct {
	CAPEM   string
	CertPEM string
	KeyPEM  string
}

type PublicDomainConfig struct {
	DexHost          string
	GrafanaHost      string
	HeadlampHost     string
	ReporterHost     string
	PrometheusHost   string
	AlertmanagerHost string
	HubbleHost       string
	TLS              DexTLSMaterial

	// HeadlampCallbackURL is the OIDC redirect URI this cluster's Headlamp
	// uses. It is carried whole rather than assembled from a host and a port
	// because the two differ: the hub's gateway is published on the default
	// port, a spoke's on its own.
	HeadlampCallbackURL string

	// HeadlampClientID and HeadlampClientSecret are the Dex client this
	// cluster's Headlamp authenticates with.
	HeadlampClientID     string
	HeadlampClientSecret string

	// ClusterName is the cluster's name in the topology. It becomes the
	// `cluster` external label on every series the cluster reports, which is
	// what makes a hub's and a spoke's metrics distinguishable after they land
	// in the same Prometheus.
	ClusterName string

	// WorkloadDomain is the domain user workloads are served under in this
	// cluster. It is a base name, not a scheme: a WebApplication sets its own
	// host, and this is what it qualifies it with — team-a.<workload domain>.
	//
	// On a spoke it is <cluster>.<hub domain>, so two spokes cannot claim the
	// same workload host. On the hub it is the hub's own domain.
	WorkloadDomain string
}

func DefaultDexTLSMaterial() (DexTLSMaterial, error) {
	caPEM, err := extractIndentedBlock(string(manifests.AuthenticationConfig), "certificateAuthority: |", 8)
	if err != nil {
		return DexTLSMaterial{}, err
	}
	certPEM, err := extractIndentedBlock(string(manifests.DefaultDexTLSSecret), "tls.crt: |", 4)
	if err != nil {
		return DexTLSMaterial{}, err
	}
	keyPEM, err := extractIndentedBlock(string(manifests.DefaultDexTLSSecret), "tls.key: |", 4)
	if err != nil {
		return DexTLSMaterial{}, err
	}
	return DexTLSMaterial{CAPEM: caPEM, CertPEM: certPEM, KeyPEM: keyPEM}, nil
}

func GenerateDexTLSMaterial(dexHost string) (DexTLSMaterial, error) {
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return DexTLSMaterial{}, fmt.Errorf("generate ca key: %w", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "shoulders-dex-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return DexTLSMaterial{}, fmt.Errorf("create ca certificate: %w", err)
	}

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return DexTLSMaterial{}, fmt.Errorf("generate leaf key: %w", err)
	}

	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano() + 1),
		Subject:      pkix.Name{CommonName: "shoulders-dex"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{
			dexHost,
			"dex.dex.svc",
			"dex.dex.svc.cluster.local",
		},
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caTemplate, &leafKey.PublicKey, caKey)
	if err != nil {
		return DexTLSMaterial{}, fmt.Errorf("create leaf certificate: %w", err)
	}

	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}))
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)}))

	return DexTLSMaterial{CAPEM: caPEM, CertPEM: certPEM, KeyPEM: keyPEM}, nil
}

func RenderAuthenticationConfig(dexHost, caPEM string) []byte {
	trimmedCA := strings.TrimSuffix(caPEM, "\n")
	return []byte(fmt.Sprintf(`apiVersion: apiserver.config.k8s.io/v1
kind: AuthenticationConfiguration
jwt:
  - issuer:
      url: https://%s
      discoveryURL: https://dex.dex.svc.cluster.local/.well-known/openid-configuration
      certificateAuthority: |
%s
      audiences:
        - kubernetes
      audienceMatchPolicy: MatchAny
    claimMappings:
      username:
        claim: email
        prefix: ""
      groups:
        claim: groups
        prefix: ""
    userValidationRules:
      - expression: "!user.username.startsWith('system:')"
        message: "username cannot use reserved system: prefix"
`, dexHost, indentBlock(trimmedCA, 8)))
}

func indentBlock(content string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		lines[index] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func extractIndentedBlock(document, marker string, indent int) (string, error) {
	lines := strings.Split(document, "\n")
	prefix := strings.Repeat(" ", indent)
	collecting := false
	var block []string

	for _, line := range lines {
		if !collecting {
			if strings.TrimSpace(line) == marker {
				collecting = true
			}
			continue
		}

		if !strings.HasPrefix(line, prefix) {
			break
		}
		block = append(block, strings.TrimPrefix(line, prefix))
	}

	if len(block) == 0 {
		return "", fmt.Errorf("extract %q block", marker)
	}

	result := strings.Join(block, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result, nil
}
