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
	"sync"
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

	req := httptest.NewRequest(http.MethodPost, "/es/"+remoteIndex+"*/_search",
		strings.NewReader(`{"from":1,"size":3,"aggs":{"by":{"terms":{"field":"f","size":10}}}}`))
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
	terms := query["aggs"].(map[string]interface{})["by"].(map[string]interface{})["terms"].(map[string]interface{})
	assert.EqualValues(t, 25, terms["size"], "every owner returns more terms than asked for")
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

// A scattered search whose owner is gone reaches a live replica instead of
// failing, like a search of that single index does.
func TestScatterSearchFallsBackToReplica(t *testing.T) {
	env := setupClusterEnv(t)
	withSearchRoutes(t, env)
	env.own(t, remoteIndex, "node-gone")
	env.respond = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"hits":{"total":{"value":1},"hits":[{"_index":"` + remoteIndex + `","_id":"doc-1","_score":1}]}}`))
		assert.NoError(t, err)
	}
	path := "/es/" + remoteIndex + "*/_search"

	t.Run("cold index", func(t *testing.T) {
		env.setReplicas(t, remoteIndex, "node-gone-too", "node-b")
		stubReplicaReadable(t, true)
		groups, replicas := groupByOwner([]string{localIndex, remoteIndex})
		assert.Equal(t, map[string][]string{"node-a": {localIndex}}, groups)
		assert.Equal(t, map[string][]string{remoteIndex: {"node-b"}}, replicas)

		w := env.do(httptest.NewRequest(http.MethodPost, path, nil))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "doc-1")
		assert.Equal(t, InternalSearchPath, env.received(t).URL.Path)
	})

	t.Run("no live replica", func(t *testing.T) {
		env.setReplicas(t, remoteIndex, "node-gone-too")
		stubReplicaReadable(t, true)
		w := env.do(httptest.NewRequest(http.MethodPost, path, nil))
		assert.Equal(t, http.StatusBadGateway, w.Code)
		assert.Contains(t, w.Body.String(), "node-gone is unreachable")
	})

	t.Run("index without copies", func(t *testing.T) {
		env.setReplicas(t, remoteIndex, "node-b")
		stubReplicaReadable(t, false)
		groups, replicas := groupByOwner([]string{remoteIndex})
		assert.Equal(t, map[string][]string{"node-gone": {remoteIndex}}, groups,
			"a disk index is only on its owner while replication is off")
		assert.Empty(t, replicas)
	})

	t.Run("replicated disk index", func(t *testing.T) {
		startReplication(t)
		// this node is a replica too, but its copy is not installed
		env.setReplicas(t, remoteIndex, "node-a", "node-b")
		groups, replicas := groupByOwner([]string{remoteIndex})
		assert.Empty(t, groups)
		assert.Equal(t, map[string][]string{remoteIndex: {"node-b"}}, replicas)

		env.setReplicas(t, remoteIndex, "node-a")
		groups, replicas = groupByOwner([]string{remoteIndex})
		assert.Equal(t, map[string][]string{"node-gone": {remoteIndex}}, groups)
		assert.Empty(t, replicas)
	})
}

func TestScatterSearchRetriesUnavailableReplica(t *testing.T) {
	env := setupClusterEnv(t)
	withSearchRoutes(t, env)
	env.own(t, remoteIndex, "node-gone")
	stubReplicaReadable(t, true)
	env.respond = func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, InternalSearchPath, r.URL.Path)
		_, err := io.WriteString(w, `{"hits":{"total":{"value":1},"hits":[{"_id":"from-ready-replica"}]}}`)
		assert.NoError(t, err)
	}))
	defer ready.Close()
	peer := cluster.NewMembership(env.store, cluster.KeyPrefix(),
		cluster.Node{ID: "node-c", Addr: ready.URL}, time.Minute, time.Second)
	require.NoError(t, peer.Join(context.Background()))
	path := "/es/" + remoteIndex + "*/_search"

	env.setReplicas(t, remoteIndex, "node-b", "node-c")
	w := env.do(httptest.NewRequest(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "from-ready-replica")
	env.received(t)

	env.setReplicas(t, remoteIndex, "node-b")
	w = env.do(httptest.NewRequest(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "replica index unavailable")
	env.received(t)

	env.respond = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, err := io.WriteString(w, `{"error":"index `+remoteIndex+` does not exists"}`)
		assert.NoError(t, err)
	}
	env.setReplicas(t, remoteIndex, "node-b", "node-c")
	w = env.do(httptest.NewRequest(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "from-ready-replica")
	env.received(t)
}

// replicaNode joins an httptest node to the cluster of env. It answers an
// internal search with one hit named after itself and the indexes asked
// for, or 404 when serves refuses them, and records what it was asked.
type replicaNode struct {
	mu     sync.Mutex
	asked  [][]string
	serves func(indexes []string) bool
}

func (n *replicaNode) handler(t *testing.T, id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Indexes  []string `json:"indexes"`
			Replica  bool     `json:"replica"`
			SortKeys bool     `json:"sort_keys"`
		}
		data, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.NoError(t, json.Unmarshal(data, &payload))
		assert.True(t, payload.Replica, "a replica is told it is asked as one")
		assert.True(t, payload.SortKeys)
		n.mu.Lock()
		n.asked = append(n.asked, payload.Indexes)
		serves := n.serves
		n.mu.Unlock()
		if !serves(payload.Indexes) {
			http.NotFound(w, r)
			return
		}
		_, err = io.WriteString(w, `{"hits":{"total":{"value":1},"hits":[{"_id":"`+id+":"+strings.Join(payload.Indexes, "+")+`"}]}}`)
		assert.NoError(t, err)
	}
}

// reset sets what the node serves and forgets what it was asked.
func (n *replicaNode) reset(serves func(indexes []string) bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.asked, n.serves = nil, serves
}

func (n *replicaNode) requests() [][]string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.asked
}

func (env *clusterTestEnv) join(t *testing.T, id, addr string) {
	t.Helper()
	peer := cluster.NewMembership(env.store, cluster.KeyPrefix(), cluster.Node{ID: id, Addr: addr}, time.Minute, time.Second)
	require.NoError(t, peer.Join(context.Background()))
}

// The indexes of a lost owner are asked from their first replica in one
// request, what it cannot serve index by index from each replica in turn.
func TestScatterSearchBatchesReplicas(t *testing.T) {
	const second = "TestScatterSearchBatchesReplicas.index_2"
	env := setupClusterEnv(t)
	require.NotContains(t, []int{cluster.PartitionOfN(localIndex, testPartitions), cluster.PartitionOfN(remoteIndex, testPartitions)},
		cluster.PartitionOfN(second, testPartitions), "another partition than the fixture indexes")
	withSearchRoutes(t, env)
	registerIndex(t, second)
	env.own(t, remoteIndex, "node-gone")
	env.own(t, second, "node-gone")
	stubReplicaReadable(t, true)

	nodeB, nodeC := &replicaNode{}, &replicaNode{}
	env.respond = nodeB.handler(t, "node-b")
	ready := httptest.NewServer(nodeC.handler(t, "node-c"))
	defer ready.Close()
	env.join(t, "node-c", ready.URL)

	ctx := context.Background()
	nodes := []string{"node-b", "node-c"}
	_, rev, err := env.assigner.ListReplicas(ctx)
	require.NoError(t, err)
	ok, err := env.assigner.SetReplicas(ctx, map[int][]string{
		cluster.PartitionOfN(remoteIndex, testPartitions): nodes,
		cluster.PartitionOfN(second, testPartitions):      nodes,
	}, rev)
	require.NoError(t, err)
	require.True(t, ok)
	require.Eventually(t, func() bool {
		return assert.ObjectsAreEqual(nodes, cluster.Replicas(remoteIndex)) && assert.ObjectsAreEqual(nodes, cluster.Replicas(second))
	}, 10*time.Second, 5*time.Millisecond)

	all := func([]string) bool { return true }
	none := func([]string) bool { return false }
	only := func(index string) func([]string) bool {
		return func(indexes []string) bool { return len(indexes) == 1 && indexes[0] == index }
	}
	batch := []string{second, remoteIndex} // sorted

	tests := []struct {
		name         string
		nodeB, nodeC func([]string) bool
		wantCode     int
		wantBody     []string
		wantB, wantC [][]string
	}{
		{
			name:  "one request for the indexes of a replica",
			nodeB: all, nodeC: all,
			wantCode: http.StatusOK,
			wantBody: []string{"node-b:" + second + "+" + remoteIndex},
			wantB:    [][]string{batch},
		},
		{
			name:  "what a replica lacks is asked index by index",
			nodeB: only(remoteIndex), nodeC: all,
			wantCode: http.StatusOK,
			wantBody: []string{"node-b:" + remoteIndex + `"`, "node-c:" + second + `"`},
			wantB:    [][]string{batch, {remoteIndex}, {second}},
			wantC:    [][]string{{second}},
		},
		{
			name:  "the next replica serves them all",
			nodeB: none, nodeC: all,
			wantCode: http.StatusOK,
			wantBody: []string{"node-c:" + remoteIndex + `"`, "node-c:" + second + `"`},
			wantB:    [][]string{batch, {remoteIndex}, {second}},
			wantC:    [][]string{{remoteIndex}, {second}},
		},
		{
			name:  "no replica has one of them",
			nodeB: none, nodeC: only(remoteIndex),
			wantCode: http.StatusBadGateway,
			wantBody: []string{"replica index unavailable"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeB.reset(tt.nodeB)
			nodeC.reset(tt.nodeC)
			w := env.do(httptest.NewRequest(http.MethodPost, "/es/"+remoteIndex+","+second+"/_search", nil))
			for len(env.requests) > 0 {
				<-env.requests
			}
			assert.Equal(t, tt.wantCode, w.Code)
			for _, want := range tt.wantBody {
				assert.Contains(t, w.Body.String(), want)
			}
			if tt.wantCode == http.StatusOK {
				assert.ElementsMatch(t, tt.wantB, nodeB.requests())
				assert.ElementsMatch(t, tt.wantC, nodeC.requests())
			}
		})
	}
}

// A replica that is down or fails is skipped like one without a copy, a
// replica that rejects the query is not: every other one would too.
func TestScatterSearchReplicaErrors(t *testing.T) {
	env := setupClusterEnv(t)
	withSearchRoutes(t, env)
	env.own(t, remoteIndex, "node-gone")
	stubReplicaReadable(t, true)

	nodeC := &replicaNode{}
	ready := httptest.NewServer(nodeC.handler(t, "node-c"))
	defer ready.Close()
	env.join(t, "node-c", ready.URL)
	down := httptest.NewServer(http.NotFoundHandler())
	env.join(t, "node-down", down.URL)
	down.Close()

	status := func(code int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { http.Error(w, body, code) }
	}
	tests := []struct {
		name     string
		replicas []string
		nodeB    http.HandlerFunc
		wantCode int
		wantBody string
		wantC    int // requests node-c gets
	}{
		{
			name:     "a replica that fails is skipped",
			replicas: []string{"node-b", "node-c"},
			nodeB:    status(http.StatusInternalServerError, "disk full"),
			wantCode: http.StatusOK, wantBody: "node-c:" + remoteIndex, wantC: 1,
		},
		{
			name:     "a replica that cannot be reached is skipped",
			replicas: []string{"node-down", "node-c"},
			wantCode: http.StatusOK, wantBody: "node-c:" + remoteIndex, wantC: 1,
		},
		{
			name:     "a replica that breaks off its answer is skipped",
			replicas: []string{"node-b", "node-c"},
			nodeB: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "100")
				_, err := io.WriteString(w, `{"hits":`)
				assert.NoError(t, err)
				panic(http.ErrAbortHandler)
			},
			wantCode: http.StatusOK, wantBody: "node-c:" + remoteIndex, wantC: 1,
		},
		{
			name:     "a replica that rejects the query ends the search",
			replicas: []string{"node-b", "node-c"},
			nodeB:    status(http.StatusBadRequest, `{"error":"bad query"}`),
			wantCode: http.StatusBadGateway, wantBody: "bad query",
		},
		{
			name:     "the last replica fails",
			replicas: []string{"node-c", "node-b"},
			nodeB:    status(http.StatusServiceUnavailable, "starting"),
			wantCode: http.StatusBadGateway, wantBody: "replica index unavailable: 503 Service Unavailable: starting", wantC: 1,
		},
		{
			name:     "the only replica cannot be reached",
			replicas: []string{"node-down"},
			wantCode: http.StatusBadGateway, wantBody: "node node-down: replica index unavailable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env.setReplicas(t, remoteIndex, tt.replicas...)
			env.respond = tt.nodeB
			// node-c has no copy when it is asked before node-b
			nodeC.reset(func([]string) bool { return tt.replicas[0] != "node-c" })
			w := env.do(httptest.NewRequest(http.MethodPost, "/es/"+remoteIndex+"*/_search", nil))
			for len(env.requests) > 0 {
				<-env.requests
			}
			assert.Equal(t, tt.wantCode, w.Code)
			assert.Contains(t, w.Body.String(), tt.wantBody)
			assert.Len(t, nodeC.requests(), tt.wantC)
		})
	}
}

// A replica picked before it left the cluster is skipped like one that is
// down, an owner without an address still ends the search.
func TestScatterSearchReplicaLeft(t *testing.T) {
	env := setupClusterEnv(t)
	nodeC := &replicaNode{}
	nodeC.reset(func([]string) bool { return true })
	ready := httptest.NewServer(nodeC.handler(t, "node-c"))
	defer ready.Close()
	env.join(t, "node-c", ready.URL)
	ctx := context.Background()

	responses, err := scatter(ctx, nil, map[string][]string{remoteIndex: {"node-left", "node-c"}}, nil, "")
	require.NoError(t, err)
	require.Len(t, responses, 2)
	assert.Nil(t, responses[0], "the batch node-left was asked for")
	require.NotNil(t, responses[1])
	assert.Equal(t, "node-c:"+remoteIndex, responses[1].Hits.Hits[0].ID)
	assert.Equal(t, [][]string{{remoteIndex}}, nodeC.requests())

	_, err = internalSearch(ctx, "node-left", []string{remoteIndex}, nil, "", false)
	require.Error(t, err)
	assert.NotErrorIs(t, err, errReplicaUnavailable)
	assert.Contains(t, err.Error(), "node-left is unreachable")
}

// A node that redirects keeps the credentials: the redirect is not followed.
func TestScatterSearchRefusesRedirects(t *testing.T) {
	env := setupClusterEnv(t)
	withSearchRoutes(t, env)
	trap := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the redirect was followed")
	}))
	defer trap.Close()
	env.respond = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, trap.URL+InternalSearchPath, http.StatusTemporaryRedirect)
	}

	req := httptest.NewRequest(http.MethodPost, "/es/"+remoteIndex+"*/_search", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := env.do(req)
	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "307 Temporary Redirect")
	env.received(t)
}

func TestReplicaIndexMissing(t *testing.T) {
	const missing = `{"error":"index a does not exists"}`
	tests := []struct {
		name    string
		status  int
		body    string
		indexes []string
		want    bool
	}{
		{name: "not found", status: http.StatusNotFound, body: "404 page not found", indexes: []string{"a", "b"}, want: true},
		{name: "older peer, missing index", status: http.StatusBadRequest, body: missing, indexes: []string{"a"}, want: true},
		{name: "older peer, a batch names no index", status: http.StatusBadRequest, body: missing, indexes: []string{"a", "b"}},
		{name: "older peer, another index", status: http.StatusBadRequest, body: missing, indexes: []string{"b"}},
		{name: "rejected query", status: http.StatusBadRequest, body: `{"error":"bad query"}`, indexes: []string{"a"}},
		{name: "not json", status: http.StatusBadRequest, body: "index a does not exists", indexes: []string{"a"}},
		{name: "server error", status: http.StatusInternalServerError, body: missing, indexes: []string{"a"}},
		{name: "answered", status: http.StatusOK, body: missing, indexes: []string{"a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, replicaIndexMissing(tt.status, []byte(tt.body), tt.indexes))
		})
	}
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
