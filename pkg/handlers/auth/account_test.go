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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/auth"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestUpdateAccount(t *testing.T) {
	const id = "testhandlerupdateaccount"
	_, err := auth.CreateUser(id, id, "oldpass1", "admin")
	assert.NoError(t, err)
	defer func() { assert.NoError(t, auth.DeleteUser(id)) }()

	type args struct {
		code   int
		data   interface{}
		result string
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "invalid json",
			args: args{code: http.StatusBadRequest, data: "xxx", result: "error"},
		},
		{
			name: "missing password",
			args: args{
				code:   http.StatusBadRequest,
				data:   map[string]interface{}{"_id": id, "name": "New"},
				result: "error",
			},
		},
		{
			name: "nothing to change",
			args: args{
				code:   http.StatusBadRequest,
				data:   map[string]interface{}{"_id": id, "password": "oldpass1"},
				result: "error",
			},
		},
		{
			name: "wrong password",
			args: args{
				code:   http.StatusUnauthorized,
				data:   map[string]interface{}{"_id": id, "password": "wrong", "name": "New"},
				result: "invalid credentials",
			},
		},
		{
			name: "name only",
			args: args{
				code:   http.StatusOK,
				data:   map[string]interface{}{"_id": id, "password": "oldpass1", "name": "New Name"},
				result: `"name":"New Name"`,
			},
		},
		{
			name: "password and name",
			args: args{
				code:   http.StatusOK,
				data:   map[string]interface{}{"_id": id, "password": "oldpass1", "name": "Final", "new_password": "newpass1"},
				result: `"name":"Final"`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.args.data)
			UpdateAccount(c)
			assert.Equal(t, tt.args.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.args.result)
			assert.NotContains(t, w.Body.String(), `"salt"`)
		})
	}

	user, ok := auth.VerifyCredentials(id, "newpass1")
	assert.True(t, ok)
	assert.Equal(t, "Final", user.Name)
}

func TestUpdateAccount_StorageFailure(t *testing.T) {
	// a cached user whose id ends with "/" has no bolt key, so the update cannot be persisted
	const id = "testupdateaccount_storagefailure.user_1/"
	salt := auth.GenerateSalt()
	auth.ZED_CACHED_USERS.Set(id, &meta.User{ID: id, Name: "Old", Role: "admin", Salt: salt, Password: auth.GeneratePassword("oldpass1", salt)})
	t.Cleanup(func() { auth.ZED_CACHED_USERS.Delete(id) })

	c, w := utils.NewContext()
	utils.SetRequestData(c, map[string]interface{}{"_id": id, "password": "oldpass1", "name": "New", "new_password": "newpass1"})
	UpdateAccount(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "key required")

	user, ok := auth.VerifyCredentials(id, "oldpass1")
	require.True(t, ok, "a failed update keeps the old password")
	assert.Equal(t, "Old", user.Name)
}
