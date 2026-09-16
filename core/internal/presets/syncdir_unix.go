//go:build unix

package presets

import (
	"os"

	"github.com/matthewlu070111/smart-srun/core/internal/domain"
)

// syncDir makes the rename itself durable.
//
// Syncing the file guarantees its contents; until the directory entry is
// flushed, a power loss can leave the name pointing at the old inode or at
// nothing. A cache that was never flushed is a cache that is not there after
// the reboot it exists for -- and worse than absent, because a half-written one
// is read and fails to parse where an absent one falls straight through to the
// built-in catalogue.
//
// wireless has its own copy of this and they are deliberately not shared. Two
// packages needing the same three system calls is not a reason for one to
// import the other, and the architecture table says which of them is allowed to.
func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return domain.Errorf(domain.CodeInternal,
			"无法打开目录以同步").Wrap(err)
	}
	defer handle.Close()

	if err := handle.Sync(); err != nil {
		return domain.Errorf(domain.CodeInternal, "目录同步失败").Wrap(err)
	}
	return nil
}
