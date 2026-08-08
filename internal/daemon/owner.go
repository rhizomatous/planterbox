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
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil // an unfamiliar platform; the mode check below still applies
	}
	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to uid %d, not to you: refusing to put jard's socket in it", dir, stat.Uid)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s is reachable by other users (mode %04o): refusing to put jard's socket in it", dir, perm)
	}
	return nil
}
