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
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/cluster"
	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/tracing"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

const (
	testPartitions = 8
	localIndex     = "cluster-middleware-local"
	remoteIndex    = "cluster-middleware-remote"
)

// clusterTestEnv is a two node cluster: this process is node-a, node-b is
// an httptest server that records what it was asked to do.
type clusterTestEnv struct {
	store    *cluster.MemoryStore
	assigner *cluster.Assigner
	peer     *httptest.Server
	engine   *zhttp.Engine
	requests chan *http.Request
	// respond, when set, answers for node-b instead of the default body
	respond http.HandlerFunc
}

// setupClusterEnv makes node-a own localIndex and node-b own remoteIndex.
func setupClusterEnv(t *testing.T) *clusterTestEnv {
	t.Helper()
	return setupClusterEnvWith(t, 15*time.Second, nil)
}

// setupClusterEnvWith is setupClusterEnv with a heartbeat ttl and an optional
// wrapper around the store node-a runs on.
func setupClusterEnvWith(t *testing.T, ttl time.Duration, wrap func(cluster.Store) cluster.Store) *clusterTestEnv {
	t.Helper()
	ctx := context.Background()

	env := &clusterTestEnv{requests: make(chan *http.Request, 4)}
	env.peer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// read before the send, so a test may swap it once it received r
		respond := env.respond
		env.requests <- r.Clone(context.Background())
		if respond != nil {
			respond(w, r)
			return
		}
		// a node answers with the trace id it was handed, like Tracing
		w.Header().Set(tracing.TraceIDHeader, tracing.TraceID(tracing.Extract(r.Context(), r.Header.Get)))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"served_by":"node-b"}`))
		assert.NoError(t, err)
	}))
	t.Cleanup(env.peer.Close)

	previous := config.Global.Cluster
	config.Global.Cluster.Name = "middleware-test"
	config.Global.Cluster.NodeID = "node-a"
	config.Global.Cluster.AdvertiseAddr = "http://127.0.0.1:0"
	config.Global.Cluster.Partitions = testPartitions
	config.Global.Cluster.HeartbeatTTL = ttl
	config.Global.Cluster.HeartbeatInterval = 100 * time.Millisecond

	env.store = cluster.NewMemoryStore()
	var store cluster.Store = env.store
	if wrap != nil {
		store = wrap(store)
	}
	// a foreign leader keeps node-a's coordinator from rebalancing the
	// partitions the tests assign by hand
	_, won, err := env.store.Create(ctx, cluster.KeyPrefix()+"leader", []byte("node-z"), cluster.NoLease)
	require.NoError(t, err)
	require.True(t, won)
	require.NoError(t, cluster.StartWith(ctx, store))
	t.Cleanup(func() {
		cluster.Stop()
		config.Global.Cluster = previous
	})

	// node-b joins so its advertised address can be resolved
	peer := cluster.NewMembership(env.store, cluster.KeyPrefix(),
		cluster.Node{ID: "node-b", Addr: env.peer.URL}, time.Minute, time.Second)
	require.NoError(t, peer.Join(ctx))

	env.assigner = cluster.NewAssigner(env.store, cluster.KeyPrefix(), testPartitions)
	env.own(t, localIndex, "node-a")
	env.own(t, remoteIndex, "node-b")

	env.engine = zhttp.New()
	Tracing(env.engine)
	env.engine.Use(ClusterRoute())
	handler := func(c zhttp.Context) {
		c.JSON(http.StatusOK, zhttp.H{"served_by": "node-a", "index": c.Param("target")})
	}
	env.engine.GET("/api/:target/_doc/:id", handler)
	env.engine.POST("/api/:target/_doc", handler)
	return env
}

// own moves the partition of indexName to owner and waits until the local
// fence has seen it.
func (env *clusterTestEnv) own(t *testing.T, indexName, owner string) int64 {
	t.Helper()
	ctx := context.Background()
	partition := cluster.PartitionOfN(indexName, testPartitions)

	assignments, err := env.assigner.List(ctx)
	require.NoError(t, err)
	assignment, ok, err := env.assigner.Set(ctx, partition, owner, assignments[partition].Epoch)
	require.NoError(t, err)
	require.True(t, ok)

	require.Eventually(t, func() bool {
		got, epoch, ok := cluster.Owner(indexName)
		return ok && got == owner && epoch == assignment.Epoch
	}, 10*time.Second, 5*time.Millisecond)
	return assignment.Epoch
}

func (env *clusterTestEnv) do(req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	env.engine.ServeHTTP(w, req)
	return w
}

func TestClusterRouteDisabled(t *testing.T) {
	require.False(t, cluster.Enabled())

	engine := zhttp.New()
	engine.Use(ClusterRoute())
	engine.GET("/api/:target/_doc/:id", func(c zhttp.Context) {
		c.JSON(http.StatusOK, zhttp.H{"served_by": "local"})
	})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/any-index/_doc/1", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "local")
}

func TestClusterRouteForwardsToOwner(t *testing.T) {
	env := setupClusterEnv(t)

	req := httptest.NewRequest(http.MethodGet, "/api/"+remoteIndex+"/_doc/1", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := env.do(req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "node-b")

	select {
	case forwarded := <-env.requests:
		assert.Equal(t, "/api/"+remoteIndex+"/_doc/1", forwarded.URL.Path)
		assert.Equal(t, "node-a", forwarded.Header.Get(ForwardedByHeader))
		assert.Equal(t, "Basic dXNlcjpwYXNz", forwarded.Header.Get("Authorization"))
	case <-time.After(3 * time.Second):
		t.Fatal("the owner never received the request")
	}
}

func TestClusterRouteForwardKeepsTrace(t *testing.T) {
	env := setupClusterEnv(t)
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"

	req := httptest.NewRequest(http.MethodGet, "/api/"+remoteIndex+"/_doc/1", nil)
	req.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
	w := env.do(req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{traceID}, w.Header().Values(tracing.TraceIDHeader))
	select {
	case forwarded := <-env.requests:
		assert.Equal(t, traceID, tracing.TraceID(tracing.Extract(context.Background(), forwarded.Header.Get)))
	case <-time.After(3 * time.Second):
		t.Fatal("the owner never received the request")
	}
}

// closedAtEOF is a request body that is closed once its bytes are read, as
// net/http closes it when the response starts: reading on fails.
type closedAtEOF struct{ r io.Reader }

func (b *closedAtEOF) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if errors.Is(err, io.EOF) {
		return n, http.ErrBodyReadAfterClose
	}
	return n, err
}

func TestClusterRouteForwardStopsAtContentLength(t *testing.T) {
	if zhttp.Backend == "fiber" {
		t.Skip("fiber buffers the request body, the adaptor reads it before the proxy")
	}
	env := setupClusterEnv(t)
	body := []byte(`{"query":{"match_all":{}}}`)

	for range 20 {
		req := httptest.NewRequest(http.MethodPost, "/api/"+remoteIndex+"/_doc", &closedAtEOF{r: bytes.NewReader(body)})
		req.ContentLength = int64(len(body))
		w := env.do(req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"served_by":"node-b"}`, w.Body.String())
		<-env.requests
	}
}

