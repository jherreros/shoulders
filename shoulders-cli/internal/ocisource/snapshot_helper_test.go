package ocisource

import (
	"path/filepath"
	"testing"
	"time"

	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

func testClock() time.Time {
	return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
}

// buildKustomization builds a path the way flux's kustomize-controller does:
// the filesystem is rooted at the artifact root and the path is relative to it.
func buildKustomization(t *testing.T, root, path string) []string {
	t.Helper()
	fs := filesys.MakeFsOnDisk()
	k := krusty.MakeKustomizer(krusty.MakeDefaultOptions())
	res, err := k.Run(fs, filepath.Join(root, path))
	if err != nil {
		t.Fatalf("build %s: %v", path, err)
	}
	var ids []string
	for _, r := range res.Resources() {
		ids = append(ids, r.GetKind()+"/"+r.GetName())
	}
	return ids
}
