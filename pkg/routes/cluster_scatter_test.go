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
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/cluster"
	"github.com/vcaesar/zedsearch/pkg/cluster/replication"
	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// registerIndex adds a disk index to the in-memory index list.
func registerIndex(t *testing.T, name string) {
	t.Helper()
	index, err := core.NewIndex(name, core.StorageTypeDisk, 1)
	require.NoError(t, err)
	core.ZED_INDEX_LIST.Add(index)
	t.Cleanup(func() { core.ZED_INDEX_LIST.Delete(name) })
}

// withSearchRoutes registers the Elasticsearch search paths on env and the
// indexes the scatter resolves.
func withSearchRoutes(t *testing.T, env *clusterTestEnv) {
	t.Helper()
	registerIndex(t, localIndex)
	registerIndex(t, remoteIndex)
	handler := func(c zhttp.Context) {
		body, err := io.ReadAll(c.Body())
		require.NoError(t, err)
		c.JSON(http.StatusOK, zhttp.H{"served_by": "node-a", "body": string(body)})
	}
	env.engine.POST("/es/:target/_search", handler)
}

func (env *clusterTestEnv) received(t *testing.T) *http.Request {
	t.Helper()
	select {
	case r := <-env.requests:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("node-b never received the request")
		return nil
	}
}

func TestScatterSearchMerges(t *testing.T) {
	env := setupClusterEnv(t)
	withSearchRoutes(t, env)
	payloads := make(chan map[string]interface{}, 1)
	env.respond = func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]interface{}
		data, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.NoError(t, json.Unmarshal(data, &payload))
		payloads <- payload
		w.Header().Set("Content-Type", "application/json")
		_, err = w.Write([]byte(`{"took":3,"hits":{"total":{"value":1},"hits":[{"_index":"` + remoteIndex + `","_id":"doc-1","_score":2},{"_index":"` + remoteIndex + `","_id":"doc-2","_score":1}]}}`))
		assert.NoError(t, err)
	}

	req := httptest.NewRequest(http.MethodPost, "/es/"+remoteIndex+"*/_search", strings.NewReader(`{"from":1,"size":3}`))
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := env.do(req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "doc-2")
	assert.NotContains(t, w.Body.String(), "doc-1", "the merge applies from")
	forwarded := env.received(t)
	assert.Equal(t, InternalSearchPath, forwarded.URL.Path)
	assert.Equal(t, "node-a", forwarded.Header.Get(ForwardedByHeader))
	assert.Equal(t, "Basic dXNlcjpwYXNz", forwarded.Header.Get("Authorization"))
	payload := <-payloads
	assert.Equal(t, []interface{}{remoteIndex}, payload["indexes"])
	query, ok := payload["query"].(map[string]interface{})
	require.True(t, ok)
	assert.EqualValues(t, 4, query["size"], "every owner returns the full window")
}

func TestScatterSearchServesLocally(t *testing.T) {
	env := setupClusterEnv(t)
	withSearchRoutes(t, env)

	for _, target := range []string{localIndex + "*", "no-such-index-*"} {
		t.Run(target, func(t *testing.T) {
			w := env.do(httptest.NewRequest(http.MethodPost, "/es/"+target+"/_search", strings.NewReader(`{"size":1}`)))
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), "node-a")
			assert.Contains(t, w.Body.String(), `\"size\":1`, "the body is handed on")
			assert.Empty(t, env.requests)
		})
	}
}

