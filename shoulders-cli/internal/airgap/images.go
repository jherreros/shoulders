package airgap

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/jherreros/shoulders/shoulders-cli/internal/ocisource"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PauseImage is the sandbox image every kubelet pulls. Harvesting cannot see
// it (no pod references it), so it is always vendored explicitly.
const PauseImage = "registry.k8s.io/pause:3.10"

// HarvestClusterImages returns the sorted, deduplicated image references of
// every pod in the cluster (containers, init containers, ephemeral
// containers). Locally-built user images (localhost/...) are excluded: they
// are user content, not platform closure.
func HarvestClusterImages(kubeconfigPath string) ([]string, error) {
	clientset, err := kube.NewClientset(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	pods, err := clientset.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	seen := map[string]bool{}
	for _, pod := range pods.Items {
		for _, image := range podImages(pod.Spec) {
			image = strings.TrimSpace(image)
			if image == "" || strings.HasPrefix(image, "localhost/") {
				continue
			}
			seen[image] = true
		}
	}
	images := make([]string, 0, len(seen))
	for image := range seen {
		images = append(images, image)
	}
	sort.Strings(images)
	return images, nil
}

func podImages(spec corev1.PodSpec) []string {
	images := []string{}
	for _, container := range spec.Containers {
		images = append(images, container.Image)
	}
	for _, container := range spec.InitContainers {
		images = append(images, container.Image)
	}
	for _, container := range spec.EphemeralContainers {
		images = append(images, container.Image)
	}
	return images
}

// VindNodeImages returns the images backing the vind containers themselves
// (vcluster), which a fresh airgap cluster creation needs.
func VindNodeImages(ctx context.Context, clusterName string) ([]string, error) {
	containers, err := bootstrap.VindContainers(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, container := range containers {
		out, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.Config.Image}}", container).Output()
		if err != nil {
			return nil, fmt.Errorf("inspect vind container %s: %w", container, err)
		}
		image := strings.TrimSpace(string(out))
		if image != "" {
			seen[image] = true
		}
	}
	images := make([]string, 0, len(seen))
	for image := range seen {
		images = append(images, image)
	}
	sort.Strings(images)
	return images, nil
}

// VendorExtras are images no harvest can see but every install needs.
func VendorExtras() []string {
	return []string{PauseImage, ocisource.RegistryImage}
}

// PullImages pulls every image into the host Docker daemon. Digest-pinned
// references are pulled exactly (content match); plain tags pull current.
func PullImages(ctx context.Context, images []string, log func(string)) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker not found in PATH")
	}
	for _, image := range images {
		log(fmt.Sprintf("pulling image %s", image))
		cmd := exec.CommandContext(ctx, "docker", "pull", image)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("pull image %s: %w\n%s", image, err, out)
		}
	}
	return nil
}

// saveRef strips any @digest suffix for use as the archive tag name.
func saveRef(ref string) string {
	if digestIndex := strings.LastIndex(ref, "@"); digestIndex != -1 {
		return ref[:digestIndex]
	}
	return ref
}

// ImageDigest returns the manifest digest of a registry reference without
// touching the Docker daemon.
func ImageDigest(ctx context.Context, ref string) string {
	out, err := exec.CommandContext(ctx, "skopeo", "inspect", "--format", "{{.Digest}}", "docker://"+ref).Output()
	if err != nil {
		return ""
	}
	if digest := strings.TrimSpace(string(out)); digest != "" && digest != "<no value>" {
		return digest
	}
	return ""
}

// SaveImages writes each image into its own docker-save tarball under
// destDir (00-<sanitized>.tar, ...). One tar per image: containerd's
// `ctr images import` chokes on multi-image docker-save archives, and
// per-image files also stream with bounded memory.
// SaveImages is kept for API compatibility and delegates to CopyImages
// with the host architecture.
func SaveImages(ctx context.Context, images []string, destDir string) ([]string, error) {
	return CopyImages(ctx, images, destDir, "", func(string) {})
}

// NodeArch returns the architecture of the cluster nodes (assumed
// homogeneous), used to vendor matching image platforms.
func NodeArch(kubeconfigPath string) (string, error) {
	clientset, err := kube.NewClientset(kubeconfigPath)
	if err != nil {
		return "", err
	}
	nodes, err := clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodes: %w", err)
	}
	if len(nodes.Items) == 0 {
		return "", fmt.Errorf("no nodes found")
	}
	arch := nodes.Items[0].Status.NodeInfo.Architecture
	if arch == "" {
		return "", fmt.Errorf("node architecture unknown")
	}
	return arch, nil
}

