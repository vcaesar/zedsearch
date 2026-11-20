/* Copyright 2026 AtomAI
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

package metadata

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
)

func TestQueryRules(t *testing.T) {
	// fixture stores a ruleset owned by the calling subtest, so each runs alone.
	fixture := func(t *testing.T, id string) meta.QueryRuleset {
		t.Helper()
		rs := meta.QueryRuleset{ID: id, Rules: []*meta.QueryRule{{ID: "r1", Type: "pinned",
			Criteria: []meta.QueryRuleCriteria{{Type: "exact", Metadata: "q", Values: []interface{}{"pugs"}}},
			Actions:  meta.QueryRuleActions{IDs: []string{"1"}}}}}
		require.NoError(t, QueryRules.Set(id, rs))
		t.Cleanup(func() { assert.NoError(t, QueryRules.Delete(id)) })
		return rs
	}

	t.Run("set", func(t *testing.T) {
		rs := fixture(t, "TestQueryRules.rs_set")
		rs.Rules = append(rs.Rules, &meta.QueryRule{ID: "r2", Type: "exclude",
			Criteria: []meta.QueryRuleCriteria{{Type: "always"}}, Actions: meta.QueryRuleActions{IDs: []string{"2"}}})
		require.NoError(t, QueryRules.Set(rs.ID, rs))
		got, err := QueryRules.Get(rs.ID)
		require.NoError(t, err)
		assert.Equal(t, rs, *got)
	})
	t.Run("get", func(t *testing.T) {
		rs := fixture(t, "TestQueryRules.rs_get")
		got, err := QueryRules.Get(rs.ID)
		require.NoError(t, err)
		assert.Equal(t, rs, *got)
	})
	t.Run("list", func(t *testing.T) {
		rs := fixture(t, "TestQueryRules.rs_list")
		got, err := QueryRules.List(0, 0)
		require.NoError(t, err)
		found := false
		for _, r := range got {
			if r.ID == rs.ID {
				found = true
				assert.Equal(t, rs.Rules, r.Rules)
			}
		}
		assert.True(t, found)
	})
	t.Run("delete", func(t *testing.T) {
		rs := fixture(t, "TestQueryRules.rs_delete")
		require.NoError(t, QueryRules.Delete(rs.ID))
		_, err := QueryRules.Get(rs.ID)
		assert.ErrorIs(t, err, errors.ErrKeyNotFound)
	})
	t.Run("get invalid json", func(t *testing.T) {
		key := "TestQueryRules.invalid"
		require.NoError(t, db.Set(QueryRules.key(key), []byte("{")))
		t.Cleanup(func() { assert.NoError(t, QueryRules.Delete(key)) })
		_, err := QueryRules.Get(key)
		assert.Error(t, err)
		_, err = QueryRules.List(0, 0)
		assert.Error(t, err)
	})
	t.Run("set unsupported value", func(t *testing.T) {
		key := "TestQueryRules.unsupported"
		rs := meta.QueryRuleset{ID: key, Rules: []*meta.QueryRule{{ID: "r1",
			Criteria: []meta.QueryRuleCriteria{{Type: "exact", Values: []interface{}{make(chan int)}}}}}}
		assert.Error(t, QueryRules.Set(key, rs))
		_, err := QueryRules.Get(key)
		assert.ErrorIs(t, err, errors.ErrKeyNotFound, "nothing stored")
	})
}

func TestQueryRulesStorageError(t *testing.T) {
	m := useMock(t)
	m.err = errMock

	_, err := QueryRules.List(0, 0)
	assert.ErrorIs(t, err, errMock)
	_, err = QueryRules.Get("TestQueryRulesStorageError.rs")
	assert.ErrorIs(t, err, errMock)
	assert.ErrorIs(t, QueryRules.Set("TestQueryRulesStorageError.rs", meta.QueryRuleset{}), errMock)
	assert.ErrorIs(t, QueryRules.Delete("TestQueryRulesStorageError.rs"), errMock)
}
