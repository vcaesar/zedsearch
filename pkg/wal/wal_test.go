/* Copyright 2022 Zinc Labs Inc. and Contributors
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

package wal

import (
	"log"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vcaesar/zedsearch/pkg/config"
)

var l *Log

const name = "walTest"

func TestMain(m *testing.M) {
	var err error
	l, err = Open(name)
	if err != nil {
		log.Fatal(err)
	}

	code := m.Run()

	if err = l.Close(); err != nil {
		log.Fatal(err)
	}

	os.Exit(code)
}

func TestOpenClose(t *testing.T) {
	l, err := Open("walTest2")
	assert.NoError(t, err)
	err = l.Close()
	assert.NoError(t, err)
}

func TestWAL(t *testing.T) {
	// plain go test keeps data/ between runs: work after the last entry
	before, err := l.LastIndex()
	assert.NoError(t, err)
	id := before + 1

	err = l.Write([]byte("test"))
	assert.NoError(t, err)

	var data []byte
	data, err = l.Read(id)
	assert.NoError(t, err)
	assert.Equal(t, "test", string(data))

	assert.Equal(t, name, l.Name())

	err = l.TruncateFront(id)
	assert.NoError(t, err)

	length, err := l.Len()
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), length)

	firstIndex, err := l.FirstIndex()
	assert.NoError(t, err)
	assert.Equal(t, id, firstIndex)

	lastIndex, err := l.LastIndex()
	assert.NoError(t, err)
	assert.Equal(t, id, lastIndex)

	err = l.Sync()
	assert.NoError(t, err)
}

func TestWriteBatch(t *testing.T) {
	l, err := Open("walTestBatch")
	assert.NoError(t, err)
	before, err := l.LastIndex()
	assert.NoError(t, err)

	assert.NoError(t, l.WriteBatch(nil))
	assert.NoError(t, l.WriteBatch([][]byte{[]byte("one"), []byte("two"), []byte("three")}))
	last, err := l.LastIndex()
	assert.NoError(t, err)
	assert.Equal(t, before+3, last)
	for i, want := range []string{"one", "two", "three"} {
		data, err := l.Read(before + uint64(i) + 1)
		assert.NoError(t, err)
		assert.Equal(t, want, string(data))
	}

	assert.NoError(t, l.Close())
	assert.Error(t, l.WriteBatch([][]byte{[]byte("closed")}))
}

func TestWriteBatchSpansSegments(t *testing.T) {
	dataPath := config.Global.DataPath
	config.Global.DataPath = t.TempDir()
	defer func() { config.Global.DataPath = dataPath }()

	l, err := Open("TestWriteBatchSpansSegments")
	assert.NoError(t, err)
	defer func() { assert.NoError(t, l.Close()) }()

	big := make([]byte, 9<<20) // two entries exceed the 16 MB segment size
	assert.NoError(t, l.WriteBatch([][]byte{big, big, []byte("tail")}))

	segments, err := os.ReadDir(path.Join(config.Global.DataPath, "TestWriteBatchSpansSegments", "wal"))
	assert.NoError(t, err)
	assert.Greater(t, len(segments), 1)

	last, err := l.LastIndex()
	assert.NoError(t, err)
	assert.Equal(t, uint64(3), last)
	data, err := l.Read(3)
	assert.NoError(t, err)
	assert.Equal(t, "tail", string(data))
}

func BenchmarkWAL(b *testing.B) {
	var err error
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err = l.Write([]byte("test"))
		assert.NoError(b, err)
	}
}

func TestOpenError(t *testing.T) {
	dataPath := config.Global.DataPath
	config.Global.DataPath = t.TempDir()
	defer func() { config.Global.DataPath = dataPath }()

	t.Run("wal", func(t *testing.T) {
		dir := path.Join(config.Global.DataPath, "TestOpenError.wal")
		assert.NoError(t, os.MkdirAll(dir, 0o750))
		// the wal directory is a file
		assert.NoError(t, os.WriteFile(path.Join(dir, "wal"), []byte("x"), 0o640))
		_, err := Open("TestOpenError.wal")
		assert.Error(t, err)
	})
	t.Run("redo", func(t *testing.T) {
		// the redo file is a directory
		dir := path.Join(config.Global.DataPath, "TestOpenError.redo")
		assert.NoError(t, os.MkdirAll(path.Join(dir, "redo"), 0o750))
		l, err := Open("TestOpenError.redo")
		assert.Error(t, err)
		assert.Nil(t, l)
		// the wal must be closed on failure, or Windows cannot remove its files
		assert.NoError(t, os.RemoveAll(dir))
	})
}

func TestCloseTwice(t *testing.T) {
	l, err := Open("TestCloseTwice")
	assert.NoError(t, err)
	assert.NoError(t, l.Close())
	assert.Error(t, l.Close())
}
