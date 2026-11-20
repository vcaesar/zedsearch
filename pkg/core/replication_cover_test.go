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

package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// invalidAnalysisMeta returns the stored record of an index whose analysis
// cannot be parsed.
func invalidAnalysisMeta(t *testing.T, name string) []byte {
	t.Helper()
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	index.ref.Settings = &meta.IndexSettings{Analysis: &meta.IndexAnalysis{
		Analyzer: map[string]*meta.Analyzer{"default": {Type: "bogus"}},
	}}
	data, err := index.MarshalJSON()
	require.NoError(t, err)
	return data
}

func TestLoadIndex_InvalidRecord(t *testing.T) {
	name := "TestLoadIndex_InvalidRecord.index_1"
	data := invalidAnalysisMeta(t, name)
	require.NoError(t, metadata.Index.Set(name, data))
	t.Cleanup(func() { assert.NoError(t, metadata.Index.Delete(name)) })

	_, err := LoadIndex(name)
	assert.Error(t, err)
	assert.Error(t, InstallSnapshot(name, data, t.TempDir()))
	_, ok := GetIndex(name)
	assert.False(t, ok)
}

func TestApplyReplicaEntries_Errors(t *testing.T) {
	t.Run("read only", func(t *testing.T) {
		index, err := NewIndex("TestApplyReplicaEntries_Errors.index_1", StorageTypeDisk, 1)
		require.NoError(t, err)
		index.applyTier(TierCold)
		assert.ErrorIs(t, ApplyReplicaEntries(index, anyShard(index).GetID(), nil), ErrIndexReadOnly)
	})

	t.Run("retired", func(t *testing.T) {
		name := "TestApplyReplicaEntries_Errors.index_2"
		index, err := NewIndex(name, StorageTypeDisk, 1)
		require.NoError(t, err)
		require.NoError(t, StoreIndex(index))
		t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
		index.retired.Store(true)
		assert.ErrorIs(t, ApplyReplicaEntries(index, anyShard(index).GetID(), nil), ErrIndexReadOnly)
		_, err = index.SnapshotTo(t.TempDir())
		assert.ErrorIs(t, err, ErrIndexReadOnly)
	})

	t.Run("invalid entry", func(t *testing.T) {
		index, shard := openTestShard(t, "TestApplyReplicaEntries_Errors.index_3")
		assert.Error(t, ApplyReplicaEntries(index, shard.GetID(), [][]byte{[]byte("{")}))
	})
}

func TestSnapshotTo_TargetIsAFile(t *testing.T) {
	index, _ := openTestShard(t, "TestSnapshotTo_TargetIsAFile.index_1")
	dir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(dir, nil, 0o600))
	_, err := index.SnapshotTo(dir)
	assert.Error(t, err)
}

func TestLinkGraphs_BadPattern(t *testing.T) {
	assert.Error(t, linkGraphs("[", t.TempDir()))
}

func TestInstallSnapshot_Errors(t *testing.T) {
	t.Run("old copy fails to close", func(t *testing.T) {
		index, shard := openTestShard(t, "TestInstallSnapshot_Errors.index_1")
		data, err := index.MarshalJSON()
		require.NoError(t, err)
		require.NoError(t, shard.wal.Close())
		assert.Error(t, InstallSnapshot(index.GetName(), data, t.TempDir()))
		assert.True(t, index.retired.Load())
	})

	t.Run("data path is blocked", func(t *testing.T) {
		parent := "TestInstallSnapshot_Errors.file"
		blocked := filepath.Join(config.Global.DataPath, parent)
		require.NoError(t, os.WriteFile(blocked, nil, 0o600))
		t.Cleanup(func() { assert.NoError(t, os.Remove(blocked)) })
		name := parent + "/index_2"
		data, err := json.Marshal(&meta.Index{Name: name})
		require.NoError(t, err)
		assert.Error(t, InstallSnapshot(name, data, t.TempDir()))
	})

	t.Run("data path is not writable", func(t *testing.T) {
		skipWithoutPermissions(t)
		parent := "TestInstallSnapshot_Errors.readonly"
		dir := filepath.Join(config.Global.DataPath, parent)
		require.NoError(t, os.MkdirAll(dir, 0o750))
		require.NoError(t, os.Chmod(dir, 0o500))
		t.Cleanup(func() {
			assert.NoError(t, os.Chmod(dir, 0o750))
			assert.NoError(t, os.RemoveAll(dir))
		})
		name := parent + "/sub/index_4"
		data, err := json.Marshal(&meta.Index{Name: name})
		require.NoError(t, err)
		assert.Error(t, InstallSnapshot(name, data, t.TempDir()))
	})

	t.Run("snapshot directory is missing", func(t *testing.T) {
		name := "TestInstallSnapshot_Errors.index_3"
		data, err := json.Marshal(&meta.Index{Name: name})
		require.NoError(t, err)
		assert.Error(t, InstallSnapshot(name, data, filepath.Join(t.TempDir(), "missing")))
		_, ok := GetIndex(name)
		assert.False(t, ok)
	})
}

// Entries arriving while the shard is closed by a flush are rejected.
func TestApplyReplicaEntries_ShardClosedMeanwhile(t *testing.T) {
	index, shard := openTestShard(t, "TestApplyReplicaEntries_ShardClosedMeanwhile.index_1")
	entry := []byte(`{"@_id":"1","@_action":"insert","@_shard":-1,"name":"one"}`)

	index.writes.Lock()
	done := make(chan error, 1)
	go func() { done <- ApplyReplicaEntries(index, shard.GetID(), [][]byte{entry}) }()
	waitBlocked(t, "core.ApplyReplicaEntries(", "sync.RWMutex.RLock", 1)
	err := shard.Close()
	index.writes.Unlock()
	require.NoError(t, err)
	assert.ErrorIs(t, <-done, ErrIndexReadOnly)
}
