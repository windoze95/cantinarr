package auth

import "sort"

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

type Permission string

const (
	PermissionAdmin             Permission = "admin:*"
	PermissionMediaDiscover     Permission = "media:discover"
	PermissionMediaRequest      Permission = "media:request"
	PermissionAIChat            Permission = "ai:chat"
	PermissionMCPAccess         Permission = "mcp:access"
	PermissionUsersManage       Permission = "users:manage"
	PermissionRequestsManage    Permission = "requests:manage"
	PermissionCredentialsManage Permission = "credentials:manage"
	PermissionAIToolsManage     Permission = "ai_tools:manage"
	PermissionInstancesManage   Permission = "instances:manage"
	PermissionArrRead           Permission = "arr:read"
	PermissionArrSearch         Permission = "arr:search"
	PermissionDownloadsRead     Permission = "downloads:read"
	PermissionDownloadsManage   Permission = "downloads:manage"
	PermissionMonitoringRead    Permission = "monitoring:read"
	PermissionSystemRead        Permission = "system:read"
)

var rolePermissions = map[string]map[Permission]bool{
	RoleAdmin: {
		PermissionAdmin: true,
	},
	RoleUser: {
		PermissionMediaDiscover: true,
		PermissionMediaRequest:  true,
		PermissionAIChat:        true,
		PermissionMCPAccess:     true,
	},
}

// HasPermission answers whether a role is allowed to perform an action.
// Empty permissions are allowed so legacy call sites can opt in incrementally.
func HasPermission(role string, permission Permission) bool {
	if permission == "" {
		return true
	}
	perms := rolePermissions[role]
	if perms == nil {
		return false
	}
	return perms[PermissionAdmin] || perms[permission]
}

func PermissionsForRole(role string) []Permission {
	perms := rolePermissions[role]
	if perms == nil {
		return []Permission{}
	}
	out := make([]Permission, 0, len(perms))
	if perms[PermissionAdmin] {
		for permission := range allPermissions() {
			out = append(out, permission)
		}
	} else {
		for permission := range perms {
			out = append(out, permission)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func allPermissions() map[Permission]bool {
	return map[Permission]bool{
		PermissionAdmin:             true,
		PermissionMediaDiscover:     true,
		PermissionMediaRequest:      true,
		PermissionAIChat:            true,
		PermissionMCPAccess:         true,
		PermissionUsersManage:       true,
		PermissionRequestsManage:    true,
		PermissionCredentialsManage: true,
		PermissionAIToolsManage:     true,
		PermissionInstancesManage:   true,
		PermissionArrRead:           true,
		PermissionArrSearch:         true,
		PermissionDownloadsRead:     true,
		PermissionDownloadsManage:   true,
		PermissionMonitoringRead:    true,
		PermissionSystemRead:        true,
	}
}
