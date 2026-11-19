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
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// skipAsRoot skips tests that rely on file permissions.
func skipAsRoot(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions are not enforced on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root, permissions are not enforced")
	}
}

// lockDir makes dir unusable for the test.
func lockDir(t *testing.T, dir string) {
	t.Helper()
	skipAsRoot(t)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() { assert.NoError(t, os.Chmod(dir, 0o750)) })
}

func TestApplyErrors(t *testing.T) {
	name := "TestApplyErrors.index_1"
	newDiskIndex(t, name)
	topo := newFakeTopology("node-b")
	topo.set(name, "node-a", "node-b")
	tests := []struct {
		name  string
		batch *Batch
	}{
		{name: "broken mappings", batch: &Batch{Index: name, Term: "t1", From: 1, Ops: []Op{{Seq: 1}}, Mappings: json.RawMessage(`{`)}},
		{name: "unknown shard", batch: &Batch{Index: name, Term: "t1", From: 1, Ops: []Op{{Seq: 1, Shard: "nope", Entry: json.RawMessage(testEntry)}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newManager(t, ModeDocument, topo, newLoopTransport())
			r := m.replicaFor(name)
			r.term, r.ready = "t1", true
			_, err := m.apply(tt.batch)
			assert.Error(t, err)
			assert.Zero(t, r.seq, "a failed batch is not acknowledged")
		})
	}

	index, ok := core.GetIndex(name)
	require.True(t, ok)
	assert.Error(t, applyOps(index, []Op{{Shard: "nope"}}))
	assert.Error(t, (&replica{pending: []Op{{Seq: 1}}}).replayPending("TestApplyErrors.missing"))
	assert.Zero(t, lastSeq(nil))
}

func TestStateErrors(t *testing.T) {
	name := "TestStateErrors.index_1"
	newDiskIndex(t, name)
	require.NoError(t, os.MkdirAll(indexDir(name), 0o750))
	require.NoError(t, removeState(name))
	topo := newFakeTopology("node-b")
	topo.set(name, "node-a", "node-b")
	m := newManager(t, ModeDocument, topo, newLoopTransport())

	t.Run("unwritable record", func(t *testing.T) {
		tmp := statePath(name) + ".tmp"
		require.NoError(t, os.MkdirAll(tmp, 0o750))
		t.Cleanup(func() { assert.NoError(t, os.RemoveAll(tmp)) })
		r := &replica{term: "t1", seq: 2, ready: true}
		m.persist(name, r)
		assert.Equal(t, replicaState{}, r.saved, "nothing recorded")
	})

	t.Run("unreadable and unremovable record", func(t *testing.T) {
		require.NoError(t, os.MkdirAll(filepath.Join(statePath(name), "x"), 0o750))
		t.Cleanup(func() { assert.NoError(t, os.RemoveAll(statePath(name))) })
		_, ok := loadState(name, ModeDocument)
		assert.False(t, ok)
		assert.Error(t, removeState(name))
	})

	t.Run("index gone", func(t *testing.T) {
		r := &replica{term: "t1", seq: 2, ready: true}
		assert.NoError(t, r.persist("TestStateErrors.missing", ModeDocument))
		assert.Equal(t, replicaState{}, r.saved)
	})
}

func TestStartRecoveryRunsOnce(t *testing.T) {
	name := "TestStartRecoveryRunsOnce.index_1"
	topo := newFakeTopology("node-b")
	topo.set(name, "node-a", "node-b")
	calls := 0
	transport := &funcTransport{snapshot: func(string, *SnapshotRequest, string) (*SnapshotInfo, error) {
		calls++
		return nil, errors.New("owner unreachable")
	}}
	m := newManager(t, ModeDocument, topo, transport)
	useDataPath(t, t.TempDir())
	r := m.replicaFor(name)
	r.recovering = true
	m.startRecovery(name)
	m.wg.Wait()
	assert.Zero(t, calls, "a running recovery is not started again")

	r.recovering = false
	m.startRecovery(name)
	m.wg.Wait()
	assert.Equal(t, 1, calls)
	assert.False(t, r.isRecovering())
}

// recover fails cleanly at every step, leaving the replica as it was.
func TestRecoverErrors(t *testing.T) {
	name := "TestRecoverErrors.index_1"
	topo := newFakeTopology("node-b")
	topo.set(name, "node-a", "node-b")
	snapshot := func(string) (*SnapshotInfo, error) { return nil, errors.New("no snapshot expected") }
	transport := &funcTransport{snapshot: func(_ string, _ *SnapshotRequest, dir string) (*SnapshotInfo, error) {
		return snapshot(dir)
	}}
	m := newManager(t, ModeSegment, topo, transport)

	t.Run("no remote owner", func(t *testing.T) {
		assert.Error(t, m.recover("TestRecoverErrors.unassigned", new(replica), Checkpoint{}))
		other := newFakeTopology("node-b")
		other.set(name, "node-b")
		owned := newManager(t, ModeSegment, other, transport)
		assert.Error(t, owned.recover(name, new(replica), Checkpoint{}))
	})

	t.Run("staging not creatable", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, nil, 0o640))
		useDataPath(t, file)
		assert.Error(t, m.recover(name, new(replica), Checkpoint{}))
	})

	t.Run("staging not writable", func(t *testing.T) {
		data := t.TempDir()
		useDataPath(t, data)
		lockDir(t, filepath.Join(data, "_replication"))
		assert.Error(t, m.recover(name, new(replica), Checkpoint{}))
	})

	t.Run("local copy not readable", func(t *testing.T) {
		data := t.TempDir()
		useDataPath(t, data)
		lockDir(t, filepath.Join(data, name, "s"))
		assert.Error(t, m.recover(name, new(replica), Checkpoint{}))
	})

	useDataPath(t, t.TempDir())
	tests := []struct {
		name     string
		snapshot func(dir string) (*SnapshotInfo, error)
	}{
		{name: "owner unreachable", snapshot: func(string) (*SnapshotInfo, error) { return nil, errors.New("down") }},
		{name: "reused file escapes", snapshot: func(string) (*SnapshotInfo, error) {
			return &SnapshotInfo{Reused: []string{"../x.seg"}}, nil
		}},
		{name: "reused file not held", snapshot: func(string) (*SnapshotInfo, error) {
			return &SnapshotInfo{Reused: []string{"s/1.seg"}}, nil
		}},
		{name: "reused file blocked", snapshot: func(dir string) (*SnapshotInfo, error) {
			if err := writeFile(filepath.Join(dir, "s"), bytes.NewReader(nil)); err != nil {
				return nil, err
			}
			return &SnapshotInfo{Reused: []string{"s/1.seg"}}, nil
		}},
		{name: "metadata of another index", snapshot: func(string) (*SnapshotInfo, error) {
			return &SnapshotInfo{Index: json.RawMessage(`{"name":"other"}`)}, nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot = tt.snapshot
			r := new(replica)
			assert.Error(t, m.recover(name, r, Checkpoint{}))
			assert.False(t, r.isReady())
		})
	}
}

