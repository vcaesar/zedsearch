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

package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func TestIsDocumentWrite(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/api/:target/_doc/:id", want: true},
		{path: "/es/:target/_create/:id", want: true},
		{path: "/es/:target/_update/:id", want: true},
		{path: "/api/:target/_bulkv2", want: true},
		{path: "/api/:target/_multi", want: true},
		{path: "/es/:target/_delete_by_query", want: true},
		{path: "/api/:target/_json", want: true},
		{path: "/api/:target/_hec", want: true},
		{path: "/api/:target/loki/api/v1/push", want: true},
		{path: "/api/:target/v1/logs", want: true},
		{path: "/es/:target/_search", want: false},
		{path: "/api/:target/_mapping", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assert.Equal(t, tt.want, isDocumentWrite(tt.path))
		})
	}
}

// coldIndex stores a cold index record and loads it, which makes it read-only.
func coldIndex(t *testing.T, name string) {
	t.Helper()
	data, err := json.Marshal(meta.Index{
		Name:        name,
		StorageType: core.StorageTypeDisk,
		ShardNum:    1,
		Shards:      map[string]*meta.IndexShard{},
		Settings:    &meta.IndexSettings{Tier: core.TierCold},
	})
	require.NoError(t, err)
	require.NoError(t, metadata.Index.Set(name, data))
	index, err := core.LoadIndex(name)
	require.NoError(t, err)
	require.True(t, index.IsReadOnly())
	t.Cleanup(func() {
		core.ZED_INDEX_LIST.Delete(name)
		assert.NoError(t, metadata.Index.Delete(name))
	})
}

func TestReadOnlyGuard(t *testing.T) {
	const cold = "TestReadOnlyGuard.index_1"
	coldIndex(t, cold)
	assert.True(t, replicaReadable(cold), "a cold index is readable on every replica")

	e := zhttp.New()
	e.Use(ReadOnlyGuard)
	ok := func(c zhttp.Context) { c.Status(http.StatusOK) }
	e.POST("/api/:target/_doc", ok)
	e.GET("/api/:target/_doc/:id", ok)
	e.POST("/api/:target/_search", ok)
	e.POST("/api/_bulk", ok)

	tests := []struct {
		method, path string
		want         int
	}{
		{method: http.MethodPost, path: "/api/" + cold + "/_doc", want: http.StatusForbidden},
		{method: http.MethodPost, path: "/api/TestReadOnlyGuard.hot/_doc", want: http.StatusOK},
		{method: http.MethodGet, path: "/api/" + cold + "/_doc/1", want: http.StatusOK},
		{method: http.MethodPost, path: "/api/" + cold + "/_search", want: http.StatusOK},
		{method: http.MethodPost, path: "/api/_bulk", want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			w := serve(e, httptest.NewRequest(tt.method, tt.path, nil))
			assert.Equal(t, tt.want, w.Code)
			if tt.want == http.StatusForbidden {
				assert.Contains(t, w.Body.String(), core.ErrIndexReadOnly.Error())
			}
		})
	}
}
