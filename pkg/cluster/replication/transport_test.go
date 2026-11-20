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

package replication

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/cluster"
	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// startLocalCluster joins a single node cluster (node-a) advertising addr
// on an in-memory store and waits until node-a owns every partition.
func startLocalCluster(t *testing.T, addr string) {
	t.Helper()
	previous := config.Global.Cluster
	config.Global.Cluster.Name = "replication-test"
	config.Global.Cluster.NodeID = "node-a"
	config.Global.Cluster.AdvertiseAddr = addr
	config.Global.Cluster.Partitions = 4
	config.Global.Cluster.HeartbeatTTL = 15 * time.Second
	config.Global.Cluster.HeartbeatInterval = 100 * time.Millisecond
	require.NoError(t, cluster.StartWith(context.Background(), cluster.NewMemoryStore()))
	t.Cleanup(func() {
		cluster.Stop()
		config.Global.Cluster = previous
	})
	require.Eventually(t, func() bool { return len(cluster.Assignments()) == 4 }, 5*time.Second, 10*time.Millisecond)
}

func TestClusterTopology(t *testing.T) {
	var topo clusterTopology
	assert.Empty(t, topo.LocalID(), "no cluster, no local node")

	startLocalCluster(t, "http://127.0.0.1:1")
	ctx := context.Background()
	name := "TestClusterTopology.index_1"
	assert.Equal(t, "node-a", topo.LocalID())
	owner, ok := topo.Owner(name)
	assert.True(t, ok)
	assert.Equal(t, "node-a", owner)
	assert.True(t, topo.IsOwner(name))
	assert.False(t, topo.IsReplica(name))
	assert.Empty(t, topo.Replicas(name))
	assert.Positive(t, topo.Epoch(name))

	require.NoError(t, topo.UpdateInSync(ctx, name, []string{"node-b"}, topo.Epoch(name)))
	insync, err := topo.InSync(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"node-b"}, insync[name])
}

func TestStartWithCluster(t *testing.T) {
	startLocalCluster(t, "http://127.0.0.1:1")
	previous := config.Global.Cluster
	t.Cleanup(func() {
		Stop()
		config.Global.Cluster = previous
	})
	ctx := context.Background()

	config.Global.Cluster.Replication = "bogus"
	assert.Error(t, Start(ctx))

	config.Global.Cluster.Replication = ModeNone
	require.NoError(t, Start(ctx))
	assert.False(t, Enabled())

	config.Global.Cluster.Replication = ModeSegment
	config.Global.Cluster.Secret = ""
	assert.Error(t, Start(ctx), "a secret is required")

	config.Global.Cluster.Secret = "s3cret"
	config.Global.Cluster.ReplicationInterval = 0
	require.NoError(t, Start(ctx))
	assert.Equal(t, ModeSegment, Mode())
	assert.Equal(t, 10*time.Second, get().interval)
	Stop()

	// the background loop ticks
	config.Global.Cluster.ReplicationInterval = 5 * time.Millisecond
	require.NoError(t, Start(ctx))
	time.Sleep(50 * time.Millisecond)
	Stop()
}

// A node that redirects never gets the secret sent elsewhere.
func TestHTTPTransportRefusesRedirects(t *testing.T) {
	trap := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the redirect was followed")
	}))
	t.Cleanup(trap.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, trap.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	startLocalCluster(t, server.URL)

	err := NewHTTPTransport("s3cret").Recover(context.Background(), "node-a", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "307")
}

