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

package cluster

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStoreClosed(t *testing.T) {
	store := NewMemoryStore()
	require.NoError(t, store.Close())

	_, err := store.Grant(context.Background(), time.Second)
	assert.ErrorIs(t, err, ErrStoreClosed)
	_, err = store.Watch(context.Background(), "/")
	assert.ErrorIs(t, err, ErrStoreClosed)
}

func TestMemoryStoreDeleteMissing(t *testing.T) {
	store := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := store.Watch(ctx, "/")
	require.NoError(t, err)

	require.NoError(t, store.Delete(ctx, "/missing"))
	select {
	case event := <-events:
		t.Fatalf("deleting a missing key must not notify, got %+v", event)
	default:
	}
}

func TestMemoryStoreSlowWatcherDropsEvents(t *testing.T) {
	store := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := store.Watch(ctx, "/")
	require.NoError(t, err)

	for i := range 100 {
		_, err := store.Put(ctx, "/k"+strconv.Itoa(i), nil, NoLease)
		require.NoError(t, err, "a full watcher must not block writers")
	}
	assert.Len(t, events, cap(events))
}