func TestClusterRouteServesLocalOwner(t *testing.T) {
	env := setupClusterEnv(t)

	w := env.do(httptest.NewRequest(http.MethodGet, "/api/"+localIndex+"/_doc/1", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "node-a")
	assert.Empty(t, env.requests, "a locally owned index must not be forwarded")
}

func TestClusterRouteLoopGuard(t *testing.T) {
	env := setupClusterEnv(t)

	req := httptest.NewRequest(http.MethodGet, "/api/"+remoteIndex+"/_doc/1", nil)
	req.Header.Set(ForwardedByHeader, "node-c")
	w := env.do(req)

	assert.Equal(t, http.StatusLoopDetected, w.Code)
	body := map[string]interface{}{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "request already forwarded", body["error"])
	assert.Equal(t, "node-b", body["owner"])
	assert.Empty(t, env.requests)
}

func TestClusterRouteRejectsWriteAfterHandoff(t *testing.T) {
	env := setupClusterEnv(t)

	// the partition moves to node-b while the write is being handled
	env.engine.POST("/api/:target/_move", func(c zhttp.Context) {
		env.own(t, localIndex, "node-b")
		c.JSON(http.StatusOK, zhttp.H{"written": true})
	})

	w := env.do(httptest.NewRequest(http.MethodPost, "/api/"+localIndex+"/_move", nil))

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.NotContains(t, w.Body.String(), "written", "the handler output must not leak")
	body := map[string]interface{}{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "partition moved", body["error"])
	assert.Equal(t, "node-b", body["owner"])
}

// failingKeepAlive fails lease renewals once fail is set.
type failingKeepAlive struct {
	cluster.Store
	fail atomic.Bool
}

func (s *failingKeepAlive) KeepAlive(ctx context.Context, id cluster.LeaseID) error {
	if s.fail.Load() {
		return context.DeadlineExceeded
	}
	return s.Store.KeepAlive(ctx, id)
}

// A node whose lease may have expired must reject writes before they reach
// the handler (and the WAL), its partitions may be owned elsewhere.
func TestClusterRouteRejectsWriteWhenNotLive(t *testing.T) {
	var flaky *failingKeepAlive
	ttl := time.Second
	env := setupClusterEnvWith(t, ttl, func(store cluster.Store) cluster.Store {
		flaky = &failingKeepAlive{Store: store}
		return flaky
	})
	called := false
	env.engine.POST("/api/:target/_write", func(c zhttp.Context) {
		called = true
		c.JSON(http.StatusCreated, zhttp.H{"written": true})
	})
	env.engine.POST("/api/:target/v1/logs", func(c zhttp.Context) {
		called = true
		c.JSON(http.StatusOK, zhttp.H{})
	})

	flaky.fail.Store(true)
	time.Sleep(ttl + 100*time.Millisecond)

	w := env.do(httptest.NewRequest(http.MethodPost, "/api/"+localIndex+"/_write", nil))
	assert.Equal(t, http.StatusConflict, w.Code)
	w = env.do(httptest.NewRequest(http.MethodPost, "/api/"+localIndex+"/v1/logs", nil))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "OTLP exporters retry 503, not 409")
	assert.False(t, called, "the write must not run")
	assert.False(t, cluster.IsLocalOwner(localIndex))
}

