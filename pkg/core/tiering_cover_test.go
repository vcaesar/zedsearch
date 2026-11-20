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
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
)

func setLifecycle(index *Index, lifecycle *meta.IndexLifecycle) {
	index.lock.Lock()
	index.ref.Settings.Lifecycle = lifecycle
	index.lock.Unlock()
}

// stopTiering cancels the mover and waits until its goroutine returned, so no
// lifecycle pass outlives the test.
func stopTiering(t *testing.T, cancel context.CancelFunc) {
	t.Helper()
	cancel()
	require.Eventually(t, func() bool {
		buf := make([]byte, 1<<20)
		for {
			if size := runtime.Stack(buf, true); size < len(buf) {
				return !strings.Contains(string(buf[:size]), "core.StartTiering.func1(")
			}
			buf = make([]byte, 2*len(buf))
		}
	}, 10*time.Second, time.Millisecond, "the lifecycle mover did not stop")
}

func TestStartTiering(t *testing.T) {
	oldEnable, oldInterval := config.Global.Tier.Enable, config.Global.Tier.CheckInterval
	config.Global.Tier.Enable = true
	t.Cleanup(func() {
		config.Global.Tier.Enable = oldEnable
		config.Global.Tier.CheckInterval = oldInterval
	})

	t.Run("default interval", func(t *testing.T) {
		config.Global.Tier.CheckInterval = 0
		ctx, cancel := context.WithCancel(context.Background())
		StartTiering(ctx)
		stopTiering(t, cancel)
	})

	t.Run("expired index is deleted", func(t *testing.T) {
		name := "TestStartTiering.index_1"
		index := newTierIndex(t, name)
		setLifecycle(index, &meta.IndexLifecycle{DeleteAfter: "1ns"})
		config.Global.Tier.CheckInterval = 10 * time.Millisecond
		ctx, cancel := context.WithCancel(context.Background())
		StartTiering(ctx)
		defer stopTiering(t, cancel)
		// the metadata is removed last by DeleteIndex
		require.Eventually(t, func() bool {
			_, err := metadata.Index.Get(name)
			return err != nil
		}, 30*time.Second, 10*time.Millisecond)
		_, exists := GetIndex(name)
		assert.False(t, exists)
		assert.NoDirExists(t, filepath.Join(config.Global.DataPath, name))
	})
}

func TestRunTieringOnce_Errors(t *testing.T) {
	old := config.Global.Tier.ColdStorageType
	config.Global.Tier.ColdStorageType = StorageTypeDisk
	defer func() { config.Global.Tier.ColdStorageType = old }()

	missing := newTierIndex(t, "TestRunTieringOnce_Errors.index_1")
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(missing.GetName())) })
	setLifecycle(missing, &meta.IndexLifecycle{Policy: "TestRunTieringOnce_Errors.missing"})

	unmovable := newTierIndex(t, "TestRunTieringOnce_Errors.index_2")
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(unmovable.GetName())) })
	setLifecycle(unmovable, &meta.IndexLifecycle{WarmAfter: "1ns"})

	runTieringOnce(time.Now().Add(time.Hour))
	assert.Equal(t, TierHot, missing.GetTier(), "a lifecycle that cannot be resolved is skipped")
	assert.Equal(t, TierHot, unmovable.GetTier(), "a failed move keeps the index where it is")
}

// An index deleted while the mover runs is skipped.
func TestRunTieringOnce_DeleteFails(t *testing.T) {
	name := "TestRunTieringOnce_DeleteFails.index_1"
	listed := "TestRunTieringOnce_DeleteFails.listed"
	index := newTierIndex(t, name)
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })
	setLifecycle(index, &meta.IndexLifecycle{DeleteAfter: "1ns"})

	// listed under another name, GetIndex no longer finds it by its own
	ZED_INDEX_LIST.lock.Lock()
	delete(ZED_INDEX_LIST.Indexes, name)
	ZED_INDEX_LIST.Indexes[listed] = index
	ZED_INDEX_LIST.lock.Unlock()
	runTieringOnce(time.Now().Add(time.Hour))
	ZED_INDEX_LIST.lock.Lock()
	delete(ZED_INDEX_LIST.Indexes, listed)
	ZED_INDEX_LIST.Indexes[name] = index
	ZED_INDEX_LIST.lock.Unlock()

	setLifecycle(index, nil)
	_, exists := GetIndex(name)
	assert.True(t, exists)
}

func TestExplainTiering_NextPhase(t *testing.T) {
	name := "TestExplainTiering_NextPhase.index_1"
	index, err := NewIndex(name, StorageTypeDisk, 1)
	require.NoError(t, err)
	require.NoError(t, StoreIndex(index))
	t.Cleanup(func() { assert.NoError(t, DeleteIndex(name)) })

	now := time.Now()
	atomic.StoreInt64(&anyShard(index).shards[0].ref.Stats.DocTimeMax, now.Add(-time.Hour).UnixNano())
	tests := []struct {
		name      string
		tier      string
		lifecycle *meta.IndexLifecycle
		action    string
		next      string
	}{
		{name: "no warm phase", tier: TierHot, lifecycle: &meta.IndexLifecycle{ColdAfter: "30d"}, action: ILMPhaseCold, next: TierCold},
		{name: "already warm", tier: TierWarm, lifecycle: &meta.IndexLifecycle{WarmAfter: "7d", ColdAfter: "30d"}, action: ILMPhaseCold, next: TierCold},
		{name: "already cold", tier: TierCold, lifecycle: &meta.IndexLifecycle{ColdAfter: "30d", DeleteAfter: "90d"}, action: ILMPhaseDelete},
		{name: "no phase", tier: TierHot, lifecycle: &meta.IndexLifecycle{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index.lock.Lock()
			index.ref.Settings.Tier = tt.tier
			index.lock.Unlock()
			age, action, next := ExplainTiering(index, tt.lifecycle, now)
			assert.InDelta(t, time.Hour, age, float64(time.Minute), "the age comes from the second layer shard")
			assert.Equal(t, tt.action, action)
			assert.Equal(t, tt.next, next)
		})
	}
	index.lock.Lock()
	index.ref.Settings.Tier = ""
	index.lock.Unlock()
}
