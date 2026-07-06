// Package bootstrap performs the one-time database-level initialization the
// server needs on first boot: generating the instance server UUID, seeding the
// single server_settings row, and creating the initial administrator with a
// random username and high-entropy password. The plaintext admin password is
// returned to the caller for a single console print and is never stored.
package bootstrap

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/mokeyjay/clipbridge/server/internal/security"
	"github.com/mokeyjay/clipbridge/server/internal/store"
)

// AdminCredentials carries freshly generated admin login details for a one-time
// console display. It is returned only when credentials are actually created.
type AdminCredentials struct {
	Username string
	Password string
}

// passwordEntropyBytes and usernameSuffixBytes size the random admin credentials.
const (
	passwordEntropyBytes = 24
	usernameSuffixBytes  = 4
)

// InitializeIdentity seeds the server_settings row (with a new server UUID) if
// absent, and creates the initial admin if none exists. On a fresh database it
// returns the new admin credentials; on subsequent boots it returns nil so the
// caller prints nothing.
func InitializeIdentity(st *store.Store) (*AdminCredentials, error) {
	if err := st.EnsureServerSettings(uuid.NewString()); err != nil {
		return nil, fmt.Errorf("bootstrap: 初始化服务端配置: %w", err)
	}

	count, err := st.CountAdmins()
	if err != nil {
		return nil, fmt.Errorf("bootstrap: 检查管理员: %w", err)
	}
	if count > 0 {
		return nil, nil // already initialized; never re-print credentials
	}
	return createAdmin(st)
}

// createAdmin generates and persists a new administrator, returning its plaintext
// credentials for one-time display.
func createAdmin(st *store.Store) (*AdminCredentials, error) {
	suffix, err := security.RandomToken(usernameSuffixBytes)
	if err != nil {
		return nil, err
	}
	password, err := security.RandomToken(passwordEntropyBytes)
	if err != nil {
		return nil, err
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return nil, err
	}
	username := "admin-" + suffix
	if _, err := st.CreateAdmin(username, hash); err != nil {
		return nil, fmt.Errorf("bootstrap: 创建初始管理员: %w", err)
	}
	return &AdminCredentials{Username: username, Password: password}, nil
}

// ResetAdminPassword sets a new random password for the existing administrator
// and returns the username and new plaintext password for one-time display. It
// is invoked by the offline -reset-admin-password command.
func ResetAdminPassword(st *store.Store) (*AdminCredentials, error) {
	admin, err := st.GetAdmin()
	if err != nil {
		return nil, fmt.Errorf("bootstrap: 读取管理员账号: %w", err)
	}
	password, err := security.RandomToken(passwordEntropyBytes)
	if err != nil {
		return nil, err
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return nil, err
	}
	if err := st.UpdateAdminPassword(admin.ID, hash); err != nil {
		return nil, fmt.Errorf("bootstrap: 重置管理员密码: %w", err)
	}
	return &AdminCredentials{Username: admin.Username, Password: password}, nil
}
