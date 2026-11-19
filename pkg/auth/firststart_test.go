/* Copyright 2022 Zinc Labs Inc. and Contributors
* Copyright 2026 AtomAI
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

	"github.com/vcaesar/zedsearch/pkg/config"
)

func TestInitFirstUser(t *testing.T) {
	original := *config.Global
	t.Cleanup(func() { *config.Global = original })
	t.Setenv("ZED_FIRST_ADMIN_USER", "")
	t.Setenv("ZED_FIRST_ADMIN_PASSWORD", "")
	config.Global.FirstAdminUser = "TestInitFirstUser"
	config.Global.FirstAdminPassword = "test-only-password"

	type args struct {
		init func()
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "init first user",
			args: args{
				init: func() {},
			},
			wantErr: false,
		},
		{
			name: "init first user with error",
			args: args{
				init: func() {
					config.Global.FirstAdminUser = ""
					config.Global.FirstAdminPassword = ""
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.args.init()
			err := initFirstUser()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestIsFirstStart(t *testing.T) {
	_, err := CreateUser("TestIsFirstStart", "First Start", "***", "admin")
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, DeleteUser("testisfirststart")) })
	ZED_CACHED_USERS.Delete("testisfirststart")

	first, err := isFirstStart()
	assert.NoError(t, err)
	assert.False(t, first)
	_, ok := ZED_CACHED_USERS.Get("testisfirststart")
	assert.True(t, ok)
}

func TestInitPermissionCache(t *testing.T) {
	_, err := CreateRole("TestInitPermissionCache", "Cache Role", []string{"p1", "p2"})
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, DeleteRole("testinitpermissioncache")) })
	ZED_CACHED_PERMISSIONS.Delete("testinitpermissioncache")

	assert.NoError(t, initPermissionCache())
	pm, ok := ZED_CACHED_PERMISSIONS.Get("testinitpermissioncache")
	assert.True(t, ok)
	assert.Equal(t, map[string]struct{}{"p1": {}, "p2": {}}, pm)
}
