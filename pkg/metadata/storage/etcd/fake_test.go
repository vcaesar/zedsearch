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

package etcd

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.etcd.io/etcd/api/v3/mvccpb"
	client "go.etcd.io/etcd/client/v3"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/errors"
)

// fakeKV is an in-memory client.KV covering the calls etcdStorage makes.
type fakeKV struct {
	client.KV
	data map[string]string
}

func (f *fakeKV) Get(_ context.Context, key string, opts ...client.OpOption) (*client.GetResponse, error) {
	prefix := len(client.OpGet(key, opts...).RangeBytes()) > 0
	keys := make([]string, 0, len(f.data))
	for k := range f.data {
		if k == key || prefix && strings.HasPrefix(k, key) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	resp := &client.GetResponse{}
	for _, k := range keys {
		resp.Kvs = append(resp.Kvs, &mvccpb.KeyValue{Key: []byte(k), Value: []byte(f.data[k])})
	}
	return resp, nil
}

func (f *fakeKV) Put(_ context.Context, key, val string, _ ...client.OpOption) (*client.PutResponse, error) {
	f.data[key] = val
	return &client.PutResponse{}, nil
}

func (f *fakeKV) Delete(_ context.Context, key string, _ ...client.OpOption) (*client.DeleteResponse, error) {
	delete(f.data, key)
	return &client.DeleteResponse{}, nil
}

// newStore uses the configured etcd when available, otherwise an in-memory fake.
func newStore(t *testing.T) *etcdStorage {
	t.Helper()
	if config.Global.Etcd.Endpoints != nil {
		return New("/zedsearch/test").(*etcdStorage)
	}
	cli := client.NewCtxClient(context.Background())
	cli.KV = &fakeKV{data: map[string]string{}}
	return &etcdStorage{prefix: "/zedsearch/test", cli: cli}
}

func Test_etcdStorage_ListPrefix(t *testing.T) {
	store := newStore(t)
	defer store.Close()
	assert.NoError(t, store.Set("/Test_etcdStorage_ListPrefix/a", []byte("1")))
	assert.NoError(t, store.Set("/Test_etcdStorage_ListPrefix/b", []byte("2")))
	assert.NoError(t, store.Set("/Test_etcdStorage_ListPrefixOther/c", []byte("3")))

	got, err := store.List("/Test_etcdStorage_ListPrefix/", 0, 0)
	assert.NoError(t, err)
	assert.Equal(t, [][]byte{[]byte("1"), []byte("2")}, got)

	_, err = store.Get("/Test_etcdStorage_ListPrefix/missing")
	assert.ErrorIs(t, err, errors.ErrKeyNotFound)
	for _, key := range []string{"/Test_etcdStorage_ListPrefix/a", "/Test_etcdStorage_ListPrefix/b", "/Test_etcdStorage_ListPrefixOther/c"} {
		assert.NoError(t, store.Delete(key))
	}
}

func Test_etcdStorage_Unreachable(t *testing.T) {
	if config.Global.Etcd.Endpoints != nil {
		t.Skip("etcd is configured")
	}
	prevTimeout := timeout
	timeout = 50 * time.Millisecond
	config.Global.Etcd.Endpoints = []string{"127.0.0.1:1"}
	t.Cleanup(func() {
		timeout = prevTimeout
		config.Global.Etcd.Endpoints = nil
	})

	store := New("/zedsearch/test")
	_, err := store.List("/test/", 0, 0)
	assert.Error(t, err)
	_, err = store.Get("/test/foo")
	assert.Error(t, err)
	assert.Error(t, store.Set("/test/foo", []byte("bar")))
	assert.Error(t, store.Delete("/test/foo"))
	assert.NoError(t, store.Close())
}
