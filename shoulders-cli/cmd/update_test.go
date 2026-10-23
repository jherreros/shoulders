package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateAsset(t *testing.T) {
	tests := []struct {
		name       string
		goos       string
		goarch     string
		wantAsset  string
		wantBinary string
		wantKind   archiveKind
	}{
		{
			name:       "linux",
			goos:       "linux",
			goarch:     "amd64",
			wantAsset:  "shoulders_0.6.1_linux_amd64.tar.gz",
			wantBinary: "shoulders",
			wantKind:   archiveTarGz,
		},
		{
			name:       "darwin",
			goos:       "darwin",
			goarch:     "arm64",
			wantAsset:  "shoulders_0.6.1_darwin_arm64.tar.gz",
			wantBinary: "shoulders",
			wantKind:   archiveTarGz,
		},
		{
			name:       "windows",
			goos:       "windows",
			goarch:     "amd64",
			wantAsset:  "shoulders_0.6.1_windows_amd64.zip",
			wantBinary: "shoulders.exe",
			wantKind:   archiveZip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asset, binary, kind := updateAsset("0.6.1", tt.goos, tt.goarch)
			if asset != tt.wantAsset {
				t.Fatalf("asset = %q, want %q", asset, tt.wantAsset)
			}
			if binary != tt.wantBinary {
				t.Fatalf("binary = %q, want %q", binary, tt.wantBinary)
			}
			if kind != tt.wantKind {
				t.Fatalf("kind = %v, want %v", kind, tt.wantKind)
			}
		})
	}
}

func TestExtractBinaryFromTarGz(t *testing.T) {
	archive := tarGzArchive(t, "shoulders", []byte("new binary"))
	got, err := extractBinaryFromTarGz(archive, "shoulders")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if string(got) != "new binary" {
		t.Fatalf("binary = %q, want %q", got, "new binary")
	}
}

func TestExtractBinaryFromZip(t *testing.T) {
	archive := zipArchive(t, "shoulders.exe", []byte("windows binary"))
	got, err := extractBinaryFromZip(archive, "shoulders.exe")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if string(got) != "windows binary" {
		t.Fatalf("binary = %q, want %q", got, "windows binary")
	}
}

func TestChecksumForAsset(t *testing.T) {
	data := []byte("release archive")
	sum := sha256.Sum256(data)
	checksums := []byte(hex.EncodeToString(sum[:]) + "  shoulders_0.6.1_linux_amd64.tar.gz\n")

	got, err := checksumForAsset(checksums, "shoulders_0.6.1_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("checksumForAsset: %v", err)
	}
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("checksum = %q, want %q", got, want)
	}
	if _, err := checksumForAsset(checksums, "missing.tar.gz"); err == nil {
		t.Fatal("expected missing checksum to fail")
	}
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("release archive")
	sum := sha256.Sum256(data)
	if err := verifyChecksum("archive.tar.gz", data, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := verifyChecksum("archive.tar.gz", data, "00"); err == nil {
		t.Fatal("expected mismatch to fail")
	}
}

func TestReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "shoulders")
	if err := os.WriteFile(execPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceExecutable(execPath, []byte("new binary")); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new binary" {
		t.Fatalf("executable = %q, want %q", got, "new binary")
	}
	if _, err := os.Stat(execPath + ".new"); !os.IsNotExist(err) {
		t.Fatalf("temporary file still exists: %v", err)
	}
}

func tarGzArchive(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipArchive(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
