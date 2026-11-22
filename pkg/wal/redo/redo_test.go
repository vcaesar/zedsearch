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

package redo

import (
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

var l *Log

func TestMain(m *testing.M) {
	var err error
	l, err = Open("data/redoTest", nil)
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
	l, err := Open("data/redoTest2", nil)
	assert.NoError(t, err)
	err = l.Close()
	assert.NoError(t, err)
}

func TestLog(t *testing.T) {
	type args struct {
		index uint64
		data  []byte
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "1",
			args: args{
				index: 1,
				data:  []byte("test1"),
			},
		},
		{
			name: "2",
			args: args{
				index: 2,
				data:  []byte("test2"),
			},
		},
		{
			name: "2",
			args: args{
				index: 2,
				data:  []byte("test2-2"),
			},
		},
		{
			name: "3",
			args: args{
				index: 3,
				data:  []byte("test3"),
			},
		},
		{
			name: "3",
			args: args{
				index: 3,
				data:  []byte("test3-2"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := l.Write(tt.args.index, tt.args.data); (err != nil) != tt.wantErr {
				t.Errorf("Log.Write() error = %v, wantErr %v", err, tt.wantErr)
			}
			got, err := l.Read(tt.args.index)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.args.data, got)
			}
		})
	}
}

func BenchmarkLogWrite(b *testing.B) {
	var err error
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err = l.Write(1, []byte("test"))
		assert.NoError(b, err)
	}
}

func BenchmarkLogRead(b *testing.B) {
	var err error
	var data []byte
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, err = l.Read(1)
		assert.NoError(b, err)
		assert.NotNil(b, data)
	}
}

func TestReopen(t *testing.T) {
	name := "data/TestReopen"
	l, err := Open(name, &Options{NoSync: true})
	assert.NoError(t, err)
	assert.NoError(t, l.Write(1, []byte("one")))
	assert.NoError(t, l.Write(2, []byte("two")))
	assert.NoError(t, l.Write(1, []byte("uno")))
	assert.NoError(t, l.Close())

	l, err = Open(name, nil)
	assert.NoError(t, err)
	for key, want := range map[uint64]string{1: "uno", 2: "two"} {
		got, err := l.Read(key)
		assert.NoError(t, err)
		assert.Equal(t, want, string(got))
	}
	assert.NoError(t, l.Close())
}

func TestOpenError(t *testing.T) {
	assert.NoError(t, os.MkdirAll("data/TestOpenError/dir", 0o755))
	assert.NoError(t, os.WriteFile("data/TestOpenError/file", []byte("x"), 0o644))
	for _, tt := range []struct{ name, path string }{
		{"parent is a file", "data/TestOpenError/file/log"},
		{"log is a directory", "data/TestOpenError/dir"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Open(tt.path, nil)
			assert.Error(t, err)
		})
	}
}

func TestErrors(t *testing.T) {
	l, err := Open("data/TestErrors", nil)
	assert.NoError(t, err)

	assert.ErrorIs(t, l.Write(1, make([]byte, ValueFixedLength+1)), ErrValueTooLarge)
	assert.NoError(t, l.Write(1, make([]byte, ValueFixedLength)))
	_, err = l.Read(2)
	assert.ErrorIs(t, err, ErrNotFound)

	assert.NoError(t, l.f.Close())
	assert.Error(t, l.Write(2, []byte("closed")))
	assert.Error(t, l.Close())
}