func TestClusterRouteWriteKeepsEpoch(t *testing.T) {
	env := setupClusterEnv(t)

	var seen int64
	env.engine.POST("/api/:target/_write", func(c zhttp.Context) {
		epoch, ok := c.Get(ClusterEpochKey)
		require.True(t, ok, "the handler must see the epoch it was admitted with")
		seen = epoch.(int64)
		c.JSON(http.StatusCreated, zhttp.H{"written": true})
	})

	w := env.do(httptest.NewRequest(http.MethodPost, "/api/"+localIndex+"/_write", nil))
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), "written")

	_, epoch, ok := cluster.Owner(localIndex)
	require.True(t, ok)
	assert.Equal(t, epoch, seen)
}

// setReplicas makes nodes the read replicas of indexName and waits until the
// local fence has seen it.
func (env *clusterTestEnv) setReplicas(t *testing.T, indexName string, nodes ...string) {
	t.Helper()
	ctx := context.Background()
	_, rev, err := env.assigner.ListReplicas(ctx)
	require.NoError(t, err)
	partition := cluster.PartitionOfN(indexName, testPartitions)
	ok, err := env.assigner.SetReplicas(ctx, map[int][]string{partition: nodes}, rev)
	require.NoError(t, err)
	require.True(t, ok)
	require.Eventually(t, func() bool {
		return assert.ObjectsAreEqual(nodes, cluster.Replicas(indexName))
	}, 10*time.Second, 5*time.Millisecond)
}

