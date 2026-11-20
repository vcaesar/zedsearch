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
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/bluge/directory"
	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

const testEntry = `{"@_id":"1","@_action":"update","@_shard":-2}`

// useObjectStore points the s3 backend at an in-memory store, which is
// also the cold storage type, for the test.
func useObjectStore(t *testing.T) *directory.MemoryObjectStore {
	t.Helper()
	store := directory.NewMemoryObjectStore()
	restore, err := directory.SetTestObjectStore(core.StorageTypeS3, store)
	require.NoError(t, err)
	oldBucket, oldCold := config.Global.S3.Bucket, config.Global.Tier.ColdStorageType
	config.Global.S3.Bucket = "zed-test"
	config.Global.Tier.ColdStorageType = core.StorageTypeS3
	t.Cleanup(func() {
		config.Global.S3.Bucket, config.Global.Tier.ColdStorageType = oldBucket, oldCold
		restore()
	})
	return store
}

// newColdIndex creates a disk index and moves it to the cold tier.
func newColdIndex(t *testing.T, name string) *core.Index {
	t.Helper()
	useObjectStore(t)
	newDiskIndex(t, name)
	require.NoError(t, core.MoveIndexTier(name, core.TierCold))
	index, ok := core.GetIndex(name)
	require.True(t, ok)
	require.True(t, index.IsCold())
	return index
}

// newRemoteIndex creates and registers an object-storage index.
func newRemoteIndex(t *testing.T, name string) *core.Index {
	t.Helper()
	index, err := core.NewIndex(name, core.StorageTypeS3, 1)
	require.NoError(t, err)
	require.NoError(t, core.StoreIndex(index))
	core.ZED_INDEX_LIST.Add(index)
	t.Cleanup(func() { core.ZED_INDEX_LIST.Delete(name) })
	return index
}

func TestRecordSkipsForeignAndReadOnlyIndexes(t *testing.T) {
	cold := "TestRecordSkipsForeignAndReadOnlyIndexes.index_1"
	newColdIndex(t, cold)
	topo := newFakeTopology("node-a")
	topo.set(cold, "node-a")
	topo.set("TestRecordSkipsForeignAndReadOnlyIndexes.missing", "node-a")
	m := newManager(t, ModeDocument, topo, newLoopTransport())

	m.record("TestRecordSkipsForeignAndReadOnlyIndexes.foreign", "0", []byte(testEntry))
	m.record("TestRecordSkipsForeignAndReadOnlyIndexes.missing", "0", []byte(testEntry))
	m.record(cold, "0", []byte(testEntry))
	assert.Empty(t, m.primaries)

	_, err := m.prepare(context.Background(), cold)
	assert.ErrorContains(t, err, "read-only")

	remote := &primary{remote: true, seq: 2, uploaded: 1}
	assert.True(t, remote.dirty())
	remote.uploaded = 2
	assert.False(t, remote.dirty())
}

func TestPrepareErrors(t *testing.T) {
	name := "TestPrepareErrors.index_1"
	index := newDiskIndex(t, name)
	topo := newFakeTopology("node-a")
	topo.set(name, "node-a", "node-b")
	topo.set("TestPrepareErrors.missing", "node-a")
	m := newManager(t, ModeDocument, topo, newLoopTransport())
	ctx := context.Background()

	_, err := m.prepare(ctx, "TestPrepareErrors.foreign")
	assert.ErrorContains(t, err, "not owned")
	_, err = m.prepare(ctx, "TestPrepareErrors.missing")
	assert.Error(t, err, "no index metadata")

	t.Run("in-sync set unreadable", func(t *testing.T) {
		topo.listErr = errors.New("etcd down")
		_, err := m.prepare(ctx, name)
		topo.listErr = nil
		assert.Error(t, err)
		assert.NotContains(t, m.primaries, name, "a failed promotion is retried")
	})

	t.Run("in-sync set not writable", func(t *testing.T) {
		topo.insyncErr = errors.New("etcd down")
		_, err := m.prepare(ctx, name)
		topo.insyncErr = nil
		assert.Error(t, err)
	})

	t.Run("state not removable", func(t *testing.T) {
		require.NoError(t, os.MkdirAll(filepath.Join(statePath(name), "x"), 0o750))
		t.Cleanup(func() { assert.NoError(t, os.RemoveAll(statePath(name))) })
		_, err := m.prepare(ctx, name)
		assert.Error(t, err)
	})

	t.Run("kept operations not replayable", func(t *testing.T) {
		m.replicas[name] = &replica{pending: []Op{{Seq: 1, Shard: "nope", Entry: json.RawMessage(testEntry)}}}
		_, err := m.prepare(ctx, name)
		assert.Error(t, err)
	})

	p, err := m.prepare(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, index.GetName(), p.index)
}

