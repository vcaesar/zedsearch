//go:build !windows

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

package main

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
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
	closed := []string{"Index closed", "Metadata closed", "Server shutdown ok"}
	// fiber's immediate close drops the listener error while a connection is open
	closeErr := closed
	if zhttp.Backend == "gin" {
		closeErr = append([]string{"Server Close", "close failed"}, closed...)
	}
	tests := []struct {
		name    string
		sig     syscall.Signal
		env     []string
		wantErr bool
		want    []string
	}{
		{"graceful SIGTERM", syscall.SIGTERM, nil, false, closed},
		{"graceful SIGINT", syscall.SIGINT, nil, false, closed},
		{"immediate SIGQUIT", syscall.SIGQUIT, nil, false, closed},
		{
			"tracing and profiling to unreachable servers", syscall.SIGTERM,
			[]string{
				"ZED_TRACING_ENABLE=true", "ZED_TRACING_ENDPOINT=http://127.0.0.1:1/v1/traces",
				"ZED_PROFILER=true", "ZED_PROFILER_SERVER=http://127.0.0.1:1",
			},
			false, append([]string{"traces export"}, closed...),
		},
		{"graceful shutdown error", syscall.SIGTERM, []string{setupEnv + "=close"}, true, []string{"Server Shutdown", "close failed"}},
		{"immediate close error", syscall.SIGQUIT, []string{setupEnv + "=close"}, false, closeErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := freePort(t)
			out := new(syncBuffer)
			env := append([]string{"ZED_SERVER_ADDRESS=127.0.0.1", "ZED_SERVER_PORT=" + port}, tt.env...)
			cmd := mainCommand(t, "", env...)
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
			case <-time.After(60 * time.Second):
				t.Fatalf("server did not exit:\n%s", out.String())
			}
			logs := out.String()
			if tt.wantErr {
				require.Error(t, waitErr, logs)
			} else {
				require.NoError(t, waitErr, logs)
			}
			assert.Contains(t, logs, "Listen on 127.0.0.1:"+port)
			for _, want := range tt.want {
				assert.Contains(t, logs, want)
			}
		})
	}
}

// fakeToolchain puts npm and go stand-ins first on PATH and a module root
// in dir, so the web command builds without a real toolchain. The fake go
// build copies binary (empty when "") to its -o path.
func fakeToolchain(t *testing.T, dir, binary string) []string {
	t.Helper()
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "npm"), []byte("#!/bin/sh\necho \"fake npm $*\"\n"), 0o755))
	goScript := `#!/bin/sh
while [ $# -gt 0 ]; do
	if [ "$1" = "-o" ]; then out="$2"; fi
	shift
done
if [ -n "$FAKE_WEB_BINARY" ]; then cp "$FAKE_WEB_BINARY" "$out"; else : > "$out"; fi
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "go"), []byte(goScript), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/vcaesar/zedsearch\n"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "frontend"), 0o755))
	return []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "FAKE_WEB_BINARY=" + binary}
}

func TestMainWeb(t *testing.T) {
	front, err := zedsearch.GetFrontendAssets()
	require.NoError(t, err)
	if front != nil {
		t.Skip("assets are embedded: the web command serves in place instead of building")
	}

	t.Run("requires a source checkout", func(t *testing.T) {
		out, err := mainCommand(t, "web").CombinedOutput()
		assert.Error(t, err)
		assert.Contains(t, string(out), "build web binary")
		assert.Contains(t, string(out), "source checkout")
	})

	t.Run("built binary fails to start", func(t *testing.T) {
		cmd := mainCommand(t, "web")
		cmd.Env = append(cmd.Env, fakeToolchain(t, cmd.Dir, "")...)
		out, err := cmd.CombinedOutput()
		assert.Error(t, err)
		assert.Contains(t, string(out), "fake npm ci")
		assert.Contains(t, string(out), "fake npm run build")
		assert.Contains(t, string(out), "run web binary")
		assert.FileExists(t, filepath.Join(cmd.Dir, "test", "zedsearch-web"))
	})

	t.Run("replaced by the built binary", func(t *testing.T) {
		binary := filepath.Join(t.TempDir(), "zedsearch-web")
		require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\necho \"web binary started: $*\"\n"), 0o755))
		cmd := mainCommand(t, "web --flag value")
		cmd.Env = append(cmd.Env, fakeToolchain(t, cmd.Dir, binary)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		assert.Contains(t, string(out), "web binary started: --flag value")
	})
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
