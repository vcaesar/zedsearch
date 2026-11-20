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
	"sync"
	"testing"
	"time"

	"github.com/segmentio/analytics-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
)

// fakeSegment records the messages enqueued instead of sending them.
type fakeSegment struct {
	mu   sync.Mutex
	msgs []analytics.Message
}

func (f *fakeSegment) Enqueue(m analytics.Message) error {
	f.mu.Lock()
	f.msgs = append(f.msgs, m)
	f.mu.Unlock()
	return nil
}

func (f *fakeSegment) Close() error { return nil }

func (f *fakeSegment) len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.msgs)
}

func TestTelemetry_Enabled(t *testing.T) {
	fake := &fakeSegment{}
	oldClient, oldEnable := meta.SEGMENT_CLIENT, config.Global.TelemetryEnable
	meta.SEGMENT_CLIENT = fake
	config.Global.TelemetryEnable = true
	t.Cleanup(func() {
		meta.SEGMENT_CLIENT = oldClient
		config.Global.TelemetryEnable = oldEnable
	})

	tel := newTelemetry()
	tel.Instance()
	tel.Event("test_event", map[string]interface{}{"key": "value"})
	tel.HeartBeat()
	require.Eventually(t, func() bool { return fake.len() == 3 }, 10*time.Second, 5*time.Millisecond)
	close(tel.events)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	identify, ok := fake.msgs[0].(analytics.Identify)
	require.True(t, ok)
	assert.Equal(t, tel.GetInstanceID(), identify.UserId)
	track, ok := fake.msgs[1].(analytics.Track)
	require.True(t, ok)
	assert.Equal(t, "test_event", track.Event)
	assert.Equal(t, "value", track.Properties["key"])
}

func TestTelemetry_GetInstanceID(t *testing.T) {
	require.NoError(t, metadata.KV.Delete("instance_id"))

	created := new(telemetry)
	id := created.GetInstanceID()
	assert.NotEmpty(t, id, "a new id is created and stored")
	assert.Equal(t, id, created.GetInstanceID(), "and cached")

	loaded := new(telemetry)
	assert.Equal(t, id, loaded.GetInstanceID(), "the stored id is loaded")
}

func TestTelemetry_GetIndexSizeMissing(t *testing.T) {
	assert.Zero(t, Telemetry.GetIndexSize("TestTelemetry_GetIndexSizeMissing.index_1"))
}

func TestTelemetry_Cron(t *testing.T) {
	old := config.Global.TelemetryEnable
	t.Cleanup(func() { config.Global.TelemetryEnable = old })

	config.Global.TelemetryEnable = false
	assert.NotPanics(t, new(telemetry).Cron)
	config.Global.TelemetryEnable = true
	assert.NotPanics(t, new(telemetry).Cron)
}