func TestCommitMappingsOfDeletedIndex(t *testing.T) {
	name := "TestCommitMappingsOfDeletedIndex.index_1"
	index := newDiskIndex(t, name)
	topo := newFakeTopology("node-a")
	topo.set(name, "node-a", "node-b")
	m := newManager(t, ModeDocument, topo, newLoopTransport())
	p, err := m.prepare(context.Background(), name)
	require.NoError(t, err)

	core.ZED_INDEX_LIST.Delete(name)
	assert.NoError(t, m.commit(context.Background(), p, false), "nothing to ship, the mappings are not encoded")
	assert.Error(t, m.commit(context.Background(), p, true), "a probe may have to resend operations")
	m.record(name, "0", []byte(testEntry))
	assert.Error(t, m.commit(context.Background(), p, false))
	_, err = m.mappings(name)
	assert.Error(t, err)
	core.ZED_INDEX_LIST.Add(index)
}

// ship walks a replica forward through every answer it may give.
func TestShip(t *testing.T) {
	name := "TestShip.index_1"
	newDiskIndex(t, name)
	topo := newFakeTopology("node-a")
	topo.set(name, "node-a", "node-b")

	tests := []struct {
		name      string
		trimmed   bool // the owner dropped its retained operations
		insync    bool // the replica is in the in-sync set
		answers   func(p *primary) []func(*Batch) (Ack, error)
		recoverOK bool
		want      bool
		recovered bool
	}{
		{
			name:   "in-sync replica is sent the log at once",
			insync: true,
			answers: func(p *primary) []func(*Batch) (Ack, error) {
				return []func(*Batch) (Ack, error){func(b *Batch) (Ack, error) {
					if b.From != 1 || len(b.Ops) != 3 {
						return Ack{}, errors.New("unexpected batch")
					}
					return Ack{Term: p.term, Seq: 3}, nil
				}}
			},
			want: true,
		},
		{
			name: "replica not in sync is probed before the log is sent",
			answers: func(p *primary) []func(*Batch) (Ack, error) {
				return []func(*Batch) (Ack, error){
					func(b *Batch) (Ack, error) {
						if b.From != 4 || len(b.Ops) != 0 {
							return Ack{}, errors.New("unexpected batch")
						}
						return Ack{Term: p.term, Seq: 1}, ErrOutOfSync
					},
					func(b *Batch) (Ack, error) {
						if b.From != 2 || len(b.Ops) != 2 {
							return Ack{}, errors.New("unexpected batch")
						}
						return Ack{Term: p.term, Seq: 3}, nil
					},
				}
			},
			want: true,
		},
		{
			name:    "trimmed log probes the replica",
			trimmed: true,
			answers: func(p *primary) []func(*Batch) (Ack, error) {
				return []func(*Batch) (Ack, error){func(b *Batch) (Ack, error) {
					if b.From != 4 || len(b.Ops) != 0 {
						return Ack{}, errors.New("unexpected batch")
					}
					return Ack{Term: p.term, Seq: 3}, nil
				}}
			},
			want: true,
		},
		{
			name: "partial acks continue",
			answers: func(p *primary) []func(*Batch) (Ack, error) {
				return []func(*Batch) (Ack, error){
					func(*Batch) (Ack, error) { return Ack{Term: p.term, Seq: 1}, nil },
					func(b *Batch) (Ack, error) {
						if b.From != 2 {
							return Ack{}, errors.New("unexpected batch")
						}
						return Ack{Term: p.term, Seq: 3}, nil
					},
				}
			},
			want: true,
		},
		{
			name: "missing operations are resent",
			answers: func(p *primary) []func(*Batch) (Ack, error) {
				return []func(*Batch) (Ack, error){
					func(*Batch) (Ack, error) { return Ack{Term: p.term, Seq: 1}, ErrOutOfSync },
					func(b *Batch) (Ack, error) {
						if b.From != 2 {
							return Ack{}, errors.New("unexpected batch")
						}
						return Ack{Term: p.term, Seq: 3}, nil
					},
				}
			},
			want: true,
		},
		{
			name:    "missing operations already trimmed recover",
			trimmed: true,
			answers: func(p *primary) []func(*Batch) (Ack, error) {
				return []func(*Batch) (Ack, error){
					func(*Batch) (Ack, error) { return Ack{Term: p.term, Seq: 1}, ErrOutOfSync },
				}
			},
			recovered: true,
		},
		{
			name: "recovering replica",
			answers: func(p *primary) []func(*Batch) (Ack, error) {
				return []func(*Batch) (Ack, error){
					func(*Batch) (Ack, error) { return Ack{Term: p.term, Recovering: true}, ErrOutOfSync },
				}
			},
			recoverOK: true,
			recovered: true,
		},
		{
			name: "unreachable replica",
			answers: func(*primary) []func(*Batch) (Ack, error) {
				return []func(*Batch) (Ack, error){
					func(*Batch) (Ack, error) { return Ack{}, errors.New("connection refused") },
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var answers []func(*Batch) (Ack, error)
			recovered := false
			transport := &funcTransport{
				replicate: func(_ string, b *Batch) (Ack, error) {
					mu.Lock()
					defer mu.Unlock()
					if len(answers) == 0 {
						return Ack{}, errors.New("no more answers")
					}
					next := answers[0]
					answers = answers[1:]
					return next(b)
				},
				recover: func(string, string) error {
					recovered = true
					if tt.recoverOK {
						return nil
					}
					return errors.New("recover refused")
				},
			}
			m := newManager(t, ModeDocument, topo, transport)
			p, err := m.prepare(context.Background(), name)
			require.NoError(t, err)
			for range 3 {
				m.record(name, "0", []byte(testEntry))
			}
			if tt.trimmed {
				p.ops = nil
			}
			if tt.insync {
				p.insync = []string{"node-b"}
			}
			answers = tt.answers(p)
			assert.Equal(t, tt.want, m.ship(context.Background(), p, "node-b", 3, nil, false))
			assert.Equal(t, tt.recovered, recovered)
			if tt.want {
				mu.Lock()
				assert.Empty(t, answers, "every scripted answer is consumed")
				mu.Unlock()
				p.mu.Lock()
				assert.Equal(t, uint64(3), p.acked["node-b"])
				p.mu.Unlock()
			}
		})
	}
}

// A replica joining the in-sync set is not worth failing a write for when
// the set cannot be recorded.
func TestCommitToleratesUnrecordedJoin(t *testing.T) {
	name := "TestCommitToleratesUnrecordedJoin.index_1"
	newDiskIndex(t, name)
	topo := newFakeTopology("node-a")
	topo.set(name, "node-a", "node-b")
	transport := &funcTransport{replicate: func(string, *Batch) (Ack, error) { return Ack{Seq: 1}, nil }}
	m := newManager(t, ModeDocument, topo, transport)
	p, err := m.prepare(context.Background(), name)
	require.NoError(t, err)
	m.record(name, "0", []byte(testEntry))

	topo.insyncErr = errors.New("etcd down")
	require.NoError(t, m.commit(context.Background(), p, false))
	assert.Empty(t, p.insync)
}

// A bulk ships what it wrote to a replica that is not in sync yet: a new
// index is replicated from its first bulk, not from the next probe.
func TestCommitAllShipsToReplicaNotInSync(t *testing.T) {
	name := "TestCommitAllShipsToReplicaNotInSync.index_1"
	newDiskIndex(t, name)
	topo := newFakeTopology("node-a")
	topo.set(name, "node-a", "node-b")
	var mu sync.Mutex
	var seq uint64 // what the replica holds
	calls := 0
	transport := &funcTransport{replicate: func(_ string, b *Batch) (Ack, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if b.From > seq+1 {
			return Ack{Term: b.Term, Seq: seq}, ErrOutOfSync
		}
		seq = max(seq, b.From-1+uint64(len(b.Ops)))
		return Ack{Term: b.Term, Seq: seq}, nil
	}}
	m := newManager(t, ModeDocument, topo, transport)
	activate(t, m)
	ctx := context.Background()
	p, err := m.prepare(ctx, name)
	require.NoError(t, err)

	m.record(name, "0", []byte(testEntry))
	require.True(t, p.dirty())
	require.NoError(t, CommitAll(ctx, 0))
	assert.Equal(t, uint64(1), seq)
	insync, _ := topo.inSyncOf(name)
	assert.Equal(t, []string{"node-b"}, insync)

	m.record(name, "0", []byte(testEntry))
	m.record(name, "0", []byte(testEntry))
	require.NoError(t, CommitAll(ctx, 0))
	assert.Equal(t, uint64(3), seq)

	shipped := calls
	assert.False(t, p.dirty())
	require.NoError(t, CommitAll(ctx, 0))
	assert.Equal(t, shipped, calls, "nothing was written since the last commit")
}

// A bulk has no path index to fence: CommitAll must fail the operations of
// a partition that moved, whether it now has another owner or came back
// under a new epoch, instead of acknowledging them.
func TestCommitAllFailsMovedPartition(t *testing.T) {
	tests := []struct {
		name string
		move func(topo *fakeTopology, index string)
	}{
		{name: "owned by another node", move: func(topo *fakeTopology, index string) { topo.set(index, "node-c", "node-b") }},
		{name: "moved away and back", move: func(topo *fakeTopology, _ string) { topo.setEpoch(topo.Epoch("") + 1) }},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := fmt.Sprintf("TestCommitAllFailsMovedPartition.index_%d", i+1)
			newDiskIndex(t, name)
			topo := newFakeTopology("node-a")
			topo.set(name, "node-a", "node-b")
			transport := &funcTransport{replicate: func(_ string, b *Batch) (Ack, error) {
				return Ack{Term: b.Term, Seq: b.From - 1 + uint64(len(b.Ops))}, nil
			}}
			m := newManager(t, ModeDocument, topo, transport)
			activate(t, m)
			ctx := context.Background()
			_, err := m.prepare(ctx, name)
			require.NoError(t, err)

			m.record(name, "0", []byte(testEntry))
			tt.move(topo, name)
			assert.ErrorIs(t, CommitAll(ctx, 0), errStaleEpoch)
		})
	}
}

