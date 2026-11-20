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
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"

	"github.com/vcaesar/zedsearch/pkg/cluster"
	"github.com/vcaesar/zedsearch/pkg/cluster/replication"
	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/meta/elastic"
	"github.com/vcaesar/zedsearch/pkg/tracing"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
	"github.com/vcaesar/zedsearch/pkg/zutils"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

const (
	// ForwardedByHeader names the node that proxied a request, a request
	// carrying it must be served locally or fail, never proxied again.
	ForwardedByHeader = "X-Zed-Forwarded-By"
	// ClusterEpochKey holds the ownership epoch the request was admitted
	// with, handlers may read it from the request context.
	ClusterEpochKey = "cluster_epoch"
	// InternalSearchPath is the node to node search endpoint.
	InternalSearchPath = "/api/_internal/search"

	scatterParallelism = 8
	scatterTimeout     = 30 * time.Second
)

// clusterClient is shared so node to node calls reuse connections. It never
// follows a redirect: the credentials stay with the node that was asked.
var clusterClient = &http.Client{Timeout: scatterTimeout, Transport: cluster.Transport(), CheckRedirect: cluster.NoRedirect}

// The replication steps around a write, variables for tests.
var (
	prepareWrite     = replication.Prepare
	commitWrite      = replication.Commit
	prepareAllWrites = replication.PrepareAll
	commitAllWrites  = replication.CommitAll
)

// ClusterRoute routes a request for the `:target` index to the node that
// owns it.
//
//   - clustering disabled: nothing happens, this is the single node build
//   - another node owns the index: the request is proxied to it
//   - we own it: the ownership epoch is recorded and, for writes, checked
//     again after the handler ran. A partition that moved meanwhile turns
//     the response into 409 so the client retries against the new owner
//   - several indexes or a glob on an Elasticsearch search path: the
//     request is split per owner and the responses are merged
func ClusterRoute() zhttp.HandlerFunc {
	return func(c zhttp.Context) {
		if !cluster.Enabled() {
			c.Next()
			return
		}

		target := c.Param("target")
		if isScatterTarget(c, target) {
			scatterSearch(c, target)
			return
		}
		if target == "" {
			serveUntargeted(c)
			return
		}

		owner, epoch, ok := cluster.Owner(target)
		if !ok {
			c.Next() // not assigned yet, serve locally like a single node
			return
		}
		if owner != localNodeID() {
			read := !isMutating(c.Method(), c.Path())
			if read && servesReplica(target, owner, newResolver(c.Context()).reachable) {
				c.Next() // warm replica of a cold index, or a copy that holds every write
				return
			}
			var fallbacks []string
			if read && (replicaReadable(target) || replicated(target)) {
				fallbacks = cluster.Replicas(target)
			}
			forward(c, owner, fallbacks)
			return
		}

		c.Set(ClusterEpochKey, epoch)
		if !isMutating(c.Method(), c.Path()) {
			c.Next()
			return
		}
		serveFenced(c, target, epoch)
	}
}

// serveFenced runs the handler with its output buffered, so a handoff that
// happened while the write was in flight can still be reported to the
// client instead of a misleading success.
func serveFenced(c zhttp.Context, target string, epoch int64) {
	// a node that may have lost its lease must not reach the WAL at all
	if errors.Is(cluster.CheckEpoch(target, epoch), cluster.ErrNotLive) {
		rejectWrite(c, target, cluster.ErrNotLive)
		return
	}
	// a replica that just became owner replays what it kept first; a bulk
	// body may name other indexes than the path
	bulk := isBulk(c.Path())
	prepare := func(ctx context.Context) error { return prepareWrite(ctx, target) }
	commit := func(ctx context.Context) error { return commitWrite(ctx, target) }
	if bulk {
		prepare, commit = prepareAllWrites, commitAllWrites
	}
	if err := prepare(c.Context()); err != nil {
		rejectUnreplicated(c, target, err)
		return
	}
	flush := c.NextBuffered()

	// acknowledge only what every in-sync replica (or the bucket) holds
	if err := commit(c.Context()); err != nil {
		rejectUnreplicated(c, target, err)
		return
	}
	err := cluster.CheckEpoch(target, epoch)
	if err == nil {
		if flushErr := flush(); flushErr != nil {
			log.Error().Err(flushErr).Msg("cluster: flush buffered response")
		}
		return
	}
	rejectWrite(c, target, err)
}

