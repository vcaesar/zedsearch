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

package upgrade

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
)

// withDataPath points config.Global.DataPath at a fresh temp dir for one test.
func withDataPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := config.Global.DataPath
	config.Global.DataPath = dir
	t.Cleanup(func() { config.Global.DataPath = old })
	return dir
}

// withShardNum overrides config.Global.Shard.Num for one test.
func withShardNum(t *testing.T, n int64) {
	t.Helper()
	old := config.Global.Shard.Num
	config.Global.Shard.Num = n
	t.Cleanup(func() { config.Global.Shard.Num = old })
}

// writeFile creates parent dirs and writes content to root/parts....
func writeFile(t *testing.T, content string, parts ...string) {
	t.Helper()
	p := filepath.Join(parts...)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

func readFile(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(parts...))
	require.NoError(t, err)
	return string(b)
}

func sortedShardIDs(index *meta.Index) []string {
	ids := make([]string, 0, len(index.Shards))
	for id := range index.Shards {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func TestDo(t *testing.T) {
	t.Run("missing index is a no-op for every version", func(t *testing.T) {
		withDataPath(t)
		for _, v := range []string{"0.2.4", "v0.2.4", "0.2.5", "v0.2.6", "0.2.7", "0.2.8", "v0.2.9", "0.4.0", ""} {
			index := &meta.Index{Name: "TestDo.missing"}
			assert.NoError(t, Do(v, index), v)
			assert.Empty(t, index.Shards, v)
		}
	})

	t.Run("current versions leave data untouched", func(t *testing.T) {
		root := withDataPath(t)
		name := "TestDo.current"
		writeFile(t, "seg", root, name, "000000", "seg")
		for _, v := range []string{"0.2.7", "v0.2.8", "0.2.9", "1.0.0"} {
			index := &meta.Index{Name: name}
			assert.NoError(t, Do(v, index), v)
			assert.Empty(t, index.Shards, v)
			assert.Equal(t, "seg", readFile(t, root, name, "000000", "seg"), v)
		}
	})

	t.Run("v0.2.4 chains through every upgrade", func(t *testing.T) {
		root := withDataPath(t)
		withShardNum(t, 3)
		name := "TestDo.v024"
		writeFile(t, "seg", root, name, "seg")

		index := &meta.Index{Name: name}
		require.NoError(t, Do("v0.2.4", index))

		assert.Equal(t, int64(3), index.ShardNum)
		require.Len(t, index.Shards, 3)
		ids := sortedShardIDs(index)
		assert.Equal(t, "seg", readFile(t, root, name, ids[0], "000000", "seg"))
		for _, id := range ids[1:] {
			entries, err := os.ReadDir(filepath.Join(root, name, id))
			require.NoError(t, err)
			assert.Empty(t, entries)
		}
		_, err := os.Stat(filepath.Join(root, name+"_old"))
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("v0.2.5 chains to v0.2.7", func(t *testing.T) {
		root := withDataPath(t)
		withShardNum(t, 1)
		name := "TestDo.v025"
		writeFile(t, "seg", root, name, "000000", "seg")

		index := &meta.Index{Name: name}
		require.NoError(t, Do("0.2.5", index))
		ids := sortedShardIDs(index)
		require.Len(t, ids, 1)
		assert.Equal(t, "seg", readFile(t, root, name, ids[0], "000000", "seg"))
	})

	t.Run("v0.2.4 step error stops the chain", func(t *testing.T) {
		root := withDataPath(t)
		name := "TestDo.v024err"
		writeFile(t, "seg", root, name, "seg")
		writeFile(t, "busy", root, name+"_old", "busy")

		index := &meta.Index{Name: name}
		assert.Error(t, Do("0.2.4", index))
		assert.Empty(t, index.Shards)
	})

	t.Run("v0.2.6 step error is returned", func(t *testing.T) {
		root := withDataPath(t)
		withShardNum(t, 1)
		name := "TestDo.v026err"
		writeFile(t, "seg", root, name, "000000", "seg")
		writeFile(t, "busy", root, name+"_old", "busy")

		assert.Error(t, Do("0.2.6", &meta.Index{Name: name}))
	})
}

func TestUpgradeFromV024T025(t *testing.T) {
	t.Run("missing index is skipped", func(t *testing.T) {
		root := withDataPath(t)
		name := "TestUpgradeFromV024T025.missing"
		assert.NoError(t, UpgradeFromV024T025(&meta.Index{Name: name}))
		_, err := os.Stat(filepath.Join(root, name))
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("already upgraded index is skipped", func(t *testing.T) {
		root := withDataPath(t)
		name := "TestUpgradeFromV024T025.done"
		writeFile(t, "seg", root, name, "000000", "seg")
		assert.NoError(t, UpgradeFromV024T025(&meta.Index{Name: name}))
		assert.Equal(t, "seg", readFile(t, root, name, "000000", "seg"))
		_, err := os.Stat(filepath.Join(root, name, "000000", "000000"))
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("index moves into 000000", func(t *testing.T) {
		root := withDataPath(t)
		name := "TestUpgradeFromV024T025.move"
		writeFile(t, "a", root, name, "a")
		writeFile(t, "b", root, name, "sub", "b")

		require.NoError(t, UpgradeFromV024T025(&meta.Index{Name: name}))
		assert.Equal(t, "a", readFile(t, root, name, "000000", "a"))
		assert.Equal(t, "b", readFile(t, root, name, "000000", "sub", "b"))
		_, err := os.Stat(filepath.Join(root, name+"_old"))
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("rename error is returned", func(t *testing.T) {
		root := withDataPath(t)
		name := "TestUpgradeFromV024T025.err"
		writeFile(t, "a", root, name, "a")
		writeFile(t, "busy", root, name+"_old", "busy")

		assert.Error(t, UpgradeFromV024T025(&meta.Index{Name: name}))
		assert.Equal(t, "a", readFile(t, root, name, "a"))
	})
}

func TestUpgradeFromV025T026(t *testing.T) {
	assert.NoError(t, UpgradeFromV025T026(nil))
	assert.NoError(t, UpgradeFromV025T026(&meta.Index{Name: "TestUpgradeFromV025T026"}))
}

func TestUpgradeFromV026T027(t *testing.T) {
	t.Run("missing index is skipped", func(t *testing.T) {
		withDataPath(t)
		index := &meta.Index{Name: "TestUpgradeFromV026T027.missing"}
		assert.NoError(t, UpgradeFromV026T027(index))
		assert.Empty(t, index.Shards)
	})

	t.Run("empty shards get default metadata", func(t *testing.T) {
		root := withDataPath(t)
		withShardNum(t, 2)
		name := "TestUpgradeFromV026T027.default"
		writeFile(t, "s0", root, name, "000000", "seg")
		writeFile(t, "s1", root, name, "000001", "seg")

		index := &meta.Index{Name: name}
		require.NoError(t, UpgradeFromV026T027(index))

		assert.Equal(t, int64(2), index.ShardNum)
		ids := sortedShardIDs(index)
		require.Len(t, ids, 2)
		assert.Equal(t, int64(1), index.Shards[ids[0]].ShardNum)
		assert.Equal(t, "s0", readFile(t, root, name, ids[0], "000000", "seg"))
		assert.Equal(t, "s1", readFile(t, root, name, ids[0], "000001", "seg"))
		entries, err := os.ReadDir(filepath.Join(root, name, ids[1]))
		require.NoError(t, err)
		assert.Empty(t, entries)
		_, err = os.Stat(filepath.Join(root, name+"_old"))
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("existing shards are kept and data goes to the first", func(t *testing.T) {
		root := withDataPath(t)
		name := "TestUpgradeFromV026T027.existing"
		writeFile(t, "s0", root, name, "000000", "seg")
		index := &meta.Index{
			Name:     name,
			ShardNum: 2,
			Shards: map[string]*meta.IndexShard{
				"b": {ID: "b", ShardNum: 1},
				"a": {ID: "a", ShardNum: 1},
			},
		}

		require.NoError(t, UpgradeFromV026T027(index))
		assert.Equal(t, []string{"a", "b"}, sortedShardIDs(index))
		assert.Equal(t, "s0", readFile(t, root, name, "a", "000000", "seg"))
		entries, err := os.ReadDir(filepath.Join(root, name, "b"))
		require.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("already upgraded index is skipped", func(t *testing.T) {
		root := withDataPath(t)
		name := "TestUpgradeFromV026T027.done"
		writeFile(t, "s0", root, name, "a", "000000", "seg")
		index := &meta.Index{
			Name:     name,
			ShardNum: 1,
			Shards:   map[string]*meta.IndexShard{"a": {ID: "a", ShardNum: 1}},
		}

		require.NoError(t, UpgradeFromV026T027(index))
		assert.Equal(t, "s0", readFile(t, root, name, "a", "000000", "seg"))
		_, err := os.Stat(filepath.Join(root, name, "a", "a"))
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("rename error is returned", func(t *testing.T) {
		root := withDataPath(t)
		withShardNum(t, 1)
		name := "TestUpgradeFromV026T027.err"
		writeFile(t, "s0", root, name, "000000", "seg")
		writeFile(t, "busy", root, name+"_old", "busy")

		assert.Error(t, UpgradeFromV026T027(&meta.Index{Name: name}))
		assert.Equal(t, "s0", readFile(t, root, name, "000000", "seg"))
	})

	t.Run("metadata error is returned", func(t *testing.T) {
		root := withDataPath(t)
		withShardNum(t, 1<<10+1)
		name := "TestUpgradeFromV026T027.meta"
		writeFile(t, "s0", root, name, "000000", "seg")

		index := &meta.Index{Name: name}
		assert.Error(t, UpgradeFromV026T027(index))
		assert.Empty(t, index.Shards)
		assert.Equal(t, "s0", readFile(t, root, name, "000000", "seg"))
	})
}

func TestUpgradeMetadataFromV026T027(t *testing.T) {
	t.Run("nil data uses one empty shard", func(t *testing.T) {
		withShardNum(t, 3)
		idx, err := UpgradeMetadataFromV026T027(nil)
		require.NoError(t, err)

		assert.Equal(t, int64(3), idx.ShardNum)
		ids := sortedShardIDs(idx)
		require.Len(t, ids, 3)
		for _, id := range ids {
			assert.Equal(t, id, idx.Shards[id].ID)
			assert.Equal(t, int64(1), idx.Shards[id].ShardNum)
			require.Len(t, idx.Shards[id].Shards, 1)
			assert.Equal(t, int64(0), idx.Shards[id].Shards[0].ID)
		}
		assert.Zero(t, idx.Stats.DocNum)
		assert.Zero(t, idx.Stats.StorageSize)
	})

	t.Run("v0.2.6 shards move into the first shard", func(t *testing.T) {
		withShardNum(t, 2)
		data := []byte(`{
			"name": "TestUpgradeMetadataFromV026T027.data",
			"storage_type": "disk",
			"shard_num": 2,
			"shards": [
				{"id": 0, "doc_num": 3, "storage_size": 30, "doc_time_min": 1, "doc_time_max": 5},
				{"id": 1, "doc_num": 4, "storage_size": 40, "doc_time_min": 2, "doc_time_max": 9}
			]
		}`)
		idx, err := UpgradeMetadataFromV026T027(data)
		require.NoError(t, err)

		assert.Equal(t, "TestUpgradeMetadataFromV026T027.data", idx.Name)
		assert.Equal(t, "disk", idx.StorageType)
		assert.Equal(t, int64(2), idx.ShardNum)
		assert.Equal(t, uint64(7), idx.Stats.DocNum)
		assert.Equal(t, uint64(70), idx.Stats.StorageSize)

		ids := sortedShardIDs(idx)
		require.Len(t, ids, 2)
		first := idx.Shards[ids[0]]
		assert.Equal(t, int64(2), first.ShardNum)
		assert.Equal(t, uint64(7), first.Stats.DocNum)
		assert.Equal(t, uint64(70), first.Stats.StorageSize)
		require.Len(t, first.Shards, 2)
		assert.Equal(t, meta.IndexStat{DocNum: 3, StorageSize: 30, DocTimeMin: 1, DocTimeMax: 5}, first.Shards[0].Stats)
		assert.Equal(t, int64(1), first.Shards[1].ID)
		assert.Equal(t, meta.IndexStat{DocNum: 4, StorageSize: 40, DocTimeMin: 2, DocTimeMax: 9}, first.Shards[1].Stats)

		second := idx.Shards[ids[1]]
		assert.Equal(t, int64(1), second.ShardNum)
		assert.Zero(t, second.Stats.DocNum)
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := UpgradeMetadataFromV026T027([]byte(`{"shard_num":`))
		assert.Error(t, err)
	})

	t.Run("shard num beyond node id range", func(t *testing.T) {
		withShardNum(t, 1<<10+1)
		_, err := UpgradeMetadataFromV026T027(nil)
		assert.Error(t, err)
	})
}
