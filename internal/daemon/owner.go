package daemon

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// ownedPrivately reports an error unless dir belongs to this user and is
// reachable by nobody else.
//
// Both halves matter under /tmp. A directory another user got there first
// would let them read the socket; one that is group- or world-accessible would
// let them connect to it.
func ownedPrivately(dir string, info fs.FileInfo) error {
	// ownership needs a platform that reports it; the mode check below does
	// not, so an unfamiliar one still gets that much.
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("refusing to put plbx's socket in %s: it belongs to uid %d, not to you", dir, stat.Uid)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("refusing to put plbx's socket in %s: it is reachable by other users (mode %04o)", dir, perm)
	}
	return nil
}
