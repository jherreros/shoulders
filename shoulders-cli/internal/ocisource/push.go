package ocisource

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote"
)

// PushArtifact publishes payload (a gzipped tarball of the repo root) as an
// OCI image with a single layer and tags it. registryAddr is host[:port] as
// seen from this process (e.g. 127.0.0.1:5050 via port-forward, or a remote
// mirror host). plainHTTP skips TLS (local throwaway registries).
func PushArtifact(ctx context.Context, registryAddr, repoPath, tag string, payload []byte, plainHTTP bool) error {
	registryAddr = strings.TrimSpace(registryAddr)
	repoPath = strings.Trim(strings.TrimSpace(repoPath), "/")
	tag = strings.TrimSpace(tag)
	if registryAddr == "" || repoPath == "" || tag == "" {
		return fmt.Errorf("registry address, repository path, and tag are all required")
	}
	if len(payload) == 0 {
		return fmt.Errorf("cannot push an empty snapshot payload")
	}

	store := memory.New()
	layerDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(payload),
		Size:      int64(len(payload)),
	}
	if err := store.Push(ctx, layerDesc, bytes.NewReader(payload)); err != nil {
		return fmt.Errorf("stage snapshot layer: %w", err)
	}
	configBytes := []byte("{}")
	configDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeEmptyJSON,
		Digest:    digest.FromBytes(configBytes),
		Size:      int64(len(configBytes)),
	}
	if err := store.Push(ctx, configDesc, bytes.NewReader(configBytes)); err != nil {
		return fmt.Errorf("stage empty config: %w", err)
	}
	manifestDesc, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_0, "", oras.PackManifestOptions{
		ConfigDescriptor: &configDesc,
		Layers:           []ocispec.Descriptor{layerDesc},
		ManifestAnnotations: map[string]string{
			ocispec.AnnotationTitle: "shoulders-addons",
		},
	})
	if err != nil {
		return fmt.Errorf("pack OCI manifest: %w", err)
	}
	if err := store.Tag(ctx, manifestDesc, tag); err != nil {
		return fmt.Errorf("tag OCI manifest: %w", err)
	}

	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", registryAddr, repoPath))
	if err != nil {
		return fmt.Errorf("parse OCI repository reference: %w", err)
	}
	repo.PlainHTTP = plainHTTP
	if _, err := oras.Copy(ctx, store, tag, repo, tag, oras.DefaultCopyOptions); err != nil {
		return fmt.Errorf("push OCI artifact %s/%s:%s: %w", registryAddr, repoPath, tag, err)
	}
	return nil
}

// SplitOCIURL splits an oci://host[:port]/repo/path reference into the
// registry address (host[:port]) and repository path.
func SplitOCIURL(ociURL string) (addr, repoPath string, err error) {
	trimmed := strings.TrimSpace(ociURL)
	if !strings.HasPrefix(trimmed, "oci://") {
		return "", "", fmt.Errorf("OCI URL must start with %q: %q", "oci://", ociURL)
	}
	rest := strings.TrimPrefix(trimmed, "oci://")
	rest = strings.Trim(rest, "/")
	host, path, found := strings.Cut(rest, "/")
	if !found || host == "" || path == "" {
		return "", "", fmt.Errorf("OCI URL must be of the form oci://host/repo/path: %q", ociURL)
	}
	return host, path, nil
}

// FreePort returns an available localhost TCP port for port-forwarding.
func FreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("find free local port: %w", err)
	}
	defer func() {
		_ = listener.Close()
	}()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address type %T", listener.Addr())
	}
	return addr.Port, nil
}
