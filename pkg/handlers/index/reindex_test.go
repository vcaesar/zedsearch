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

package index

import (
	"errors"
	"io"
	"net/http"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/meta/elastic"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestReindex(t *testing.T) {
	sourceName := "TestReindex.index_1"
	destName := "TestReindex.index_2"
	limitedName := "TestReindex.index_3"

	t.Run("prepare", func(t *testing.T) {
		index, err := core.NewIndex(sourceName, "disk", 2)
		assert.NoError(t, err)
		assert.NoError(t, core.StoreIndex(index))
		assert.NoError(t, index.CreateDocument("1", map[string]interface{}{"name": "zed", "num": 1}, false))
		assert.NoError(t, index.CreateDocument("2", map[string]interface{}{"name": "zed", "num": 2}, false))
		assert.NoError(t, index.CreateDocument("3", map[string]interface{}{"name": "other", "num": 3}, false))
		assert.Eventually(t, func() bool {
			res, err := index.Search(&meta.ZedQuery{Size: 10})
			return err == nil && res.Hits.Total.Value == 3
		}, 10*time.Second, 100*time.Millisecond, "documents not indexed")
	})

	t.Run("reindex all documents", func(t *testing.T) {
		c, w := utils.NewContext()
		utils.SetRequestData(c, `{"source":{"index":"`+sourceName+`"},"dest":{"index":"`+destName+`"}}`)
		Reindex(c)
		assert.Equal(t, http.StatusOK, w.Code)

		resp := new(elastic.ReindexResponse)
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
		assert.Equal(t, 3, resp.Total)
		assert.Equal(t, 3, resp.Created)
		assert.Equal(t, 1, resp.Batches)
		assert.Empty(t, resp.Failures)
		assert.Equal(t, -1, resp.RequestsPerSecond)

		dest, exists := core.GetIndex(destName)
		assert.True(t, exists)
		assert.Eventually(t, func() bool {
			res, err := dest.Search(&meta.ZedQuery{Size: 10, Sort: []interface{}{"_id"}})
			return err == nil && res.Hits.Total.Value == 3
		}, 10*time.Second, 100*time.Millisecond)

		// _id and _source are preserved
		res, err := dest.Search(&meta.ZedQuery{Size: 10, Sort: []interface{}{"_id"}})
		assert.NoError(t, err)
		ids := make([]string, 0, len(res.Hits.Hits))
		for _, hit := range res.Hits.Hits {
			ids = append(ids, hit.ID)
		}
		assert.ElementsMatch(t, []string{"1", "2", "3"}, ids)
		source := res.Hits.Hits[0].Source.(map[string]interface{})
		assert.Equal(t, "zed", source["name"])
	})

	t.Run("reindex with query and max_docs", func(t *testing.T) {
		c, w := utils.NewContext()
		utils.SetRequestData(c, `{"source":{"index":"`+sourceName+`","query":{"match":{"name":"zed"}}},"dest":{"index":"`+limitedName+`"},"max_docs":1}`)
		Reindex(c)
		assert.Equal(t, http.StatusOK, w.Code)

		resp := new(elastic.ReindexResponse)
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
		assert.Equal(t, 1, resp.Total)
		assert.Equal(t, 1, resp.Created)
	})

	type args struct {
		code   int
		data   string
		result string
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "body json error",
			args: args{
				code:   http.StatusBadRequest,
				data:   `{"source":{x}}`,
				result: "invalid character",
			},
		},
		{
			name: "missing dest index",
			args: args{
				code:   http.StatusBadRequest,
				data:   `{"source":{"index":"` + sourceName + `"}}`,
				result: "source.index and dest.index are required",
			},
		},
		{
			name: "same source and dest",
			args: args{
				code:   http.StatusBadRequest,
				data:   `{"source":{"index":"` + sourceName + `"},"dest":{"index":"` + sourceName + `"}}`,
				result: "must be different",
			},
		},
		{
			name: "source index not found",
			args: args{
				code:   http.StatusBadRequest,
				data:   `{"source":{"index":"NotExist` + sourceName + `"},"dest":{"index":"` + destName + `"}}`,
				result: "does not exists",
			},
		},
		{
			name: "invalid dest index name",
			args: args{
				code:   http.StatusBadRequest,
				data:   `{"source":{"index":"` + sourceName + `"},"dest":{"index":"_invalid"}}`,
				result: "index name cannot start with _",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.args.data)
			Reindex(c)
			assert.Equal(t, tt.args.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.args.result)
		})
	}

	t.Run("nil body", func(t *testing.T) {
		c, w := utils.NewContext()
		Reindex(c)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "source.index and dest.index are required")
	})

	t.Run("cleanup", func(t *testing.T) {
		assert.NoError(t, core.DeleteIndex(sourceName))
		assert.NoError(t, core.DeleteIndex(destName))
		assert.NoError(t, core.DeleteIndex(limitedName))
	})
}