// rejectWrite answers 409 so the client retries against the current owner.
// OTLP exporters drop a 409 batch but retry 503, so OTLP logs get 503.
func rejectWrite(c zhttp.Context, target string, err error) {
	owner := ""
	var epochErr *cluster.EpochError
	if errors.As(err, &epochErr) {
		owner = epochErr.Owner
	}
	log.Warn().Ctx(c.Context()).Err(err).Str("index", target).Str("owner", owner).
		Msg("cluster: rejecting write, partition moved")
	status := http.StatusConflict
	if strings.HasSuffix(c.Path(), "/v1/logs") {
		status = http.StatusServiceUnavailable
	}
	c.Abort()
	zutils.RenderJSON(c, status, zhttp.H{"error": "partition moved", "owner": owner})
}

// rejectUnreplicated answers 503 for a write that could not be made
// durable on the replicas: the client must not count it as stored. A commit
// that failed because the partition moved is a 409 like any fenced write.
func rejectUnreplicated(c zhttp.Context, target string, err error) {
	var epochErr *cluster.EpochError
	if errors.As(err, &epochErr) || errors.Is(err, cluster.ErrNotLive) {
		rejectWrite(c, target, err)
		return
	}
	log.Error().Ctx(c.Context()).Err(err).Str("index", target).Msg("cluster: write not replicated")
	c.Abort()
	zutils.RenderJSON(c, http.StatusServiceUnavailable, zhttp.H{"error": "write not replicated: " + err.Error()})
}

// serveUntargeted runs a request without an index in the path. A write
// such as `_bulk` names its indexes in the body, so every index it touched
// is committed before the response leaves.
func serveUntargeted(c zhttp.Context) {
	if !replication.Enabled() || !isMutating(c.Method(), c.Path()) || strings.HasPrefix(c.Path(), "/api/_internal/") {
		c.Next()
		return
	}
	if err := prepareAllWrites(c.Context()); err != nil {
		rejectUnreplicated(c, "", err)
		return
	}
	flush := c.NextBuffered()
	if err := commitAllWrites(c.Context()); err != nil {
		rejectUnreplicated(c, "", err)
		return
	}
	if err := flush(); err != nil {
		log.Error().Err(err).Msg("cluster: flush buffered response")
	}
}

// isBulk reports whether path is a bulk write, whose body names indexes.
func isBulk(path string) bool {
	return strings.Contains(path, "/_bulk")
}

// forward proxies the request to the node that owns the index. When the
// owner cannot be resolved (it died, the partition is not reassigned yet)
// the first resolvable fallback, a read replica, gets the request instead.
func forward(c zhttp.Context, owner string, fallbacks []string) {
	if c.GetHeader(ForwardedByHeader) != "" {
		// the sender thought we owned the index and we think somebody else
		// does: bouncing it again would loop
		c.Abort()
		zutils.RenderJSON(c, http.StatusLoopDetected,
			zhttp.H{"error": "request already forwarded", "owner": owner})
		return
	}

	local := localNodeID()
	addr, ok := cluster.NodeAddr(c.Context(), owner)
	for _, replica := range fallbacks {
		if ok && addr != "" {
			break
		}
		if replica != local {
			addr, ok = cluster.NodeAddr(c.Context(), replica)
		}
	}
	if !ok || addr == "" {
		c.Abort()
		zutils.RenderJSON(c, http.StatusServiceUnavailable,
			zhttp.H{"error": "owner node is unreachable", "owner": owner})
		return
	}
	target, err := url.Parse(addr)
	if err != nil {
		log.Error().Err(err).Str("addr", addr).Msg("cluster: bad advertise address")
		c.Abort()
		zutils.RenderJSON(c, http.StatusServiceUnavailable,
			zhttp.H{"error": "owner node has an invalid address", "owner": owner})
		return
	}

	authorization := c.GetHeader("Authorization")
	proxy := &httputil.ReverseProxy{
		// traces the hop and passes the trace on in `traceparent`
		Transport: cluster.Transport(),
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			pr.Out.Header.Set(ForwardedByHeader, local)
			if authorization != "" {
				pr.Out.Header.Set("Authorization", authorization)
			}
			// net/http closes the inbound body once the response starts; the
			// transport's check for bytes past ContentLength would then fail
			// and close the owner connection mid-response
			if pr.In.ContentLength > 0 {
				pr.Out.Body = io.NopCloser(io.LimitReader(pr.In.Body, pr.In.ContentLength))
			}
		},
		// the owner answers with the same trace id, keep a single header
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del(tracing.TraceIDHeader)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Error().Ctx(r.Context()).Err(err).Str("owner", owner).Msg("cluster: forward failed")
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	c.ServeHTTP(proxy)
	c.Abort()
}