// The HTTP transport talks to the internal endpoints of the node it
// resolves through the cluster; here node-a is this test server.
func TestHTTPTransport(t *testing.T) {
	const secret = "s3cret"
	var replicateStatus int
	var replicateBody string
	var recoverStatus, snapshotStatus int
	snapshotSrc := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(snapshotSrc, "1.seg"), []byte("segment"), 0o640))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !CheckSecret(r.Header.Get(SecretHeader), secret) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case ReplicatePath:
			var batch Batch
			data, err := io.ReadAll(r.Body)
			if err == nil {
				err = json.Unmarshal(data, &batch)
			}
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(replicateStatus)
			_, _ = io.WriteString(w, replicateBody)
		case RecoverPath:
			w.WriteHeader(recoverStatus)
			_, _ = io.WriteString(w, r.URL.Query().Get("index"))
		case SnapshotPath:
			w.WriteHeader(snapshotStatus)
			if snapshotStatus == http.StatusOK {
				info := &SnapshotInfo{Checkpoint: Checkpoint{Term: "t", Seq: 2}, Index: json.RawMessage(`{}`)}
				_ = WriteSnapshot(w, info, snapshotSrc)
			}
		}
	}))
	t.Cleanup(server.Close)
	startLocalCluster(t, server.URL)

	transport := NewHTTPTransport(secret)
	ctx := context.Background()
	batch := &Batch{Index: "x", Term: "t", From: 1}

	t.Run("replicate", func(t *testing.T) {
		tests := []struct {
			name    string
			status  int
			body    string
			wantErr error
			wantAck Ack
			fails   bool
		}{
			{name: "ok", status: http.StatusOK, body: `{"term":"t","seq":3}`, wantAck: Ack{Term: "t", Seq: 3}},
			{name: "conflict", status: http.StatusConflict, body: `{"term":"t","seq":1}`, wantErr: ErrOutOfSync, wantAck: Ack{Term: "t", Seq: 1}},
			{name: "bad body", status: http.StatusOK, body: `{`, fails: true},
			{name: "server error", status: http.StatusInternalServerError, body: "boom", fails: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				replicateStatus, replicateBody = tt.status, tt.body
				ack, err := transport.Replicate(ctx, "node-a", batch)
				switch {
				case tt.wantErr != nil:
					assert.ErrorIs(t, err, tt.wantErr)
				case tt.fails:
					assert.Error(t, err)
				default:
					assert.NoError(t, err)
				}
				assert.Equal(t, tt.wantAck, ack)
			})
		}
		_, err := transport.Replicate(ctx, "node-a", &Batch{Mappings: json.RawMessage(`{`)})
		assert.Error(t, err, "a batch that cannot be encoded")
		_, err = transport.Replicate(ctx, "node-x", batch)
		assert.Error(t, err, "unknown node")
		_, err = NewHTTPTransport("wrong").Replicate(ctx, "node-a", batch)
		assert.ErrorContains(t, err, "401")
	})

	t.Run("recover", func(t *testing.T) {
		recoverStatus = http.StatusOK
		assert.NoError(t, transport.Recover(ctx, "node-a", "x"))
		recoverStatus = http.StatusConflict
		assert.ErrorContains(t, transport.Recover(ctx, "node-a", "x y"), "x y")
		assert.Error(t, transport.Recover(ctx, "node-x", "x"))
	})

	t.Run("snapshot", func(t *testing.T) {
		req := &SnapshotRequest{Index: "x"}
		snapshotStatus = http.StatusOK
		dir := t.TempDir()
		info, err := transport.Snapshot(ctx, "node-a", req, dir)
		require.NoError(t, err)
		assert.Equal(t, uint64(2), info.Seq)
		data, err := os.ReadFile(filepath.Join(dir, "1.seg"))
		require.NoError(t, err)
		assert.Equal(t, "segment", string(data))

		snapshotStatus = http.StatusNotModified
		_, err = transport.Snapshot(ctx, "node-a", req, dir)
		assert.ErrorIs(t, err, ErrNotModified)
		snapshotStatus = http.StatusInternalServerError
		_, err = transport.Snapshot(ctx, "node-a", req, dir)
		assert.ErrorContains(t, err, "500")
		_, err = transport.Snapshot(ctx, "node-x", req, dir)
		assert.Error(t, err)
	})

	t.Run("bad request", func(t *testing.T) {
		_, err := transport.do(ctx, "BAD METHOD", "node-a", "/", nil)
		assert.Error(t, err)
	})
}

// failWriter fails every write after limit bytes.
type failWriter struct{ limit int }

func (f *failWriter) Write(p []byte) (int, error) {
	if len(p) > f.limit {
		n := f.limit
		f.limit = 0
		return n, errors.New("disk full")
	}
	f.limit -= len(p)
	return len(p), nil
}

func TestWriteSnapshotErrors(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "1.seg"), bytes.Repeat([]byte("s"), 4096), 0o640))
	info := &SnapshotInfo{Index: json.RawMessage(`{}`)}

	assert.Error(t, WriteSnapshot(&failWriter{}, info, src), "the metadata header fails")
	assert.Error(t, WriteSnapshot(&failWriter{limit: 2048}, info, src), "a file fails")
	assert.Error(t, WriteSnapshot(io.Discard, &SnapshotInfo{Index: json.RawMessage(`{`)}, src), "metadata that cannot be encoded")
	assert.Error(t, WriteSnapshot(io.Discard, info, filepath.Join(src, "missing")), "a missing directory")

	locked := filepath.Join(src, "locked.seg")
	require.NoError(t, os.WriteFile(locked, []byte("x"), 0o000))
	t.Cleanup(func() { assert.NoError(t, os.Chmod(locked, 0o640)) })
	if f, err := os.Open(locked); err == nil {
		require.NoError(t, f.Close())
		t.Skip("permissions are not enforced")
	}
	assert.Error(t, WriteSnapshot(io.Discard, info, src), "an unreadable file")
}

func tarOf(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, data := range entries {
		require.NoError(t, writeTarFile(tw, name, int64(len(data)), bytes.NewReader([]byte(data))))
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

func TestReadSnapshotErrors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "not a tar", data: bytes.Repeat([]byte("x"), 1024)},
		{name: "bad metadata", data: tarOf(t, map[string]string{snapshotMetaName: "{"})},
		{name: "escaping entry", data: tarOf(t, map[string]string{snapshotMetaName: "{}", "../escape.seg": "x"}), want: "escapes the target"},
		{name: "unwritable entry", data: tarOf(t, map[string]string{snapshotMetaName: "{}", "sub": "x", "sub/1.seg": "x"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ReadSnapshot(bytes.NewReader(tt.data), t.TempDir())
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "without metadata")
			assert.ErrorContains(t, err, tt.want)
		})
	}

	t.Run("truncated metadata", func(t *testing.T) {
		data := tarOf(t, map[string]string{snapshotMetaName: string(bytes.Repeat([]byte("x"), 2048))})
		_, err := ReadSnapshot(bytes.NewReader(data[:1024]), t.TempDir())
		assert.Error(t, err)
	})
}

func TestWriteFileErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o640))
	assert.Error(t, writeFile(filepath.Join(file, "child"), bytes.NewReader(nil)), "parent is a file")
	assert.Error(t, writeFile(dir, bytes.NewReader(nil)), "target is a directory")
}