// A document indexed without @timestamp (zed_auto_timestamp off) is reindexed
// without one, not with the zero time; its own @timestamp is carried over.
func TestReindex_Timestamp(t *testing.T) {
	defer func(v bool) { config.Global.AutoTimestamp = v }(config.Global.AutoTimestamp)
	config.Global.AutoTimestamp = false

	searchOne := func(t *testing.T, index *core.Index) meta.Hit {
		var hit meta.Hit
		require.Eventually(t, func() bool {
			res, err := index.Search(&meta.ZedQuery{Size: 10})
			if err != nil || len(res.Hits.Hits) != 1 {
				return false
			}
			hit = res.Hits.Hits[0]
			return true
		}, 10*time.Second, 100*time.Millisecond, "document not indexed")
		return hit
	}

	own := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name string
		doc  map[string]interface{}
		want time.Time
	}{
		{name: "none", doc: map[string]interface{}{"name": "zed"}},
		{name: "own", doc: map[string]interface{}{"name": "zed", meta.TimeFieldName: own.Format(time.RFC3339)}, want: own},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourceName := "TestReindex_Timestamp.index_" + tt.name + "_1"
			destName := "TestReindex_Timestamp.index_" + tt.name + "_2"
			source, err := core.NewIndex(sourceName, "disk", 1)
			require.NoError(t, err)
			require.NoError(t, core.StoreIndex(source))
			defer func() {
				assert.NoError(t, core.DeleteIndex(sourceName))
				if _, exists := core.GetIndex(destName); exists {
					assert.NoError(t, core.DeleteIndex(destName))
				}
			}()
			require.NoError(t, source.CreateDocument("1", tt.doc, false))
			require.True(t, tt.want.Equal(searchOne(t, source).Timestamp))

			c, w := utils.NewContext()
			utils.SetRequestData(c, `{"source":{"index":"`+sourceName+`"},"dest":{"index":"`+destName+`"}}`)
			Reindex(c)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())

			dest, exists := core.GetIndex(destName)
			require.True(t, exists)
			hit := searchOne(t, dest)
			assert.True(t, tt.want.Equal(hit.Timestamp), "reindexed @timestamp %s", hit.Timestamp)
			if tt.want.IsZero() {
				assert.NotContains(t, hit.Source, meta.TimeFieldName)
			}
		})
	}
}

func TestReindex_Branches(t *testing.T) {
	prefix := "TestReindex_Branches.index_"
	disabled, empty, source, dest := prefix+"disabled", prefix+"empty", prefix+"source", prefix+"dest"
	t.Cleanup(func() {
		for _, name := range []string{disabled, empty, source, dest, prefix + "empty_dest"} {
			if _, exists := core.GetIndex(name); exists {
				assert.NoError(t, core.DeleteIndex(name))
			}
		}
	})

	index, err := core.NewIndex(disabled, "disk", 1)
	require.NoError(t, err)
	mappings := meta.NewMappings()
	mappings.Source = &meta.SourceMapping{Enabled: false}
	require.NoError(t, index.SetMappings(mappings))
	require.NoError(t, core.StoreIndex(index))

	index, err = core.NewIndex(empty, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, core.StoreIndex(index))

	index, err = core.NewIndex(source, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, core.StoreIndex(index))
	require.NoError(t, index.CreateDocument("1", map[string]interface{}{"num": "TestReindex_Branches"}, false))
	require.Eventually(t, func() bool {
		res, err := index.Search(&meta.ZedQuery{Size: 10})
		return err == nil && res.Hits.Total.Value == 1
	}, 10*time.Second, 100*time.Millisecond, "documents not indexed")

	index, err = core.NewIndex(dest, "disk", 1)
	require.NoError(t, err)
	mappings = meta.NewMappings()
	mappings.SetProperty("num", meta.NewProperty("numeric"))
	require.NoError(t, index.SetMappings(mappings))
	require.NoError(t, core.StoreIndex(index))

	t.Run("body read error", func(t *testing.T) {
		c, w := utils.NewContext()
		c.Request.Body = io.NopCloser(iotest.ErrReader(errors.New("TestReindex_Branches read error")))
		Reindex(c)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, `{"error":"TestReindex_Branches read error"}`, w.Body.String())
	})

	tests := []struct {
		name   string
		data   string
		code   int
		result string
	}{
		{name: "source disabled", data: `{"source":{"index":"` + disabled + `"},"dest":{"index":"` + dest + `"}}`, code: http.StatusBadRequest, result: `has _source disabled, it cannot be reindexed`},
		{name: "invalid query", data: `{"source":{"index":"` + empty + `","query":{"TestReindex_Branches":{}}},"dest":{"index":"` + prefix + `empty_dest"}}`, code: http.StatusBadRequest, result: `testreindex_branches`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.data)
			Reindex(c)
			assert.Equal(t, tt.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.result)
		})
	}

	reindex := func(t *testing.T, from, to string) *elastic.ReindexResponse {
		c, w := utils.NewContext()
		utils.SetRequestData(c, `{"source":{"index":"`+from+`"},"dest":{"index":"`+to+`"}}`)
		Reindex(c)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		resp := new(elastic.ReindexResponse)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
		return resp
	}

	t.Run("empty source", func(t *testing.T) {
		resp := reindex(t, empty, prefix+"empty_dest")
		assert.Equal(t, 0, resp.Total)
		assert.Equal(t, 0, resp.Batches)
		assert.Empty(t, resp.Failures)
	})

	t.Run("dest rejects documents", func(t *testing.T) {
		resp := reindex(t, source, dest)
		assert.Equal(t, 1, resp.Total)
		assert.Equal(t, 0, resp.Created)
		assert.Equal(t, []string{"1"}, resp.Failures)
	})
}
