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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/bluge/directory"
	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func TestParseTranslogKey(t *testing.T) {
	tests := []struct {
		key  string
		term string
		seq  uint64
		ok   bool
	}{
		{key: "x/_translog/t1-00000000000000000007", term: "t1", seq: 7, ok: true},
		{key: "x/_translog/t1-abc"},
		{key: "x/_translog/-7"},
		{key: "x/_translog/t1"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			term, seq, ok := parseTranslogKey(translogPrefix("x"), tt.key)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.term, term)
			assert.Equal(t, tt.seq, seq)
		})
	}
}

func TestUploadErrors(t *testing.T) {
	m := newManager(t, ModeDocument, newFakeTopology("node-a"), newLoopTransport())
	p := &primary{index: "x", term: "t1", remote: true, storageType: "bogus", seq: 1,
		ops: []Op{{Seq: 1, Entry: json.RawMessage(`{}`)}}}
	assert.ErrorContains(t, m.upload(p), "upload translog")
	assert.Zero(t, p.uploaded)

	p.ops[0].Entry = json.RawMessage(`{`)
	assert.Error(t, m.upload(p), "an entry that cannot be encoded")
}

func TestReplayRemoteErrors(t *testing.T) {
	store := useObjectStore(t)
	name := "TestReplayRemoteErrors.index_1"
	newRemoteIndex(t, name)
	m := newManager(t, ModeDocument, newFakeTopology("node-a"), newLoopTransport())
	p := &primary{index: name, term: "t1", remote: true, storageType: core.StorageTypeS3}

	assert.Error(t, m.replayRemote(&primary{index: "TestReplayRemoteErrors.missing"}))

	store.SetErr(errors.New("bucket down"))
	assert.Error(t, m.replayRemote(p))
	store.SetErr(nil)

	key := translogKey(p, 1)
	require.NoError(t, directory.PutObject(core.StorageTypeS3, key, []byte("{")))
	assert.ErrorContains(t, m.replayRemote(p), key)

	data, err := json.Marshal([]Op{{Seq: 1, Shard: "nope", Entry: json.RawMessage(testEntry)}})
	require.NoError(t, err)
	require.NoError(t, directory.PutObject(core.StorageTypeS3, key, data))
	assert.Error(t, m.replayRemote(p), "an entry for an unknown shard")
}

func TestFlushRemote(t *testing.T) {
	store := useObjectStore(t)
	name := "TestFlushRemote.index_1"
	newRemoteIndex(t, name)
	m := newManager(t, ModeDocument, newFakeTopology("node-a"), newLoopTransport())
	ctx := context.Background()
	p := &primary{index: name, term: newTerm(1), remote: true, storageType: core.StorageTypeS3}
	put := func(key string) {
		t.Helper()
		require.NoError(t, directory.PutObject(core.StorageTypeS3, key, []byte("[]")))
	}

	t.Run("upload fails", func(t *testing.T) {
		broken := &primary{index: name, term: p.term, remote: true, storageType: "bogus", seq: 1,
			ops: []Op{{Seq: 1, Entry: json.RawMessage(`{}`)}}}
		assert.Error(t, m.flushRemote(ctx, broken))
	})

	t.Run("listing fails", func(t *testing.T) {
		store.SetErr(errors.New("bucket down"))
		assert.Error(t, m.flushRemote(ctx, p))
		store.SetErr(nil)
	})

	t.Run("index gone", func(t *testing.T) {
		gone := &primary{index: "TestFlushRemote.missing", term: p.term, remote: true, storageType: core.StorageTypeS3}
		put(translogKey(gone, 1))
		assert.NoError(t, m.flushRemote(ctx, gone))
		assert.Equal(t, 1, store.Len(translogPrefix(gone.index)), "nothing is trimmed")
	})

	t.Run("trims what the segments hold", func(t *testing.T) {
		p.uploaded = 2
		older := &primary{index: name, term: "00000000000000000000-00000000000000000000"}
		newer := &primary{index: name, term: p.term + "z"}
		put(translogKey(older, 9))
		put(translogKey(p, 2))
		put(translogKey(p, 3))
		put(translogKey(newer, 1))
		put(translogPrefix(name) + "garbage")
		require.NoError(t, m.flushRemote(ctx, p))
		keys, err := directory.ListObjects(core.StorageTypeS3, translogPrefix(name))
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{translogKey(p, 3), translogKey(newer, 1), translogPrefix(name) + "garbage"}, keys)
	})
}
