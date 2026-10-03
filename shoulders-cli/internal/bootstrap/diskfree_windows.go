//go:build windows

package bootstrap

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// freeBytes returns the unprivileged free bytes on the filesystem containing
// path.
func freeBytes(path string) (uint64, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	vol := filepath.VolumeName(abs)
	if vol == "" {
		vol, err = os.Getwd()
		if err != nil {
			return 0, err
		}
		vol = filepath.VolumeName(vol)
	}
	var free, total, avail uint64
	if err := windows.GetDiskFreeSpaceEx(
		windows.StringToUTF16Ptr(vol+"\\"),
		&free, &total, &avail,
	); err != nil {
		return 0, err
	}
	return avail, nil
}