// Operations a segment replica kept beyond the copy it installs stay
// pending, older ones are dropped with the files that hold them.
func TestRecoverKeepsNewerOperations(t *testing.T) {
	name := "TestRecoverKeepsNewerOperations.index_1"
	env := setupTwoNodes(t, ModeSegment, name)
	ctx := context.Background()
	require.NoError(t, env.source.CreateDocument("1", map[string]interface{}{"name": "one"}, false))
	waitDocs(t, env.source, 1)
	activate(t, env.owner)
	env.transport.snapshot = func(_ string, req *SnapshotRequest, dir string) (*SnapshotInfo, error) {
		var buf bytes.Buffer
		if err := ServeSnapshot(ctx, req, func() io.Writer { return &buf }); err != nil {
			return nil, err
		}
		return ReadSnapshot(&buf, dir)
	}
	p, err := env.owner.prepare(ctx, name)
	require.NoError(t, err)
	term, seq := p.checkpoint()

	r := env.replica.replicaFor(name)
	r.term = term
	r.pending = []Op{{Seq: seq}, {Seq: seq + 1}, {Seq: seq + 2}}
	require.NoError(t, env.replica.recover(name, r, Checkpoint{}))
	assert.Equal(t, []Op{{Seq: seq + 1}, {Seq: seq + 2}}, r.pending)
	assert.Equal(t, seq+2, r.seq)
	assert.Equal(t, seq, r.filesSeq)
}

