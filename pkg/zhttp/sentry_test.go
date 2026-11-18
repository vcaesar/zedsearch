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

package zhttp

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTransport records events instead of sending them.
type mockTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (t *mockTransport) Configure(sentry.ClientOptions)        {}
func (t *mockTransport) Flush(time.Duration) bool              { return true }
func (t *mockTransport) FlushWithContext(context.Context) bool { return true }
func (t *mockTransport) Close()                                {}
func (t *mockTransport) SendEvent(e *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, e)
}

func TestRecoveredReportsToSentry(t *testing.T) {
	transport := &mockTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{Dsn: "https://key@sentry.example.com/1", Transport: transport})
	require.NoError(t, err)
	hub := sentry.CurrentHub()
	prev := hub.Client()
	hub.BindClient(client)
	t.Cleanup(func() { hub.BindClient(prev) })

	e := New()
	e.GET("/boom", func(c Context) { panic("sentry boom") })
	assert.Equal(t, http.StatusInternalServerError, do(e, http.MethodGet, "/boom", nil).Code)

	transport.mu.Lock()
	defer transport.mu.Unlock()
	require.Len(t, transport.events, 1)
	assert.Equal(t, "sentry boom", transport.events[0].Message)
}