// A primary that left the map with operations of a running bulk, dropped by
// cleanup or replaced under a new epoch, still fails that bulk; a later bulk
// that wrote nothing to it is not failed by it.
func TestCommitAllFailsRetiredPrimary(t *testing.T) {
	tests := []struct {
		name   string
		retire func(m *manager, topo *fakeTopology, index string)
	}{
		{name: "dropped by cleanup after a move", retire: func(m *manager, topo *fakeTopology, index string) {
			topo.set(index, "node-c", "node-b")
			m.cleanup(context.Background())
		}},
		{name: "replaced under a new epoch", retire: func(m *manager, topo *fakeTopology, index string) {
			topo.setEpoch(topo.Epoch("") + 1)
			require.NotNil(t, m.primaryFor(index))
		}},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := fmt.Sprintf("TestCommitAllFailsRetiredPrimary.index_%d", i+1)
			newDiskIndex(t, name)
			topo := newFakeTopology("node-a")
			topo.set(name, "node-a", "node-b")
			transport := &funcTransport{replicate: func(_ string, b *Batch) (Ack, error) {
				return Ack{Term: b.Term, Seq: b.From - 1 + uint64(len(b.Ops))}, nil
			}}
			m := newManager(t, ModeDocument, topo, transport)
			activate(t, m)
			ctx := context.Background()

			mark, err := PrepareAll(ctx)
			require.NoError(t, err)
			m.record(name, "0", []byte(testEntry))
			tt.retire(m, topo, name)
			assert.ErrorIs(t, CommitAll(ctx, mark), errStaleEpoch)

			later, err := PrepareAll(ctx)
			require.NoError(t, err)
			assert.NoError(t, CommitAll(ctx, later), "the next bulk did not write to it")
		})
	}
}

