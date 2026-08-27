package fsutil

import "github.com/shirou/gopsutil/v4/disk"

// FreeBytes returns the number of free bytes on the filesystem containing path.
// Cross-platform via gopsutil (GetDiskFreeSpaceEx on Windows, statfs on Linux) —
// no build-tag split needed here.
func FreeBytes(path string) (uint64, error) {
	usage, err := disk.Usage(path)
	if err != nil {
		return 0, err
	}
	return usage.Free, nil
}
