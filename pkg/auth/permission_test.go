/* Copyright 2026 AtomAI.
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
 */

package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAddPermission(t *testing.T) {
	AddPermission("TestAddPermission.p1")
	AddPermission("TestAddPermission.p1")
	AddPermission("TestAddPermission.p2")

	count := map[string]int{}
	for _, p := range GetPermissions() {
		count[p]++
	}
	assert.Equal(t, 1, count["TestAddPermission.p1"])
	assert.Equal(t, 1, count["TestAddPermission.p2"])
}

func TestVerifyRoleHasPermission(t *testing.T) {
	_, err := CreateRole("TestVerifyRoleHasPermission", "Verify Role", []string{"index.list"})
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, DeleteRole("TestVerifyRoleHasPermission")) })

	tests := []struct {
		name       string
		roleID     string
		permission string
		want       bool
	}{
		{name: "admin always allowed", roleID: "admin", permission: "anything", want: true},
		{name: "admin case insensitive", roleID: "Admin", permission: "anything", want: true},
		{name: "role has permission", roleID: "TestVerifyRoleHasPermission", permission: "index.list", want: true},
		{name: "role lacks permission", roleID: "testverifyrolehaspermission", permission: "index.delete", want: false},
		{name: "unknown role", roleID: "TestVerifyRoleHasPermission.missing", permission: "index.list", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, VerifyRoleHasPermission(tt.roleID, tt.permission))
		})
	}
}
