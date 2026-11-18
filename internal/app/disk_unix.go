//go:build !windows

package app

import "syscall"

func diskUsage(path string) (freeBytes int64, totalBytes int64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil || st.Bsize <= 0 {
		return 0, 0
	}
	bsize := uint64(st.Bsize)
	free := uint64(st.Bavail) * bsize
	total := uint64(st.Blocks) * bsize
	return int64(free), int64(total)
}
