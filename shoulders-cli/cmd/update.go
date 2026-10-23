package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	pathpkg "path"
	"runtime"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

const (
	githubOwner = "jherreros"
	githubRepo  = "shoulders"
)

type archiveKind int

const (
	archiveTarGz archiveKind = iota
	archiveZip
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Check for a new CLI version and update if available",
	RunE: func(cmd *cobra.Command, args []string) error {
		current := Version
		if current == "dev" {
			return fmt.Errorf("cannot update a dev build; install a released version first")
		}

		spinner, _ := pterm.DefaultSpinner.Start("Checking for updates...")
		tag, err := fetchLatestReleaseTag(cmd.Context())
		if err != nil {
			spinner.Fail("Failed to check for updates")
			return fmt.Errorf("failed to fetch latest release: %w", err)
		}

		latest := strings.TrimPrefix(tag, "v")
		current = strings.TrimPrefix(current, "v")

		if latest == current {
			spinner.Success(fmt.Sprintf("Already up to date (%s)", tag))
			return nil
		}

		spinner.UpdateText(fmt.Sprintf("New version available: %s → %s", current, latest))

		assetName, binaryName, kind := updateAsset(latest, runtime.GOOS, runtime.GOARCH)
		downloadBase := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s", githubOwner, githubRepo, tag)

		spinner.UpdateText(fmt.Sprintf("Downloading checksums for %s...", tag))
		checksums, err := downloadAsset(cmd.Context(), downloadBase+"/checksums.txt")
		if err != nil {
			spinner.Fail("Checksum download failed")
			return fmt.Errorf("failed to download checksums: %w", err)
		}
		expected, err := checksumForAsset(checksums, assetName)
		if err != nil {
			spinner.Fail("Checksum missing")
			return err
		}

		spinner.UpdateText(fmt.Sprintf("Downloading %s...", tag))
		archive, err := downloadAsset(cmd.Context(), downloadBase+"/"+assetName)
		if err != nil {
			spinner.Fail("Download failed")
			return fmt.Errorf("failed to download update: %w", err)
		}
		if err := verifyChecksum(assetName, archive, expected); err != nil {
			spinner.Fail("Checksum verification failed")
			return err
		}

		binary, err := extractBinary(archive, binaryName, kind)
		if err != nil {
			spinner.Fail("Extraction failed")
			return fmt.Errorf("failed to extract binary from archive: %w", err)
		}

		execPath, err := os.Executable()
		if err != nil {
			spinner.Fail("Cannot determine executable path")
			return err
		}

		if err := replaceExecutable(execPath, binary); err != nil {
			spinner.Fail("Update failed")
			return fmt.Errorf("failed to replace binary: %w", err)
		}

		spinner.Success(fmt.Sprintf("Updated to %s", tag))
		return nil
	},
}

// updateAsset returns the release archive, the executable name inside it, and
// the archive format. GoReleaser publishes .tar.gz everywhere except Windows,
// where it publishes a .zip containing shoulders.exe.
func updateAsset(version, goos, goarch string) (string, string, archiveKind) {
	if goos == "windows" {
		return fmt.Sprintf("shoulders_%s_%s_%s.zip", version, goos, goarch), "shoulders.exe", archiveZip
	}
	return fmt.Sprintf("shoulders_%s_%s_%s.tar.gz", version, goos, goarch), "shoulders", archiveTarGz
}

// fetchLatestReleaseTag follows GitHub's /releases/latest redirect instead of
// calling the unauthenticated API. This avoids the 60 requests/hour per-IP
// limit that makes both update and install fail on shared networks.
func fetchLatestReleaseTag(ctx context.Context) (string, error) {
	latestURL := fmt.Sprintf("https://github.com/%s/%s/releases/latest", githubOwner, githubRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, latestURL, nil)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest release request returned %s", resp.Status)
	}

	tag := pathpkg.Base(resp.Request.URL.Path)
	if !strings.HasPrefix(tag, "v") || tag == "latest" {
		return "", fmt.Errorf("unexpected latest release URL %q", resp.Request.URL.String())
	}
	return tag, nil
}

func downloadAsset(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func checksumForAsset(checksums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum found for %q", name)
}

func verifyChecksum(name string, data []byte, expected string) error {
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	if !strings.EqualFold(strings.TrimSpace(expected), actual) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", name, expected, actual)
	}
	return nil
}

func replaceExecutable(execPath string, newBinary []byte) error {
	tmpPath := execPath + ".new"
	if err := os.WriteFile(tmpPath, newBinary, 0o755); err != nil {
		return err
	}

	// Windows cannot replace a running executable in one rename. Move the old
	// binary aside first, then install the new one. The .old file is removed
	// on a best-effort basis because the running image may still be locked.
	if runtime.GOOS == "windows" {
		oldPath := execPath + ".old"
		_ = os.Remove(oldPath)
		if err := os.Rename(execPath, oldPath); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("cannot move current executable aside: %w", err)
		}
		if err := os.Rename(tmpPath, execPath); err != nil {
			_ = os.Rename(oldPath, execPath)
			_ = os.Remove(tmpPath)
			return err
		}
		_ = os.Remove(oldPath)
		return nil
	}

	if err := os.Rename(tmpPath, execPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func extractBinary(archive []byte, binaryName string, kind archiveKind) ([]byte, error) {
	switch kind {
	case archiveZip:
		return extractBinaryFromZip(archive, binaryName)
	default:
		return extractBinaryFromTarGz(archive, binaryName)
	}
}

// extractBinaryFromTarGz finds a file named binaryName inside a gzipped tar archive.
func extractBinaryFromTarGz(archive []byte, binaryName string) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("gzip open: %w", err)
	}
	defer func() { _ = gr.Close() }()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar read: %w", err)
		}
		if hdr.Name == binaryName || strings.HasSuffix(hdr.Name, "/"+binaryName) {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive", binaryName)
}

func extractBinaryFromZip(archive []byte, binaryName string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("zip open: %w", err)
	}
	for _, file := range zr.File {
		if file.Name == binaryName || strings.HasSuffix(file.Name, "/"+binaryName) {
			rc, err := file.Open()
			if err != nil {
				return nil, fmt.Errorf("zip open %q: %w", file.Name, err)
			}
			defer func() { _ = rc.Close() }()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive", binaryName)
}
