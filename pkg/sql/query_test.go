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

package sql

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta"
)

// prepareIndex creates an index with the fixture mappings and documents, and
// deletes it when the test ends.
func prepareIndex(t *testing.T, name string, docs []map[string]interface{}) {
	t.Helper()

	index, err := core.NewIndex(name, "disk", 2)
	require.NoError(t, err)
	require.NoError(t, core.StoreIndex(index))
	t.Cleanup(func() {
		assert.NoError(t, core.DeleteIndex(name))
	})

	mappings := meta.NewMappings()
	mappings.SetProperty("level", meta.NewProperty("keyword"))
	mappings.SetProperty("message", meta.NewProperty("text"))
	mappings.SetProperty("count", meta.NewProperty("numeric"))
	require.NoError(t, index.SetMappings(mappings))

	for i, doc := range docs {
		require.NoError(t, index.CreateDocument(strconv.Itoa(i), doc, false))
	}
	require.Eventually(t, func() bool {
		resp, err := index.Search(&meta.ZedQuery{Size: 100})
		return err == nil && resp.Hits.Total.Value == len(docs)
	}, 30*time.Second, 50*time.Millisecond, "documents not indexed")
}

func TestQuery(t *testing.T) {
	first := "TestQuery.index_1"
	second := "TestQuery.index_2"
	prepareIndex(t, first, []map[string]interface{}{
		{"level": "error", "message": "disk failure on node one", "count": 3},
		{"level": "error", "message": "disk failure on node two", "count": 5},
		{"level": "warn", "message": "memory pressure", "count": 1},
	})
	prepareIndex(t, second, []map[string]interface{}{
		{"level": "info", "message": "startup complete", "count": 10},
	})

	t.Run("hits of one index", func(t *testing.T) {
		resp, err := Query(`SELECT level, count AS c FROM "`+first+`" WHERE level = 'error' ORDER BY count DESC`, 0)
		require.NoError(t, err)
		assert.Equal(t, []ResultColumn{{Name: "level", Type: "keyword"}, {Name: "c", Type: "double"}}, resp.Columns)
		require.Len(t, resp.Rows, 2)
		assert.Equal(t, "error", resp.Rows[0][0])
		assert.EqualValues(t, 5, resp.Rows[0][1])
		assert.EqualValues(t, 3, resp.Rows[1][1])
	})

	t.Run("fetch size caps the rows", func(t *testing.T) {
		resp, err := Query(`SELECT level FROM "`+first+`"`, 1)
		require.NoError(t, err)
		assert.Len(t, resp.Rows, 1)
	})

	t.Run("aggregation of one index", func(t *testing.T) {
		resp, err := Query(`SELECT level, COUNT(*) FROM "`+first+`" GROUP BY level ORDER BY level`, 0)
		require.NoError(t, err)
		assert.Equal(t, [][]interface{}{{"error", int64(2)}, {"warn", int64(1)}}, resp.Rows)
	})

	t.Run("index pattern searches every match", func(t *testing.T) {
		resp, err := Query(`SELECT COUNT(*) FROM "TestQuery.index_*"`, 0)
		require.NoError(t, err)
		assert.Equal(t, [][]interface{}{{int64(4)}}, resp.Rows)
	})

	errs := []struct {
		name string
		sql  string
		want string
	}{
		{name: "parse error", sql: `SELECT FROM`, want: "syntax error"},
		{name: "unsupported where", sql: `SELECT * FROM "` + first + `" WHERE count + 1 = 2`, want: "unsupported WHERE expression"},
		{name: "missing index", sql: `SELECT * FROM "TestQuery.missing"`, want: "index TestQuery.missing does not exists"},
	}
	for _, tt := range errs {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := Query(tt.sql, 0)
			require.Error(t, err)
			assert.Nil(t, resp)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestTranslate(t *testing.T) {
	indexName := "TestTranslate.index_1"
	prepareIndex(t, indexName, nil)

	t.Run("uses the index mappings", func(t *testing.T) {
		dsl, err := Translate(`SELECT level FROM "`+indexName+`" WHERE message = 'disk' AND level = 'error'`, 10)
		require.NoError(t, err)
		assertDSL(t, `{"query":{"bool":{"must":[
			{"match":{"message":{"query":"disk"}}},
			{"term":{"level":{"value":"error"}}}]}},
			"track_total_hits":true,"from":0,"size":10,"_source":["level"]}`, dsl)
	})

	t.Run("parse error", func(t *testing.T) {
		dsl, err := Translate(`DELETE FROM "`+indexName+`"`, 0)
		require.Error(t, err)
		assert.Nil(t, dsl)
		assert.Contains(t, err.Error(), "only SELECT statements are supported")
	})
}

func TestBuildResponse(t *testing.T) {
	hits := &meta.SearchResponse{Hits: meta.Hits{Hits: []meta.Hit{
		{Source: map[string]interface{}{"level": "error", "count": 3.0}},
		{Source: map[string]interface{}{"level": "warn", "ok": true}},
		{Source: "not a document"},
	}}}

	t.Run("columns", func(t *testing.T) {
		stmt, err := Parse(`SELECT level AS l, count FROM logs`)
		require.NoError(t, err)
		got, err := buildResponse(stmt, testFields, hits, 0)
		require.NoError(t, err)
		assert.Equal(t, []ResultColumn{{Name: "l", Type: "keyword"}, {Name: "count", Type: "double"}}, got.Columns)
		assert.Equal(t, [][]interface{}{{"error", 3.0}, {"warn", nil}, {nil, nil}}, got.Rows)
	})

	t.Run("star lists every source field", func(t *testing.T) {
		stmt, err := Parse(`SELECT * FROM logs`)
		require.NoError(t, err)
		got, err := buildResponse(stmt, testFields, hits, 2)
		require.NoError(t, err)
		assert.Equal(t, []ResultColumn{
			{Name: "count", Type: "double"},
			{Name: "level", Type: "keyword"},
			{Name: "ok", Type: "boolean"},
		}, got.Columns)
		assert.Equal(t, [][]interface{}{{3.0, "error", nil}, {nil, "warn", true}}, got.Rows)
	})

	t.Run("aggregates without group by", func(t *testing.T) {
		stmt, err := Parse(`SELECT COUNT(*), AVG(price), COUNT(count), SUM(count) FROM logs`)
		require.NoError(t, err)
		resp := &meta.SearchResponse{
			Hits: meta.Hits{Total: meta.Total{Value: 7}},
			Aggregations: map[string]meta.AggregationResponse{
				"AVG(price)":   {Value: 2.5},
				"COUNT(count)": {Value: 6.0},
			},
		}
		got, err := buildResponse(stmt, testFields, resp, 0)
		require.NoError(t, err)
		assert.Equal(t, [][]interface{}{{int64(7), 2.5, int64(6), nil}}, got.Rows)
	})

	t.Run("group by with metric", func(t *testing.T) {
		stmt, err := Parse(`SELECT level, COUNT(*), AVG(price) FROM logs GROUP BY level`)
		require.NoError(t, err)
		resp := &meta.SearchResponse{Aggregations: map[string]meta.AggregationResponse{
			GroupAggName: {Buckets: []map[string]interface{}{
				{"key": "error", "doc_count": uint64(2), "AVG(price)": meta.AggregationResponse{Value: 1.5}},
				{"key": "warn", "doc_count": uint64(1)},
			}},
		}}
		got, err := buildResponse(stmt, testFields, resp, 0)
		require.NoError(t, err)
		assert.Equal(t, [][]interface{}{{"error", int64(2), 1.5}, {"warn", int64(1), nil}}, got.Rows)
	})

	t.Run("group by without buckets", func(t *testing.T) {
		stmt, err := Parse(`SELECT level, COUNT(*) FROM logs GROUP BY level`)
		require.NoError(t, err)
		got, err := buildResponse(stmt, testFields, &meta.SearchResponse{}, 0)
		require.NoError(t, err)
		assert.Empty(t, got.Rows)
	})

	t.Run("unexpected buckets", func(t *testing.T) {
		stmt, err := Parse(`SELECT level, COUNT(*) FROM logs GROUP BY level`)
		require.NoError(t, err)
		resp := &meta.SearchResponse{Aggregations: map[string]meta.AggregationResponse{
			GroupAggName: {Buckets: map[string]interface{}{"error": 2}},
		}}
		got, err := buildResponse(stmt, testFields, resp, 0)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "unexpected [groupby] aggregation result")
	})
}

func TestCompareValues(t *testing.T) {
	tests := []struct {
		name string
		a, b interface{}
		want int
	}{
		{name: "float32 below int", a: float32(1.5), b: 2, want: -1},
		{name: "int above float64", a: 2, b: 1.0, want: 1},
		{name: "uint64 equals int64", a: uint64(1), b: int64(1), want: 0},
		{name: "strings", a: "a", b: "b", want: -1},
		{name: "number against string compares text", a: 10, b: "9", want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, compareValues(tt.a, tt.b))
		})
	}
}
