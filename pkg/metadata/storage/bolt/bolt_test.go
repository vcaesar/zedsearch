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

package bolt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vcaesar/zedsearch/pkg/errors"
)

func Test_boltStorage_List(t *testing.T) {
	type args struct {
		prefix string
		in1    int
		in2    int
	}
	tests := []struct {
		name    string
		args    args
		wantNum int
		wantErr bool
	}{
		{
			name: "normal",
			args: args{
				prefix: "/test/",
			},
			wantNum: 1,
			wantErr: false,
		},
		{
			name: "empty",
			args: args{
				prefix: "/notexist/",
			},
			wantNum: 0,
			wantErr: false,
		},
	}

	store := New("/zedsearch/test")
	defer store.Close()
	t.Run("prepare", func(t *testing.T) {
		err := store.Set("/test/foo", []byte("bar"))
		assert.NoError(t, err)
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.List(tt.args.prefix, tt.args.in1, tt.args.in2)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.GreaterOrEqual(t, len(got), tt.wantNum)
		})
	}
}

func Test_boltStorage_Get(t *testing.T) {
	type args struct {
		key string
	}
	tests := []struct {
		name    string
		args    args
		want    []byte
		wantErr bool
	}{
		{
			name: "normal",
			args: args{
				key: "/test/foo",
			},
			want:    []byte("bar"),
			wantErr: false,
		},
		{
			name: "not exist",
			args: args{
				key: "/test/notexist",
			},
			want:    nil,
			wantErr: true,
		},
	}

	store := New("/zedsearch/test")
	defer store.Close()
	t.Run("prepare", func(t *testing.T) {
		err := store.Set("/test/foo", []byte("bar"))
		assert.NoError(t, err)
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.Get(tt.args.key)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_boltStorage_Set(t *testing.T) {
	type args struct {
		key   string
		value []byte
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "normal",
			args: args{
				key:   "/test/foo",
				value: []byte("bar"),
			},
			wantErr: false,
		},
		{
			name: "empty",
			args: args{
				key:   "",
				value: nil,
			},
			wantErr: true,
		},
	}

	store := New("/zedsearch/test")
	defer store.Close()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := store.Set(tt.args.key, tt.args.value); (err != nil) != tt.wantErr {
				t.Errorf("boltStorage.Set() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_boltStorage_Delete(t *testing.T) {
	type args struct {
		key string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "normal",
			args: args{
				key: "/test/foo",
			},
			wantErr: false,
		},
		{
			name: "empty",
			args: args{
				key: "",
			},
			wantErr: true,
		},
	}

	store := New("/zedsearch/test")
	defer store.Close()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := store.Delete(tt.args.key); (err != nil) != tt.wantErr {
				t.Errorf("boltStorage.Delete() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_boltStorage_EdgeCases(t *testing.T) {
	store := New("/zedsearch/test_edge")
	defer store.Close()

	t.Run("empty prefix lists nothing", func(t *testing.T) {
		got, err := store.List("", 0, 0)
		assert.NoError(t, err)
		assert.Empty(t, got)
	})
	t.Run("get from missing bucket", func(t *testing.T) {
		_, err := store.Get("/Test_boltStorage_EdgeCases_missing/foo")
		assert.ErrorIs(t, err, errors.ErrKeyNotFound)
	})
	t.Run("set without bucket name", func(t *testing.T) {
		assert.Error(t, store.Set("/foo", []byte("bar")))
	})
	t.Run("delete from missing bucket", func(t *testing.T) {
		assert.NoError(t, store.Delete("/Test_boltStorage_EdgeCases_missing/foo"))
	})
}

func Test_openbboltDB_Error(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	assert.NoError(t, os.WriteFile(file, nil, 0o600))
	_, err := openbboltDB(filepath.Join(file, "sub", "db.bolt"), false)
	assert.Error(t, err)
}