// scatterSearch splits a multi index search by owner, runs the parts in
// parallel against `/api/_internal/search` and merges the responses.
func scatterSearch(c zhttp.Context, target string) {
	body, err := io.ReadAll(c.Body())
	if err != nil {
		c.Abort()
		zutils.RenderJSON(c, http.StatusBadRequest, meta.HTTPResponseError{Error: err.Error()})
		return
	}

	query := &meta.ZedQuery{Size: 10}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, query); err != nil {
			c.Abort()
			zutils.RenderJSON(c, http.StatusBadRequest, meta.HTTPResponseError{Error: err.Error()})
			return
		}
	}

	groups, replicas := groupByOwner(c.Context(), resolveIndexes(target))
	if _, local := groups[localNodeID()]; len(replicas) == 0 && len(groups) <= 1 && (local || len(groups) == 0) {
		// everything we would ask for is ours: let the normal handler do it
		c.SetBody(body)
		c.Next()
		return
	}

	responses, err := scatter(c.Context(), groups, replicas, cluster.ScatterQuery(query), c.GetHeader("Authorization"))
	if err != nil {
		log.Error().Ctx(c.Context()).Err(err).Str("target", target).Msg("cluster: scatter search failed")
		c.Abort()
		zutils.RenderJSON(c, http.StatusBadGateway, meta.HTTPResponseError{Error: err.Error()})
		return
	}

	c.Abort()
	zutils.RenderJSON(c, http.StatusOK, cluster.MergeSearch(responses, query))
}

