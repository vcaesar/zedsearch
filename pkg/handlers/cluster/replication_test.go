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

package cluster

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/cluster/replication"
	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// With replication off every endpoint refuses instead of touching data.
func TestReplicationHandlersDisabled(t *testing.T) {
	tests := []struct {
		name    string
		handler zhttp.HandlerFunc
		method  string
		target  string
		body    string
		want    int
	}{
		{name: "bad batch", handler: ReplicationReplicate, method: http.MethodPost, target: "/x", body: "{", want: http.StatusBadRequest},
		{name: "batch while disabled", handler: ReplicationReplicate, method: http.MethodPost, target: "/x", body: `{"index":"a"}`, want: http.StatusInternalServerError},
		{name: "recover while disabled", handler: ReplicationRecover, method: http.MethodPost, target: "/x?index=a", want: http.StatusBadRequest},
		{name: "bad seq", handler: ReplicationSnapshot, method: http.MethodGet, target: "/x?index=a&seq=x", want: http.StatusBadRequest},
		{name: "snapshot while disabled", handler: ReplicationSnapshot, method: http.MethodGet, target: "/x?index=a", want: http.StatusServiceUnavailable},
		{name: "bad snapshot request", handler: ReplicationSnapshot, method: http.MethodPost, target: "/x", body: "{", want: http.StatusBadRequest},
		{name: "snapshot request while disabled", handler: ReplicationSnapshot, method: http.MethodPost, target: "/x", body: `{"index":"a","files":[{"path":"s/1.seg","size":1,"sum":"x"}]}`, want: http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c := zhttp.NewHTTPContext(w, httptest.NewRequest(tt.method, tt.target, strings.NewReader(tt.body)))
			tt.handler(c)
			assert.Equal(t, tt.want, w.Code)
		})
	}
}

// brokenWriter is a client that hangs up once the status line is out.
type brokenWriter struct{ *httptest.ResponseRecorder }

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestReplicationHandlersEnabled(t *testing.T) {
	local, remote := "TestReplicationHandlersEnabled.local", "TestReplicationHandlersEnabled.remote"
	index, err := core.NewIndex(local, "disk", 1)
	require.NoError(t, err)
	require.NoError(t, core.StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(local)) })
	startTestCluster(t, map[string]string{local: "node-a", remote: "node-b"})
	config.Global.Cluster.Replication = replication.ModeDocument
	config.Global.Cluster.Secret = "s3cret"
	config.Global.Cluster.ReplicationInterval = time.Hour
	require.NoError(t, replication.Start(context.Background()))
	t.Cleanup(replication.Stop)

	serve := func(w http.ResponseWriter, handler zhttp.HandlerFunc, method, target, body string) {
		handler(zhttp.NewHTTPContext(w, httptest.NewRequest(method, target, strings.NewReader(body))))
	}

	tests := []struct {
		name    string
		handler zhttp.HandlerFunc
		method  string
		target  string
		body    string
		want    int
		result  string
	}{
		{name: "batch for an owned index", handler: ReplicationReplicate, method: http.MethodPost, target: "/x", body: `{"index":"` + local + `"}`, want: http.StatusInternalServerError, result: "this node owns"},
		{name: "batch before the replica recovered", handler: ReplicationReplicate, method: http.MethodPost, target: "/x", body: `{"index":"` + remote + `","term":"t","from":1}`, want: http.StatusConflict, result: `"seq":0`},
		{name: "recover an owned index", handler: ReplicationRecover, method: http.MethodPost, target: "/x?index=" + local, want: http.StatusBadRequest, result: "this node owns"},
		{name: "recover a remote index", handler: ReplicationRecover, method: http.MethodPost, target: "/x?index=" + remote, want: http.StatusOK, result: `{"recovering":true}`},
		{name: "snapshot of a foreign index", handler: ReplicationSnapshot, method: http.MethodGet, target: "/x?index=" + remote, want: http.StatusServiceUnavailable, result: "not owned"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			serve(w, tt.handler, tt.method, tt.target, tt.body)
			assert.Equal(t, tt.want, w.Code)
			assert.Contains(t, w.Body.String(), tt.result)
		})
	}

	var info replication.SnapshotInfo
	t.Run("snapshot streams a tar", func(t *testing.T) {
		w := httptest.NewRecorder()
		serve(w, ReplicationSnapshot, http.MethodGet, "/x?index="+local, "")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/x-tar", w.Header().Get("Content-Type"))

		header, err := tar.NewReader(w.Body).Next()
		require.NoError(t, err)
		require.Positive(t, header.Size)
		data, err := io.ReadAll(io.LimitReader(w.Body, header.Size))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &info))
		assert.NotEmpty(t, info.Term)
		assert.NotEmpty(t, info.Index)
	})
	t.Run("snapshot at the replica's checkpoint", func(t *testing.T) {
		w := httptest.NewRecorder()
		target := "/x?index=" + local + "&term=" + info.Term + "&seq=" + strconv.FormatUint(info.Seq, 10)
		serve(w, ReplicationSnapshot, http.MethodGet, target, "")
		assert.Equal(t, http.StatusNotModified, w.Code)
		assert.Empty(t, w.Body.String())
	})
	t.Run("client hangs up mid stream", func(t *testing.T) {
		w := httptest.NewRecorder()
		serve(brokenWriter{w}, ReplicationSnapshot, http.MethodGet, "/x?index="+local, "")
		assert.Equal(t, http.StatusOK, w.Code, "the status is out before the stream breaks")
		assert.Equal(t, "application/x-tar", w.Header().Get("Content-Type"))
		assert.Empty(t, w.Body.String())
	})
}
