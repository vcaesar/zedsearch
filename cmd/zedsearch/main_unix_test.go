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

//go:build !windows

package main

import (
	"bytes"
	"net"
	"net/http"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shutdownSignaled guards TestShutdown under -count>1: shutdown keeps its
// handler waiting for a second signal, which would exit the test binary.
var shutdownSignaled bool

func TestShutdown(t *testing.T) {
	if shutdownSignaled {
		t.Skip("shutdown handler already installed in this process")
	}
	shutdownSignaled = true
	graceful := make(chan bool, 1)
	done := shutdown(func(grace bool, done chan<- struct{}) {
		graceful <- grace
		done <- struct{}{}
	})
	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGTERM))

	select {
	case grace := <-graceful:
		assert.True(t, grace)
	case <-time.After(10 * time.Second):
		t.Fatal("stop was not called")
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("done was not closed")
	}
}

// syncBuffer collects subprocess output while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return strconv.Itoa(port)
}

func TestMainServe(t *testing.T) {
	tests := []struct {
		name string
		sig  syscall.Signal
	}{
		{"graceful SIGTERM", syscall.SIGTERM},
		{"graceful SIGINT", syscall.SIGINT},
		{"immediate SIGQUIT", syscall.SIGQUIT},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := freePort(t)
			out := new(syncBuffer)
			cmd := mainCommand(t, "", "ZED_SERVER_ADDRESS=127.0.0.1", "ZED_SERVER_PORT="+port)
			cmd.Stdout = out
			cmd.Stderr = out
			require.NoError(t, cmd.Start())
			var waitErr error
			exited := make(chan struct{})
			go func() {
				waitErr = cmd.Wait()
				close(exited)
			}()
			t.Cleanup(func() {
				select {
				case <-exited:
				default:
					_ = cmd.Process.Kill()
					<-exited
				}
			})

			client := &http.Client{Timeout: time.Second}
			url := "http://127.0.0.1:" + port + "/healthz"
			ready := false
			for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
				resp, err := client.Get(url)
				if err == nil {
					_ = resp.Body.Close()
					ready = resp.StatusCode == http.StatusOK
					break
				}
			}
			require.True(t, ready, "server not ready:\n%s", out.String())

			require.NoError(t, cmd.Process.Signal(tt.sig))
			select {
			case <-exited:
				require.NoError(t, waitErr, out.String())
			case <-time.After(60 * time.Second):
				t.Fatalf("server did not exit:\n%s", out.String())
			}
			logs := out.String()
			assert.Contains(t, logs, "Listen on 127.0.0.1:"+port)
			assert.Contains(t, logs, "Index closed")
			assert.Contains(t, logs, "Metadata closed")
			assert.Contains(t, logs, "Server shutdown ok")
		})
	}
}

func TestMainListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	cmd := mainCommand(t, "", "ZED_SERVER_ADDRESS=127.0.0.1", "ZED_SERVER_PORT="+port)
	out, err := cmd.CombinedOutput()
	assert.Error(t, err)
	assert.Contains(t, string(out), "Listen on 127.0.0.1:"+port)
}