func TestServeSnapshotErrors(t *testing.T) {
	ctx := context.Background()
	write := func() io.Writer { return io.Discard }
	name := "TestServeSnapshotErrors.index_1"
	remote := "TestServeSnapshotErrors.index_2"
	newDiskIndex(t, name)
	useObjectStore(t)
	newRemoteIndex(t, remote)
	topo := newFakeTopology("node-a")
	topo.set(name, "node-a", "node-b")
	topo.set(remote, "node-a")
	m := newManager(t, ModeSegment, topo, newLoopTransport())

	assert.Error(t, ServeSnapshot(ctx, &SnapshotRequest{Index: name}, write), "not enabled")
	activate(t, m)
	assert.Error(t, ServeSnapshot(ctx, &SnapshotRequest{Index: "TestServeSnapshotErrors.foreign"}, write))
	assert.ErrorContains(t, ServeSnapshot(ctx, &SnapshotRequest{Index: remote}, write), "object storage")
	_, err := m.prepare(ctx, name) // promoted before the data path breaks
	require.NoError(t, err)

	t.Run("staging not creatable", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, nil, 0o640))
		useDataPath(t, file)
		assert.Error(t, ServeSnapshot(ctx, &SnapshotRequest{Index: name}, write))
	})

	t.Run("staging not writable", func(t *testing.T) {
		data := t.TempDir()
		useDataPath(t, data)
		lockDir(t, filepath.Join(data, "_replication"))
		assert.Error(t, ServeSnapshot(ctx, &SnapshotRequest{Index: name}, write))
	})
}

func TestSumErrors(t *testing.T) {
	tests := []struct {
		name string
		sum  func(dir string) error
	}{
		{name: "a directory has no content sum", sum: func(dir string) error {
			var c sumCache
			_, _, err := c.sum(dir, dir)
			return err
		}},
		{name: "missing file", sum: func(dir string) error {
			_, err := fileSum(filepath.Join(dir, "missing.seg"))
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Error(t, tt.sum(t.TempDir()))
		})
	}
}

func TestStageSegmentsErrors(t *testing.T) {
	name := "TestStageSegmentsErrors.index_1"
	data := t.TempDir()
	useDataPath(t, data)
	require.NoError(t, os.MkdirAll(filepath.Join(data, name, "s"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(data, name, "s", "1.seg"), []byte("segment"), 0o640))
	var c sumCache

	staged := filepath.Join(data, "staged")
	files, err := stageSegments(name, staged, &c)
	require.NoError(t, err)
	assert.Len(t, files, 1)
	files, err = stageSegments(name, staged, &c)
	require.NoError(t, err)
	assert.Empty(t, files, "a file that cannot be linked is copied again")

	blocker := filepath.Join(data, "blocker")
	require.NoError(t, os.WriteFile(blocker, nil, 0o640))
	_, err = stageSegments(name, blocker, &c)
	assert.Error(t, err, "the staging directory cannot be created")

	lockDir(t, filepath.Join(data, name, "s"))
	_, err = stageSegments(name, staged, &c)
	assert.Error(t, err, "the local copy cannot be read")
}
