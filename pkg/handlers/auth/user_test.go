/* Copyright 2022 Zinc Labs Inc. and Contributors
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
	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestCreateUpdateUser(t *testing.T) {
	type args struct {
		code   int
		data   map[string]interface{}
		result string
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "normal",
			args: args{
				code: http.StatusOK,
				data: map[string]interface{}{
					"_id":      "user",
					"name":     "user",
					"role":     "admin",
					"password": "password",
				},
				result: "message",
			},
		},
		{
			name: "error",
			args: args{
				code: http.StatusBadRequest,
				data: map[string]interface{}{
					"_id": "",
				},
				result: "error",
			},
		},
		{
			name: "empty",
			args: args{
				code: http.StatusBadRequest,
				data: map[string]interface{}{
					"_id": 123,
				},
				result: "error",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.args.data)
			CreateUpdateUser(c)
			assert.Equal(t, tt.args.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.args.result)
		})
	}
}

func TestListUser(t *testing.T) {
	type args struct {
		code   int
		result string
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "normal",
			args: args{
				code:   http.StatusOK,
				result: `[{"_id":`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			ListUser(c)
			assert.Equal(t, tt.args.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.args.result)
		})
	}
}

func TestDeleteUser(t *testing.T) {
	type args struct {
		code   int
		data   map[string]string
		result string
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "normal",
			args: args{
				code:   http.StatusOK,
				data:   map[string]string{"id": "1"},
				result: "message",
			},
		},
		{
			name: "empty",
			args: args{
				code:   http.StatusOK,
				data:   map[string]string{"id": ""},
				result: "message",
			},
		},
		{
			name: "nil",
			args: args{
				code:   http.StatusOK,
				data:   nil,
				result: "message",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestParams(c, tt.args.data)
			DeleteUser(c)
			assert.Equal(t, tt.args.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.args.result)
		})
	}
}

func TestCreateUpdateUser_StorageFailure(t *testing.T) {
	// bolt keys an id ending with "/" by an empty name, which it refuses
	const id = "testcreateupdateuser_storagefailure.user_1/"
	c, w := utils.NewContext()
	utils.SetRequestData(c, map[string]interface{}{"_id": id, "name": "user", "role": "admin", "password": "pass1"})
	CreateUpdateUser(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "key required")

	_, exists, err := auth.GetUser(id)
	assert.ErrorIs(t, err, errors.ErrKeyNotFound)
	assert.False(t, exists)
	_, cached := auth.ZED_CACHED_USERS.Get(id)
	assert.False(t, cached)
}

func TestListUser_HidesSecrets(t *testing.T) {
	const id = "testlistuser_hidessecrets.user_1"
	_, err := auth.CreateUser(id, "user", "pass1", "admin")
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, auth.DeleteUser(id)) })

	c, w := utils.NewContext()
	ListUser(c)
	assert.Equal(t, http.StatusOK, w.Code)

	var users []meta.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &users))
	var found *meta.User
	for i := range users {
		assert.Empty(t, users[i].Salt, users[i].ID)
		assert.Empty(t, users[i].Password, users[i].ID)
		if users[i].ID == id {
			found = &users[i]
		}
	}
	require.NotNil(t, found)
	assert.Equal(t, "admin", found.Role)
	assert.NotContains(t, w.Body.String(), `"salt"`)
	assert.NotContains(t, w.Body.String(), `"password"`)
}