func stubReplicaReadable(t *testing.T, readable bool) {
	t.Helper()
	previous := replicaReadable
	replicaReadable = func(string) bool { return readable }
	t.Cleanup(func() { replicaReadable = previous })
}

func stubReplicaCurrent(t *testing.T, current bool) {
	t.Helper()
	previous := replicaCurrent
	replicaCurrent = func(string) bool { return current }
	t.Cleanup(func() { replicaCurrent = previous })
}

func TestClusterRouteLocalReplica(t *testing.T) {
	env := setupClusterEnv(t)
	env.setReplicas(t, remoteIndex, "node-a")

	tests := []struct {
		name      string
		readable  bool
		current   bool
		method    string
		path      string
		wantLocal bool
	}{
		{name: "cold index read is served by the replica", readable: true, current: true, method: http.MethodGet, path: "/api/" + remoteIndex + "/_doc/1", wantLocal: true},
		{name: "write still goes to the owner", readable: true, current: true, method: http.MethodPost, path: "/api/" + remoteIndex + "/_doc", wantLocal: false},
		{name: "non cold index read goes to the owner", readable: false, current: true, method: http.MethodGet, path: "/api/" + remoteIndex + "/_doc/1", wantLocal: false},
		{name: "read of a copy still applying goes to the owner", readable: true, current: false, method: http.MethodGet, path: "/api/" + remoteIndex + "/_doc/1", wantLocal: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubReplicaReadable(t, tt.readable)
			stubReplicaCurrent(t, tt.current)
			w := env.do(httptest.NewRequest(tt.method, tt.path, nil))
			assert.Equal(t, http.StatusOK, w.Code)
			if tt.wantLocal {
				assert.Contains(t, w.Body.String(), "node-a")
				assert.Empty(t, env.requests)
				return
			}
			assert.Contains(t, w.Body.String(), "node-b")
			select {
			case <-env.requests:
			case <-time.After(3 * time.Second):
				t.Fatal("the owner never received the request")
			}
		})
	}
}

func TestClusterRouteFallsBackToReplica(t *testing.T) {
	env := setupClusterEnv(t)
	// the owner left the cluster, the leader has not reassigned yet
	env.own(t, remoteIndex, "node-gone")
	env.setReplicas(t, remoteIndex, "node-gone-too", "node-b")

	t.Run("cold read reaches a live replica", func(t *testing.T) {
		stubReplicaReadable(t, true)
		w := env.do(httptest.NewRequest(http.MethodGet, "/api/"+remoteIndex+"/_doc/1", nil))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "node-b")
		select {
		case <-env.requests:
		case <-time.After(3 * time.Second):
			t.Fatal("the replica never received the request")
		}
	})

	t.Run("write without the owner is unavailable", func(t *testing.T) {
		stubReplicaReadable(t, true)
		w := env.do(httptest.NewRequest(http.MethodPost, "/api/"+remoteIndex+"/_doc", nil))
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		assert.Contains(t, w.Body.String(), "node-gone")
	})

	t.Run("non cold read without the owner is unavailable", func(t *testing.T) {
		stubReplicaReadable(t, false)
		w := env.do(httptest.NewRequest(http.MethodGet, "/api/"+remoteIndex+"/_doc/1", nil))
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})
}