// The background tick promotes owned indexes, probes their replicas,
// flushes remote ones and checkpoints replicated ones.
func TestTickRoutesIndexes(t *testing.T) {
	store := useObjectStore(t)
	owned := "TestTickRoutesIndexes.index_1"
	replicated := "TestTickRoutesIndexes.index_2"
	remote := "TestTickRoutesIndexes.index_3"
	cold := "TestTickRoutesIndexes.index_4"
	newDiskIndex(t, owned)
	newDiskIndex(t, replicated)
	newRemoteIndex(t, remote)
	newDiskIndex(t, cold)
	require.NoError(t, core.MoveIndexTier(cold, core.TierCold))

	topo := newFakeTopology("node-a")
	topo.set(owned, "node-a", "node-b")
	topo.set(replicated, "node-b", "node-a")
	topo.set(remote, "node-a")
	topo.set(cold, "node-a")
	snapshots := make(chan string, 16)
	transport := &funcTransport{
		replicate: func(string, *Batch) (Ack, error) { return Ack{}, errors.New("connection refused") },
		snapshot: func(_ string, req *SnapshotRequest, _ string) (*SnapshotInfo, error) {
			snapshots <- req.Index
			return nil, errors.New("owner unreachable")
		},
	}
	m := newManager(t, ModeDocument, topo, transport)
	ctx := context.Background()

	m.tick(ctx)
	m.wg.Wait()
	assert.Contains(t, m.primaries, owned)
	assert.Contains(t, m.primaries, remote)
	assert.NotContains(t, m.primaries, cold, "cold indexes take no writes")
	assert.Equal(t, replicated, <-snapshots, "a replica without a copy recovers")

	t.Run("remote flush fails", func(t *testing.T) {
		m.record(remote, "0", []byte(testEntry))
		store.SetErr(errors.New("bucket down"))
		m.tick(ctx)
		store.SetErr(nil)
		m.wg.Wait()
	})

	t.Run("stale replica cannot be recorded", func(t *testing.T) {
		p := m.primaries[owned]
		p.mu.Lock()
		p.insync = []string{"node-b"}
		p.mu.Unlock()
		topo.insyncErr = errors.New("etcd down")
		m.tick(ctx)
		topo.insyncErr = nil
		m.wg.Wait()
		assert.Equal(t, []string{"node-b"}, p.insync)
	})

	t.Run("promotion fails", func(t *testing.T) {
		delete(m.primaries, owned)
		topo.listErr = errors.New("etcd down")
		m.tick(ctx)
		topo.listErr = nil
		m.wg.Wait()
		assert.NotContains(t, m.primaries, owned)
	})
}

