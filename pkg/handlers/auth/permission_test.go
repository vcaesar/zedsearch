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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/auth"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestListPermissions(t *testing.T) {
	const permission = "TestListPermissions.permission_1"
	auth.AddPermission(permission)

	c, w := utils.NewContext()
	ListPermissions(c)
	assert.Equal(t, http.StatusOK, w.Code)

	var permissions []string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &permissions))
	assert.Contains(t, permissions, permission)
	assert.Equal(t, auth.GetPermissions(), permissions)
}