// A copy still applying what it was shipped answers once its owner is gone:
// the read would have nowhere else to go.
func TestClusterRouteLaggingReplicaWithoutOwner(t *testing.T) {
	env := setupClusterEnv(t)
	env.own(t, remoteIndex, "node-gone")
	env.setReplicas(t, remoteIndex, "node-a")
	stubReplicaReadable(t, true)
	stubReplicaCurrent(t, false)

	w := env.do(httptest.NewRequest(http.MethodGet, "/api/"+remoteIndex+"/_doc/1", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "node-a")
	assert.Empty(t, env.requests)
}

func TestGroupByOwnerUsesLocalReplica(t *testing.T) {
	env := setupClusterEnv(t)
	env.setReplicas(t, remoteIndex, "node-a")
	indexes := []string{localIndex, remoteIndex}

	stubReplicaReadable(t, false)
	groups, replicas := groupByOwner(indexes)
	assert.Equal(t, map[string][]string{"node-a": {localIndex}, "node-b": {remoteIndex}}, groups)
	assert.Empty(t, replicas)

	stubReplicaReadable(t, true)
	stubReplicaCurrent(t, false)
	groups, replicas = groupByOwner(indexes)
	assert.Equal(t, map[string][]string{"node-a": {localIndex}, "node-b": {remoteIndex}}, groups, "a copy still applying is not asked")
	assert.Empty(t, replicas)

	stubReplicaCurrent(t, true)
	groups, replicas = groupByOwner(indexes)
	assert.Equal(t, map[string][]string{"node-a": {localIndex, remoteIndex}}, groups)
	assert.Empty(t, replicas)
}

func TestIsMultiTarget(t *testing.T) {
	tests := []struct {
		target string
		want   bool
	}{
		{target: "logs", want: false},
		{target: "logs-2024", want: false},
		{target: "a,b", want: true},
		{target: "logs-*", want: true},
		{target: "_all", want: true},
		{target: "", want: false},
	}
	for _, tt := range tests {
		t.Run("target "+tt.target, func(t *testing.T) {
			assert.Equal(t, tt.want, isMultiTarget(tt.target))
		})
	}
}

func TestIsMutating(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		assert.True(t, isMutating(method, "/es/logs/_doc"), method)
	}
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		assert.False(t, isMutating(method, "/es/logs/_doc"), method)
	}
	for _, path := range []string{"/es/logs/_search", "/es/logs/_msearch", "/api/logs/_count", "/es/logs/_field_caps", "/api/logs/_analyze"} {
		assert.False(t, isMutating(http.MethodPost, path), path)
	}
	assert.True(t, isMutating(http.MethodPost, "/es/logs/_delete_by_query"))
}

func TestResolveIndexesPatterns(t *testing.T) {
	// no index is registered in this test binary, the helper must still
	// answer with an empty list instead of panicking
	for _, target := range []string{"_all", "*", "logs-*", "a,b"} {
		t.Run("target "+target, func(t *testing.T) {
			assert.NotNil(t, resolveIndexes(target))
		})
	}
}

func TestIsScatterTarget(t *testing.T) {
	newContext := func(method, path, forwardedBy string) zhttp.Context {
		req := httptest.NewRequest(method, path, strings.NewReader(""))
		if forwardedBy != "" {
			req.Header.Set(ForwardedByHeader, forwardedBy)
		}
		return zhttp.NewHTTPContext(httptest.NewRecorder(), req)
	}

	tests := []struct {
		name        string
		path        string
		target      string
		forwardedBy string
		want        bool
	}{
		{name: "es search with glob", path: "/es/logs-*/_search", target: "logs-*", want: true},
		{name: "es search with list", path: "/es/a,b/_search", target: "a,b", want: true},
		{name: "es search without target", path: "/es/_search", target: "", want: true},
		{name: "es search single index", path: "/es/logs/_search", target: "logs", want: false},
		{name: "api v1 search is left alone", path: "/api/logs-*/_search", target: "logs-*", want: false},
		{name: "already forwarded", path: "/es/logs-*/_search", target: "logs-*", forwardedBy: "node-b", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newContext(http.MethodPost, tt.path, tt.forwardedBy)
			assert.Equal(t, tt.want, isScatterTarget(c, tt.target))
		})
	}
}