// A copy being replaced is not dropped under the recovery.
func TestDropDeletedWaitsForRecovery(t *testing.T) {
	name := "TestDropDeletedWaitsForRecovery.index_1"
	index, err := core.NewIndex(name, core.StorageTypeDisk, 1)
	require.NoError(t, err)
	core.ZED_INDEX_LIST.Add(index) // never stored: the owner deleted it
	t.Cleanup(func() { core.ZED_INDEX_LIST.Delete(name) })

	m := newManager(t, ModeDocument, newFakeTopology("node-b"), newLoopTransport())
	m.replicaFor(name).recovering = true
	m.dropDeleted(name)
	_, ok := core.GetIndex(name)
	assert.True(t, ok)
}

func TestCleanup(t *testing.T) {
	name := "TestCleanup.index_1"
	newDiskIndex(t, name)
	topo := newFakeTopology("node-a")
	topo.set(name, "node-a")
	topo.set("TestCleanup.gone", "node-a")
	m := newManager(t, ModeDocument, topo, newLoopTransport())
	ctx := context.Background()
	require.NoError(t, topo.UpdateInSync(ctx, name, []string{"node-b"}, topo.Epoch(name)))
	require.NoError(t, topo.UpdateInSync(ctx, "TestCleanup.gone", []string{"node-b"}, 7))
	m.primaries["TestCleanup.gone"] = &primary{}
	m.primaries[name] = &primary{}

	topo.listErr = errors.New("etcd down")
	m.cleanup(ctx)
	topo.listErr = nil
	assert.NotContains(t, m.primaries, "TestCleanup.gone")
	assert.Contains(t, m.primaries, name)

	topo.insyncErr = errors.New("etcd down")
	m.cleanup(ctx)
	topo.insyncErr = nil
	_, ok := topo.inSyncOf("TestCleanup.gone")
	assert.True(t, ok, "the entry stays when it cannot be dropped")

	m.cleanup(ctx)
	_, ok = topo.inSyncOf("TestCleanup.gone")
	assert.False(t, ok)
	_, ok = topo.inSyncOf(name)
	assert.True(t, ok, "entries of existing indexes stay")
}

