//go:build !windows

package credstore

// tightenWindowsACL is a no-op on non-Windows platforms; Unix file modes (handled
// in RepairPermissions) already restrict the config tree to the owner.
func (s *Store) tightenWindowsACL() error { return nil }
