package adminaccess

import "testing"

// TestRealtimeInflightRoutesRequireExplicitPermission 锁住两个实时可见性接口的权限登记：
// 只有分组容量读权限能进分组实时名单，只有用户读权限能进用户在途明细；
// 互不继承，避免把「看谁在用」的入口送给只有单一权限的受限管理员。
func TestRealtimeInflightRoutesRequireExplicitPermission(t *testing.T) {
	groupCapacity := Policy{Rules: []Rule{{
		Permissions:      []string{"groups.capacity.read"},
		GroupIDs:         []int64{1},
		AccountTypes:     []string{"oauth", "apikey"},
		AllGroups:        false,
		IncludeUngrouped: false,
	}}}
	userRead := Policy{GlobalPermissions: []string{"users.read"}}

	cases := []struct {
		name   string
		policy Policy
		method string
		path   string
		want   bool
	}{
		// 中间件用 c.FullPath() 判定，路径里保留 Gin 的 :id 占位符。
		{"capacity grants group active-users", groupCapacity, "GET", "/api/v1/admin/groups/:id/active-users", true},
		{"capacity does not grant user inflight", groupCapacity, "GET", "/api/v1/admin/users/:id/inflight", false},
		{"users.read grants user inflight", userRead, "GET", "/api/v1/admin/users/:id/inflight", true},
		{"users.read does not grant group active-users", userRead, "GET", "/api/v1/admin/groups/:id/active-users", false},
		{"empty policy grants nothing", Policy{}, "GET", "/api/v1/admin/groups/:id/active-users", false},
	}
	for _, tc := range cases {
		if got := RouteAllowed(tc.policy, tc.method, tc.path); got != tc.want {
			t.Errorf("%s: RouteAllowed(%s %s) = %v, want %v", tc.name, tc.method, tc.path, got, tc.want)
		}
	}
}

// 两个新接口必须真的出现在权限目录里，而不只是能在 RouteAllowed 里匹配上。
func TestRealtimeInflightRoutesAppearInCatalog(t *testing.T) {
	want := map[string]string{
		"GET /groups/:id/active-users": "groups.capacity.read",
		"GET /users/:id/inflight":      "users.read",
	}
	for route, permissionID := range want {
		found := ""
		for _, definition := range definitions {
			for _, candidate := range definition.Routes {
				if candidate == route {
					found = definition.ID
				}
			}
		}
		if found != permissionID {
			t.Errorf("route %q maps to permission %q, want %q", route, found, permissionID)
		}
	}
}