// A bulk over several indexes ships them side by side, and reports every
// index that could not be made durable.
func TestCommitAllShipsIndexesInParallel(t *testing.T) {
	names := []string{"TestCommitAllShipsIndexesInParallel.index_1", "TestCommitAllShipsIndexesInParallel.index_2"}
	topo := newFakeTopology("node-a")
	for _, name := range names {
		newDiskIndex(t, name)
		topo.set(name, "node-a", "node-b")
	}

	var mu sync.Mutex
	arrived := map[string]bool{}
	together := make(chan struct{})
	alone, down := false, false
	transport := &funcTransport{replicate: func(_ string, b *Batch) (Ack, error) {
		mu.Lock()
		arrived[b.Index] = true
		if len(arrived) == len(names) {
			select {
			case <-together:
			default:
				close(together)
			}
		}
		failing := down
		mu.Unlock()
		if failing {
			return Ack{}, errors.New("connection refused")
		}
		// hold the batch until the other index ships too
		select {
		case <-together:
		case <-time.After(5 * time.Second):
			mu.Lock()
			alone = true
			mu.Unlock()
		}
		return Ack{Seq: b.From - 1 + uint64(len(b.Ops))}, nil
	}}
	m := newManager(t, ModeDocument, topo, transport)
	activate(t, m)
	ctx := context.Background()

	_, err := PrepareAll(ctx)
	require.NoError(t, err)
	for _, name := range names {
		// only an index with an in-sync replica behind is committed
		p := m.primaries[name]
		p.mu.Lock()
		p.insync = []string{"node-b"}
		p.mu.Unlock()
		m.record(name, "0", []byte(testEntry))
	}
	require.NoError(t, CommitAll(ctx, 0))
	mu.Lock()
	assert.False(t, alone, "an index waited for another one to finish")
	mu.Unlock()
	for _, name := range names {
		p := m.primaries[name]
		p.mu.Lock()
		assert.Equal(t, uint64(1), p.acked["node-b"], name)
		p.mu.Unlock()
	}

	for _, name := range names {
		m.record(name, "0", []byte(testEntry))
	}
	mu.Lock()
	down = true
	mu.Unlock()
	topo.insyncErr = errors.New("etcd down")
	err = CommitAll(ctx, 0)
	topo.insyncErr = nil
	for _, name := range names {
		assert.ErrorContains(t, err, name)
	}
}

// The exported entry points act on the running manager.
func TestActiveManagerAPI(t *testing.T) {
	owned := "TestActiveManagerAPI.index_1"
	replicated := "TestActiveManagerAPI.index_2"
	newDiskIndex(t, owned)
	newDiskIndex(t, replicated)
	topo := newFakeTopology("node-a")
	topo.set(owned, "node-a", "node-b")
	topo.set(replicated, "node-b", "node-a")
	topo.set("TestActiveManagerAPI.missing", "node-a")
	var mu sync.Mutex
	shipped := 0
	transport := &funcTransport{
		replicate: func(_ string, b *Batch) (Ack, error) {
			mu.Lock()
			defer mu.Unlock()
			shipped += len(b.Ops)
			return Ack{Seq: b.From - 1 + uint64(len(b.Ops))}, nil
		},
		snapshot: func(string, *SnapshotRequest, string) (*SnapshotInfo, error) {
			return nil, errors.New("owner unreachable")
		},
	}
	m := newManager(t, ModeDocument, topo, transport)
	activate(t, m)
	recordWith(t, m)
	ctx := context.Background()

	assert.NoError(t, Prepare(ctx, replicated), "not owned here")
	assert.NoError(t, Prepare(ctx, "TestActiveManagerAPI.missing"), "about to be created")
	require.NoError(t, Prepare(ctx, owned))
	_, prepErr := PrepareAll(ctx)
	require.NoError(t, prepErr)
	assert.NoError(t, Commit(ctx, replicated))

	m.primaries[owned].insync = []string{"node-b"}
	m.record(owned, "0", []byte(testEntry))
	require.NoError(t, Commit(ctx, owned))
	m.record(owned, "0", []byte(testEntry))
	require.NoError(t, CommitAll(ctx, 0))
	mu.Lock()
	assert.Equal(t, 2, shipped)
	mu.Unlock()

	delete(m.primaries, owned)
	topo.listErr = errors.New("etcd down")
	assert.Error(t, Commit(ctx, owned))
	_, prepErr = PrepareAll(ctx)
	assert.Error(t, prepErr)
	topo.listErr = nil

	assert.False(t, Ready(replicated))
	m.replicaFor(replicated).ready = true
	assert.True(t, Ready(replicated))
	_, err := Apply(&Batch{Index: replicated, Term: "other"})
	assert.ErrorIs(t, err, ErrOutOfSync)

	assert.Error(t, RequestRecovery(owned), "an owner never recovers")
	require.NoError(t, RequestRecovery(replicated))
	m.wg.Wait()
	assert.False(t, Ready(replicated), "the copy is replaced")
}

