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
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A burst of concurrent calls to one node reuses its connections: the
// default transport would keep 2 of them and dial the rest again.
func TestTransportReusesConnections(t *testing.T) {
	const burst = 16
	require.Greater(t, peerIdleConns, burst)
	assert.Same(t, Transport(), Transport())

	var dialed atomic.Int64
	mux := http.NewServeMux()
	server := httptest.NewUnstartedServer(mux)
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			dialed.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)

	client := &http.Client{Transport: Transport(), Timeout: 10 * time.Second}
	var waveID int
	wave := func() {
		waveID++
		path := "/" + strconv.Itoa(waveID)
		arrived := make(chan struct{}, burst)
		release := make(chan struct{})
		// A late request still uses the channels for its own wave.
		mux.HandleFunc(path, func(_ http.ResponseWriter, r *http.Request) {
			arrived <- struct{}{}
			select {
			case <-release: // hold the request so the burst needs one connection each
			case <-r.Context().Done(): // the client gave up, server.Close must not wait for it
			}
		})

		failed := make(chan struct{}, burst)
		var wg sync.WaitGroup
		for range burst {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := client.Get(server.URL + path)
				if !assert.NoError(t, err) {
					failed <- struct{}{}
					return
				}
				_, err = io.Copy(io.Discard, resp.Body)
				assert.NoError(t, err)
				assert.NoError(t, resp.Body.Close())
			}()
		}
		// every request is held by the handler or failed: the timeout bounds both
		for range burst {
			select {
			case <-arrived:
			case <-failed:
			}
		}
		close(release)
		wg.Wait()
	}

	wave()
	require.EqualValues(t, burst, dialed.Load())
	// a connection goes back to the pool just after its body is read
	require.Eventually(t, func() bool {
		before := dialed.Load()
		wave()
		return dialed.Load() == before
	}, 10*time.Second, 10*time.Millisecond, "a later burst must not dial")
}
