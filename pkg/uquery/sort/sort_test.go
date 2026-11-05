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

package sort

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/search"
)

func asc(field string) *search.Sort  { return search.SortBy(FirstValue(field)) }
func desc(field string) *search.Sort { return search.SortBy(FirstValue(field)).Desc() }

func TestRequest(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    search.SortOrder
		wantErr bool
	}{
		{"nil", nil, nil, false},
		{"string asc", "year", search.SortOrder{asc("year")}, false},
		{"string desc", "-year", search.SortOrder{desc("year")}, false},
		{"string score", "_score", search.SortOrder{search.SortBy(&search.ScoreSource{}).Desc()}, false},
		{"array of strings", []interface{}{"+a", "-b"}, search.SortOrder{asc("a"), desc("b")}, false},
		{"object with string order", []interface{}{map[string]interface{}{"a": "DESC"}}, search.SortOrder{desc("a")}, false},
		{"object with asc string", []interface{}{map[string]interface{}{"a": "asc"}}, search.SortOrder{asc("a")}, false},
		{"object with nested order", []interface{}{map[string]interface{}{"a": map[string]interface{}{"ORDER": "desc", "format": "x"}}},
			search.SortOrder{desc("a")}, false},
		{"object with nested asc", []interface{}{map[string]interface{}{"a": map[string]interface{}{"order": "asc"}}},
			search.SortOrder{asc("a")}, false},
		{"object with unsupported value type defaults asc", []interface{}{map[string]interface{}{"a": 1}}, search.SortOrder{asc("a")}, false},
		{"mixed", []interface{}{"_score", map[string]interface{}{"a": "desc"}},
			search.SortOrder{search.SortBy(&search.ScoreSource{}).Desc(), desc("a")}, false},
		{"non-string/object items skipped", []interface{}{1, "a"}, search.SortOrder{asc("a")}, false},
		{"object with multiple fields", []interface{}{map[string]interface{}{"a": "asc", "b": "desc"}}, nil, true},
		{"unsupported type", 42, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Request(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, got)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("passthrough SortOrder", func(t *testing.T) {
		in := search.SortOrder{asc("a")}
		got, err := Request(in)
		assert.NoError(t, err)
		assert.Equal(t, in, got)
	})
}

// TestFirstValue sorts a real index by FirstValue and by search.Field and
// expects the same hit order and sort keys, missing values last.
func TestFirstValue(t *testing.T) {
	writer, err := riot.OpenWriter(riot.InMemoryOnlyConfig())
	require.NoError(t, err)
	defer func() { assert.NoError(t, writer.Close()) }()
	batch := riot.NewBatch()
	for id, n := range map[string]float64{"a": 3, "b": -1, "c": 20, "d": 7} {
		batch.Update(riot.Identifier(id), riot.NewDocument(id).
			AddField(riot.NewNumericField("n", n).Sortable()).
			AddField(riot.NewKeywordField("k", "k"+id).Sortable()))
	}
	batch.Update(riot.Identifier("e"), riot.NewDocument("e")) // no n, no k
	require.NoError(t, writer.Batch(batch))
	r, err := writer.Reader()
	require.NoError(t, err)
	defer func() { assert.NoError(t, r.Close()) }()

	hits := func(order search.SortOrder) (ids []string, keys [][]byte) {
		dmi, err := r.Search(context.Background(), riot.NewTopNSearch(10, riot.NewMatchAllQuery()).SortByCustom(order))
		require.NoError(t, err)
		for m, err := dmi.Next(); m != nil || err != nil; m, err = dmi.Next() {
			require.NoError(t, err)
			require.NoError(t, m.VisitStoredFields(func(field string, value []byte) bool {
				if field == "_id" {
					ids = append(ids, string(value))
				}
				return true
			}))
			keys = append(keys, m.SortValue[0])
		}
		return ids, keys
	}
	for _, field := range []string{"n", "-n", "k", "-k"} {
		t.Run(field, func(t *testing.T) {
			got, err := Request(field)
			require.NoError(t, err)
			gotIDs, gotKeys := hits(got)
			wantIDs, wantKeys := hits(search.SortOrder{search.ParseSearchSortString(field)})
			assert.Equal(t, wantIDs, gotIDs)
			assert.Equal(t, wantKeys, gotKeys)
			assert.Equal(t, "e", gotIDs[len(gotIDs)-1])
		})
	}
	ids, _ := hits(search.SortOrder{asc("n")})
	assert.Equal(t, []string{"b", "a", "d", "c", "e"}, ids)

	assert.Equal(t, []string{"n"}, FirstValue("n").Fields())
	assert.Equal(t, "n", FirstValue("n").NumericField())
	assert.Nil(t, FirstValue("n").AppendValue(&search.DocumentMatch{}, []byte("x")))
}