// A replica serves reads of a cold index, the same data on every node, and
// of a disk index only once its copy is installed.
func TestReadable(t *testing.T) {
	const disk, installed, cold = "TestReadable.index_1", "TestReadable.index_2", "TestReadable.index_3"
	newDiskIndex(t, disk)
	newDiskIndex(t, installed)
	newColdIndex(t, cold)

	tests := []struct {
		name  string
		index string
		want  bool // with replication running
	}{
		{name: "disk index without a copy", index: disk},
		{name: "disk index with an installed copy", index: installed, want: true},
		{name: "cold index", index: cold, want: true},
		{name: "unknown index", index: "TestReadable.missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name+", replication off", func(t *testing.T) {
			assert.Equal(t, tt.index == cold, Readable(tt.index))
		})
	}

	member := true
	previous := inSync
	inSync = func(string) bool { return member }
	t.Cleanup(func() { inSync = previous })
	m := newManager(t, ModeDocument, newFakeTopology("node-b"), newLoopTransport())
	activate(t, m)
	m.replicaFor(installed).ready = true
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Readable(tt.index))
		})
	}

	// the owner dropped this replica: its copy misses acknowledged writes
	member = false
	assert.False(t, Readable(installed), "a copy outside the in-sync set serves no read")
	assert.True(t, Readable(cold), "a cold index has no in-sync set")
}

// A copy is current once it is in the in-sync set and applied what the
// owner shipped; a cold index is the same data on every node.
func TestCurrent(t *testing.T) {
	const disk, cold = "TestCurrent.index_1", "TestCurrent.index_2"
	index := newDiskIndex(t, disk)
	newColdIndex(t, cold)
	member := true
	previous := inSync
	inSync = func(string) bool { return member }
	t.Cleanup(func() { inSync = previous })

	assert.True(t, Current(cold))
	assert.False(t, Current("TestCurrent.missing"))
	assert.False(t, Current(disk), "replication is off")

	m := newManager(t, ModeDocument, newFakeTopology("node-b"), newLoopTransport())
	activate(t, m)
	assert.True(t, Current(disk), "nothing was shipped")
	member = false
	assert.False(t, Current(disk), "the owner dropped this replica from the in-sync set")
	member = true

	entry := []byte(`{"@_id":"1","@_action":"insert","@_shard":-1,"name":"one"}`)
	require.NoError(t, core.ApplyReplicaEntries(index, firstShard(t, index), [][]byte{entry}))
	waitDocs(t, index, 1)
	assert.Eventually(t, func() bool { return Current(disk) }, 30*time.Second, 10*time.Millisecond)

	// a segment replica serves its installed files, not the operations it kept
	m.mode = ModeSegment
	assert.False(t, Current(disk), "no replica state")
	r := m.replicaFor(disk)
	assert.True(t, Current(disk))
	r.seq = r.filesSeq + 1
	assert.False(t, Current(disk), "operations kept beyond the installed files")
}