// CopyImages copies every image into its own docker-archive tarball under
// destDir (00-<sanitized>.tar, ...), selecting the cluster nodes'
// architecture. Single-manifest archives import cleanly with
// `ctr images import` (multi-image and manifest-list archives do not);
// digest references are re-aliased at install time so @sha256 pod specs
// resolve from the content store.
func CopyImages(ctx context.Context, images []string, destDir, arch string, log func(string)) ([]string, error) {
	if _, err := exec.LookPath("skopeo"); err != nil {
		return nil, fmt.Errorf("skopeo not found in PATH (needed to vendor images: brew install skopeo)")
	}
	args := []string{"copy"}
	if arch != "" {
		args = append(args, "--override-os", "linux", "--override-arch", arch)
	}
	files := make([]string, 0, len(images))
	for i, ref := range images {
		name := fmt.Sprintf("%02d-%s.tar", i, sanitizeImageName(ref))
		dest := name
		if destDir != "" {
			dest = destDir + string(os.PathSeparator) + name
		}
		source := digestOnlyRef(ref)
		log(fmt.Sprintf("copying image %s", ref))
		cmd := exec.CommandContext(ctx, "skopeo", append(args, "docker://"+source, "docker-archive:"+dest+":"+saveRef(ref))...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("copy image %s: %w\n%s", ref, err, out)
		}
		files = append(files, name)
	}
	return files, nil
}

// digestOnlyRef drops any tag when a digest is present: skopeo rejects
// tag+digest combinations, and the digest alone selects exact content.
func digestOnlyRef(ref string) string {
	at := strings.LastIndex(ref, "@")
	if at == -1 {
		return ref
	}
	repo := ref[:at]
	if colon := strings.LastIndex(repo, ":"); colon > strings.LastIndex(repo, "/") {
		repo = repo[:colon]
	}
	return repo + ref[at:]
}

func sanitizeImageName(ref string) string {
	var builder strings.Builder
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	name := strings.Trim(builder.String(), "-.")
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}

// BundleImageTars lists the image archives in an extracted bundle directory
// in index order (%02d-<sanitized>.tar, matching meta.Images order) and
// verifies the count against the bundle metadata.
func BundleImageTars(extractDir string, want int) ([]string, error) {
	pattern := filepath.Join(extractDir, BundleImagesDir, "*.tar")
	tars, err := filepath.Glob(pattern)
	if err != nil || len(tars) == 0 {
		return nil, fmt.Errorf("no image archives found in %s", filepath.Join(extractDir, BundleImagesDir))
	}
	sort.Strings(tars)
	if len(tars) != want {
		return nil, fmt.Errorf("bundle has %d image archives, metadata lists %d images", len(tars), want)
	}
	return tars, nil
}

// RegistryImageTar locates the vendored registry image archive in an
// extracted bundle (matched by image ref suffix).
func RegistryImageTar(extractDir, imageRef string) (string, error) {
	pattern := filepath.Join(extractDir, BundleImagesDir, "*"+sanitizeImageName(imageRef)+".tar")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return "", fmt.Errorf("registry image archive for %s not found in bundle", imageRef)
	}
	sort.Strings(matches)
	return matches[0], nil
}

// ImportSingleTar streams one image archive into every vind node. Reserved
// for bootstrapping (the registry image itself); bulk seeding goes through
// the registry.
func ImportSingleTar(ctx context.Context, clusterName, tarPath string) error {
	return ImportImageTars(ctx, clusterName, []string{tarPath}, func(string) {})
}

// ImportImageTars streams image archives directly into every vind node with
// per-archive retries. Used for the bootstrap set (registry, Cilium,
// provisioner, pause) that must be present before the local OCI registry
// itself can run.
func ImportImageTars(ctx context.Context, clusterName string, tarPaths []string, log func(string)) error {
	if log == nil {
		log = func(string) {}
	}
	containers, err := bootstrap.VindContainers(ctx, clusterName)
	if err != nil {
		return err
	}
	for _, tarPath := range tarPaths {
		var err error
		for attempt := 1; attempt <= 3; attempt++ {
			err = importSingleTarOnce(ctx, containers, tarPath)
			if err == nil {
				break
			}
			log(fmt.Sprintf("import %s attempt %d failed: %v", filepath.Base(tarPath), attempt, err))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 10 * time.Second):
			}
		}
		if err != nil {
			return fmt.Errorf("after 3 attempts: %w", err)
		}
	}
	return nil
}

func importSingleTarOnce(ctx context.Context, containers []string, tarPath string) error {
	for _, container := range containers {
		cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "ctr", "-n", "k8s.io", "images", "import", "-")
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start ctr import in %s: %w", container, err)
		}
		file, err := os.Open(tarPath)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(stdin, file)
		_ = file.Close()
		closeErr := stdin.Close()
		waitErr := cmd.Wait()
		detail := strings.TrimSpace(stderr.String())
		if copyErr != nil {
			return fmt.Errorf("stream %s into %s: %w\n%s", filepath.Base(tarPath), container, copyErr, detail)
		}
		if closeErr != nil {
			return fmt.Errorf("close import stream for %s: %w\n%s", container, closeErr, detail)
		}
		if waitErr != nil {
			return fmt.Errorf("import %s into %s: %w\n%s", filepath.Base(tarPath), container, waitErr, detail)
		}
	}
	return nil
}
