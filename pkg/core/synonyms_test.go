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

package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	zedtoken "github.com/vcaesar/zedsearch/pkg/uquery/analysis/token"
)

func TestLookupSynonymSet(t *testing.T) {
	id := "TestLookupSynonymSet.set_1"
	require.NoError(t, metadata.Synonyms.Set(id, meta.SynonymSet{ID: id, Rules: []meta.SynonymRule{
		{ID: "r1", Synonyms: "hello, hi"},
		{ID: "r2", Synonyms: "bye => goodbye"},
	}}))
	t.Cleanup(func() { assert.NoError(t, metadata.Synonyms.Delete(id)) })

	t.Run("hook is installed", func(t *testing.T) {
		require.NotNil(t, zedtoken.SynonymSetLookup)
		rules, err := zedtoken.SynonymSetLookup(id)
		require.NoError(t, err)
		assert.Equal(t, []string{"hello, hi", "bye => goodbye"}, rules)
	})
	t.Run("missing", func(t *testing.T) {
		_, err := LookupSynonymSet("TestLookupSynonymSet.missing")
		assert.Error(t, err)
	})
}