func TestScatterSearchErrors(t *testing.T) {
	env := setupClusterEnv(t)
	withSearchRoutes(t, env)
	path := "/es/" + remoteIndex + "*/_search"

	t.Run("unreadable body", func(t *testing.T) {
		// gin hands the reader to the handler (400), fiber reads it first (500)
		w := env.do(httptest.NewRequest(http.MethodPost, path, iotest.ErrReader(errors.New("boom"))))
		assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)
		assert.Empty(t, env.requests)
	})

	t.Run("invalid query", func(t *testing.T) {
		w := env.do(httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"size":`)))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Empty(t, env.requests)
	})

	t.Run("owner answers an error", func(t *testing.T) {
		env.respond = func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "index missing", http.StatusInternalServerError)
		}
		w := env.do(httptest.NewRequest(http.MethodPost, path, nil))
		assert.Equal(t, http.StatusBadGateway, w.Code)
		assert.Contains(t, w.Body.String(), "index missing")
		env.received(t)
	})

	t.Run("owner answers garbage", func(t *testing.T) {
		env.respond = func(w http.ResponseWriter, r *http.Request) {
			_, err := w.Write([]byte("not json"))
			assert.NoError(t, err)
		}
		w := env.do(httptest.NewRequest(http.MethodPost, path, nil))
		assert.Equal(t, http.StatusBadGateway, w.Code)
		env.received(t)
	})

	t.Run("owner is unreachable", func(t *testing.T) {
		env.own(t, remoteIndex, "node-gone")
		w := env.do(httptest.NewRequest(http.MethodPost, path, nil))
		assert.Equal(t, http.StatusBadGateway, w.Code)
		assert.Contains(t, w.Body.String(), "node-gone is unreachable")
	})
}

func TestClusterRouteUnassignedIndex(t *testing.T) {
	env := setupClusterEnv(t)
	taken := map[int]bool{
		cluster.PartitionOfN(localIndex, testPartitions):  true,
		cluster.PartitionOfN(remoteIndex, testPartitions): true,
	}
	name := ""
	for i := 0; name == ""; i++ {
		if candidate := fmt.Sprintf("cluster-unassigned-%d", i); !taken[cluster.PartitionOfN(candidate, testPartitions)] {
			name = candidate
		}
	}
	_, _, ok := cluster.Owner(name)
	require.False(t, ok)

	w := env.do(httptest.NewRequest(http.MethodGet, "/api/"+name+"/_doc/1", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "node-a")
}

func TestClusterRouteForwardInvalidAddr(t *testing.T) {
	env := setupClusterEnv(t)
	bad := cluster.NewMembership(env.store, cluster.KeyPrefix(),
		cluster.Node{ID: "node-bad", Addr: "http://bad host"}, time.Minute, time.Second)
	require.NoError(t, bad.Join(context.Background()))
	env.own(t, remoteIndex, "node-bad")

	w := env.do(httptest.NewRequest(http.MethodGet, "/api/"+remoteIndex+"/_doc/1", nil))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "invalid address")
}

func TestClusterRouteReachableOwnerWithReplicas(t *testing.T) {
	env := setupClusterEnv(t)
	env.setReplicas(t, remoteIndex, "node-b")
	stubReplicaReadable(t, true)

	w := env.do(httptest.NewRequest(http.MethodGet, "/api/"+remoteIndex+"/_doc/1", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "node-b")
	env.received(t)
}

// stubReplication replaces the replication steps around writes.
func stubReplication(t *testing.T, prepare, commit error) {
	t.Helper()
	p, c, pa, ca := prepareWrite, commitWrite, prepareAllWrites, commitAllWrites
	t.Cleanup(func() { prepareWrite, commitWrite, prepareAllWrites, commitAllWrites = p, c, pa, ca })
	prepareWrite = func(context.Context, string) error { return prepare }
	commitWrite = func(context.Context, string) error { return commit }
	prepareAllWrites = func(context.Context) error { return prepare }
	commitAllWrites = func(context.Context) error { return commit }
}

// failingWriter accepts headers but fails every body write.
type failingWriter struct {
	*httptest.ResponseRecorder
}

func (w failingWriter) Write([]byte) (int, error) { return 0, errors.New("client went away") }

func TestServeFencedReplication(t *testing.T) {
	env := setupClusterEnv(t)
	env.engine.POST("/api/:target/_bulk", func(c zhttp.Context) {
		c.JSON(http.StatusOK, zhttp.H{"served_by": "node-a"})
	})
	failed := errors.New("replica down")

	tests := []struct {
		name            string
		path            string
		prepare, commit error
		want            int
		written         bool
	}{
		{name: "write is acknowledged", path: "/api/" + localIndex + "/_doc", want: http.StatusOK, written: true},
		{name: "bulk is acknowledged", path: "/api/" + localIndex + "/_bulk", want: http.StatusOK, written: true},
		{name: "prepare fails", path: "/api/" + localIndex + "/_doc", prepare: failed, want: http.StatusServiceUnavailable},
		{name: "bulk prepare fails", path: "/api/" + localIndex + "/_bulk", prepare: failed, want: http.StatusServiceUnavailable},
		{name: "commit fails", path: "/api/" + localIndex + "/_doc", commit: failed, want: http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubReplication(t, tt.prepare, tt.commit)
			w := env.do(httptest.NewRequest(http.MethodPost, tt.path, nil))
			assert.Equal(t, tt.want, w.Code)
			if tt.written {
				assert.Contains(t, w.Body.String(), "node-a")
				return
			}
			assert.Contains(t, w.Body.String(), "write not replicated: replica down")
			assert.NotContains(t, w.Body.String(), "node-a", "the handler output must not leak")
		})
	}

	t.Run("flush fails", func(t *testing.T) {
		stubReplication(t, nil, nil)
		w := failingWriter{httptest.NewRecorder()}
		env.engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/"+localIndex+"/_doc", nil))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Body.String())
	})
}

// startReplication turns replication on for the cluster of env.
func startReplication(t *testing.T) {
	t.Helper()
	previous := config.Global.Cluster
	config.Global.Cluster.Secret = "TestRoutes-secret"
	config.Global.Cluster.Replication = replication.ModeDocument
	config.Global.Cluster.ReplicationInterval = time.Hour
	require.NoError(t, replication.Start(context.Background()))
	require.True(t, replication.Enabled())
	t.Cleanup(func() {
		replication.Stop()
		config.Global.Cluster.Secret = previous.Secret
		config.Global.Cluster.Replication = previous.Replication
		config.Global.Cluster.ReplicationInterval = previous.ReplicationInterval
	})
}

func TestServeUntargetedReplication(t *testing.T) {
	env := setupClusterEnv(t)
	startReplication(t)
	env.engine.POST("/api/_bulk", func(c zhttp.Context) {
		c.JSON(http.StatusOK, zhttp.H{"bulk": true})
	})
	env.engine.POST("/api/_internal/ping", func(c zhttp.Context) {
		c.JSON(http.StatusOK, zhttp.H{"pong": true})
	})
	failed := errors.New("replica down")

	tests := []struct {
		name            string
		path            string
		prepare, commit error
		want            int
		body            string
	}{
		{name: "bulk is acknowledged", path: "/api/_bulk", want: http.StatusOK, body: "bulk"},
		{name: "internal calls are not replicated", path: "/api/_internal/ping", prepare: failed, want: http.StatusOK, body: "pong"},
		{name: "prepare fails", path: "/api/_bulk", prepare: failed, want: http.StatusServiceUnavailable, body: "write not replicated"},
		{name: "commit fails", path: "/api/_bulk", commit: failed, want: http.StatusServiceUnavailable, body: "write not replicated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubReplication(t, tt.prepare, tt.commit)
			w := env.do(httptest.NewRequest(http.MethodPost, tt.path, nil))
			assert.Equal(t, tt.want, w.Code)
			assert.Contains(t, w.Body.String(), tt.body)
		})
	}

	t.Run("flush fails", func(t *testing.T) {
		stubReplication(t, nil, nil)
		w := failingWriter{httptest.NewRecorder()}
		env.engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/_bulk", nil))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Body.String())
	})

	t.Run("replicated disk index", func(t *testing.T) {
		registerIndex(t, "TestServeUntargetedReplication.index_1")
		assert.True(t, replicated("TestServeUntargetedReplication.index_1"))
		assert.False(t, replicated("TestServeUntargetedReplication.missing"))
	})
}

func TestLocalNodeIDWithoutCluster(t *testing.T) {
	require.False(t, cluster.Enabled())
	assert.Empty(t, localNodeID())
}