// scatter asks every owner for its indexes and, in one request too, the
// first replica of the indexes whose owner is gone. What that replica
// cannot serve is then asked index by index from each replica in turn,
// skipping the nodes that failed as a whole.
// The responses keep a stable order, which also breaks ties between
// identical owner sort keys.
func scatter(ctx context.Context, groups, replicas map[string][]string, query *meta.ZedQuery, authorization string) ([]*meta.SearchResponse, error) {
	type part struct {
		node    string
		indexes []string
		replica bool
	}
	batches := make(map[string][]string)
	for _, index := range slices.Sorted(maps.Keys(replicas)) {
		node := replicas[index][0]
		batches[node] = append(batches[node], index)
	}
	parts := make([]part, 0, len(groups)+len(batches))
	for _, owner := range slices.Sorted(maps.Keys(groups)) {
		parts = append(parts, part{node: owner, indexes: groups[owner]})
	}
	for _, node := range slices.Sorted(maps.Keys(batches)) {
		parts = append(parts, part{node: node, indexes: batches[node], replica: true})
	}

	responses := make([]*meta.SearchResponse, len(parts))
	unavailable := make([]error, len(parts))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(scatterParallelism)
	for i, p := range parts {
		group.Go(func() error {
			var err error
			responses[i], err = internalSearch(groupCtx, p.node, p.indexes, query, authorization, p.replica)
			if errors.Is(err, errReplicaUnavailable) {
				unavailable[i] = err
				return nil
			}
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	type retry struct {
		index string
		nodes []string
		err   error // what the first replica answered
	}
	// a node that failed as a whole is not asked again for any index; one
	// that rejected a batch may still serve some of its indexes alone
	var mu sync.Mutex
	down := make(map[string]bool)
	var retries []retry
	for i, p := range parts {
		if unavailable[i] == nil {
			continue
		}
		if errors.Is(unavailable[i], errNodeUnavailable) {
			down[p.node] = true
		}
		for _, index := range p.indexes {
			nodes := replicas[index]
			if len(p.indexes) == 1 {
				nodes = nodes[1:] // the request that failed was this one
			}
			retries = append(retries, retry{index: index, nodes: nodes, err: unavailable[i]})
		}
	}
	retried := make([]*meta.SearchResponse, len(retries))
	group, groupCtx = errgroup.WithContext(ctx)
	group.SetLimit(scatterParallelism)
	for i, r := range retries {
		group.Go(func() error {
			err := r.err
			for _, node := range r.nodes {
				mu.Lock()
				skip := down[node]
				mu.Unlock()
				if skip {
					continue
				}
				retried[i], err = internalSearch(groupCtx, node, []string{r.index}, query, authorization, true)
				if errors.Is(err, errNodeUnavailable) {
					mu.Lock()
					down[node] = true
					mu.Unlock()
				}
				if !errors.Is(err, errReplicaUnavailable) {
					return err
				}
			}
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return append(responses, retried...), nil
}

// errReplicaUnavailable marks an answer another replica may still give: the
// node does not respond, fails, or holds no usable copy of the index.
var errReplicaUnavailable = errors.New("replica index unavailable")

// errNodeUnavailable marks a replica that failed as a whole: it left, did
// not respond or broke off its answer, whatever indexes it was asked for.
var errNodeUnavailable = errors.New("node unavailable")

// internalSearch asks one node for its part of a scattered search. A
// replica is told so: it answers only from a copy it may serve. The client
// credentials go along as they are, like in forward, so the nodes must
// advertise https addresses unless the network between them is trusted.
func internalSearch(ctx context.Context, owner string, indexes []string, query *meta.ZedQuery, authorization string, replica bool) (*meta.SearchResponse, error) {
	// a replica that left, stopped answering or broke off its answer may be
	// stood in for by the next one
	unavailable := func(err error) error {
		if replica {
			return fmt.Errorf("cluster: node %s: %w: %w: %w", owner, errReplicaUnavailable, errNodeUnavailable, err)
		}
		return err
	}
	addr, ok := cluster.NodeAddr(ctx, owner)
	if !ok || addr == "" {
		return nil, unavailable(errors.New("cluster: node " + owner + " is unreachable"))
	}
	// sort_keys: the merge orders top_hits by the doc values of the owners
	payload, err := json.Marshal(map[string]interface{}{"indexes": indexes, "query": query, "replica": replica, "sort_keys": true})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, addr+InternalSearchPath, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ForwardedByHeader, localNodeID())
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	resp, err := clusterClient.Do(req)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Warn().Err(err).Msg("cluster: close internal search body")
		}
	}()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, unavailable(err)
	}
	if replica && (resp.StatusCode >= http.StatusInternalServerError || replicaIndexMissing(resp.StatusCode, data, indexes)) {
		return nil, fmt.Errorf("cluster: node %s: %w: %s: %s", owner, errReplicaUnavailable, resp.Status, data)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("cluster: node " + owner + " returned " + resp.Status + ": " + string(data))
	}
	return cluster.DecodeSearchResponse(data)
}

// replicaIndexMissing reports whether a replica answered that it has no
// usable copy: 404, or from older peers a 400 with this exact error for a
// single missing index.
func replicaIndexMissing(status int, data []byte, indexes []string) bool {
	if status == http.StatusNotFound {
		return true
	}
	if status != http.StatusBadRequest || len(indexes) != 1 {
		return false
	}
	var response meta.HTTPResponseError
	return json.Unmarshal(data, &response) == nil && response.Error == "index "+indexes[0]+" does not exists"
}

// groupByOwner batches indexes served by their owner or a readable local
// replica. For an unreachable owner it collects the reachable remote
// replicas of each index instead, in the order scatter tries them. Each
// node is resolved once per pass, and ctx bounds the lookups.
func groupByOwner(ctx context.Context, indexes []string) (groups, replicas map[string][]string) {
	local := localNodeID()
	reachable := newResolver(ctx).reachable

	groups = make(map[string][]string)
	replicas = make(map[string][]string)
	for _, name := range indexes {
		owner, _, ok := cluster.Owner(name)
		switch {
		case !ok || (owner != local && servesReplica(name, owner, reachable)):
			owner = local
		case !reachable(owner) && (replicaReadable(name) || replicated(name)):
			for _, replica := range cluster.Replicas(name) {
				// this node is a replica without a usable copy, or the case above took it
				if replica != local && reachable(replica) {
					replicas[name] = append(replicas[name], replica)
				}
			}
		}
		if len(replicas[name]) == 0 {
			groups[owner] = append(groups[owner], name)
		}
	}
	return groups, replicas
}

// replicaReadable reports whether this node may serve reads of an index it
// replicates. A variable for tests.
var replicaReadable = replication.Readable

// replicaCurrent reports whether the copy this node replicates has applied
// everything the owner shipped. A variable for tests.
var replicaCurrent = replication.Current

// servesReplica reports whether this node answers a read of an index owner
// owns from its own copy. A copy that is not current would miss
// acknowledged writes, so the owner answers while it is reachable.
func servesReplica(name, owner string, reachable func(string) bool) bool {
	if !cluster.IsLocalReplica(name) || !replicaReadable(name) {
		return false
	}
	return replicaCurrent(name) || !reachable(owner)
}

// resolver memoizes node reachability for one routing pass: an owner that
// misses the member cache costs one bounded lookup, not one per index.
type resolver struct {
	ctx      context.Context
	resolved map[string]bool
}

func newResolver(ctx context.Context) *resolver {
	return &resolver{ctx: ctx, resolved: map[string]bool{localNodeID(): true}}
}

func (r *resolver) reachable(node string) bool {
	ok, seen := r.resolved[node]
	if !seen {
		addr, found := cluster.NodeAddr(r.ctx, node)
		ok = found && addr != ""
		r.resolved[node] = ok
	}
	return ok
}

// replicated reports whether the replicas of a disk index hold a copy, so
// a read may fall back to them when the owner is gone.
func replicated(name string) bool {
	if !replication.Enabled() || replication.Mode() == replication.ModeNone {
		return false
	}
	index, ok := core.GetIndex(name)
	return ok && index.IsDisk()
}

// resolveIndexes expands a target into concrete index names using the
// index list of this node. It is complete only when the nodes share their
// index metadata (ZED_METADATA_STORAGE=etcd).
func resolveIndexes(target string) []string {
	patterns := elastic.SplitPatterns(target)
	if len(patterns) == 1 && (patterns[0] == "_all" || patterns[0] == "*") {
		patterns = nil
	}
	names := make([]string, 0, core.ZED_INDEX_LIST.Len())
	for _, index := range core.ZED_INDEX_LIST.List() {
		name := index.GetName()
		if elastic.MatchAnyPattern(name, patterns) {
			names = append(names, name)
		}
	}
	return names
}

// isScatterTarget reports whether the request asks several indexes at once
// on an Elasticsearch search path. `/api/:target/_search` speaks the v1
// query format and is left alone.
func isScatterTarget(c zhttp.Context, target string) bool {
	if c.GetHeader(ForwardedByHeader) != "" {
		return false
	}
	if !strings.HasPrefix(c.Path(), "/es/") ||
		!strings.HasSuffix(c.Path(), "/_search") {
		return false
	}
	return target == "" || isMultiTarget(target)
}

func isMultiTarget(target string) bool {
	return strings.ContainsAny(target, ",*") || target == "_all"
}

// readOnlyPostSuffixes are POST endpoints that only read, fencing them
// would turn a search during a handoff into a spurious 409.
var readOnlyPostSuffixes = []string{"/_search", "/_msearch", "/_count", "/_field_caps", "/_analyze"}

func isMutating(method, path string) bool {
	switch method {
	case http.MethodPost:
		for _, suffix := range readOnlyPostSuffixes {
			if strings.HasSuffix(path, suffix) {
				return false
			}
		}
		return true
	case http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func localNodeID() string {
	if node := cluster.Local(); node != nil {
		return node.ID
	}
	return ""
}
