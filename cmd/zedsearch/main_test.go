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
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/cluster"
	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
)

const (
	// helperEnv makes the test binary run main() with its value as arguments.
	helperEnv = "ZED_TEST_MAIN_ARGS"
	// setupEnv picks a failure for setupMain to inject before main() runs.
	setupEnv = "ZED_TEST_MAIN_SETUP"
)

func TestMain(m *testing.M) {
	if args, ok := os.LookupEnv(helperEnv); ok {
		os.Args = append([]string{"zedsearch"}, strings.Fields(args)...)
		setupMain(os.Getenv(setupEnv))
		main()
		return
	}
	os.Exit(m.Run())
}

// faultyListener fails Accept or Close of a real listener.
type faultyListener struct {
	net.Listener
	acceptErr, closeErr error
}

func (l faultyListener) Accept() (net.Conn, error) {
	if l.acceptErr != nil {
		return nil, l.acceptErr
	}
	return l.Listener.Accept()
}

func (l faultyListener) Close() error {
	return errors.Join(l.Listener.Close(), l.closeErr)
}

func faultyListen(acceptErr, closeErr error) func(string) (net.Listener, error) {
	return func(addr string) (net.Listener, error) {
		ln, err := listen(addr)
		if err != nil {
			return nil, err
		}
		return faultyListener{ln, acceptErr, closeErr}, nil
	}
}

// setupMain injects failures that env config alone cannot reach: env
// cluster mode would also move metadata to etcd.
func setupMain(mode string) {
	switch mode {
	case "":
	case "cluster":
		config.Global.Cluster.Enable = true
		config.Global.Etcd.Endpoints = nil
	case "replication":
		if err := cluster.StartWith(context.Background(), cluster.NewMemoryStore()); err != nil {
			panic(err)
		}
		config.Global.Cluster.Replication = "bogus"
	case "accept":
		listener = faultyListen(errors.New("accept failed"), nil)
	case "close":
		listener = faultyListen(nil, errors.New("close failed"))
	default:
		panic("unknown " + setupEnv + ": " + mode)
	}
}

// mainCommand returns the test binary set up to run main() with args
// against a fresh data dir. Inherited TLS settings are dropped and the
// working dir is empty, so no conf/zed.toml can switch the server to TLS.
func mainCommand(t *testing.T, args string, env ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Dir = t.TempDir()
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ZED_SERVER_TLS_CERTIFICATE_FILE=") &&
			!strings.HasPrefix(kv, "ZED_SERVER_TLS_KEY_FILE=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env,
		helperEnv+"="+args,
		"ZED_DATA_PATH="+t.TempDir(),
		"ZED_FIRST_ADMIN_USER=admin",
		"ZED_FIRST_ADMIN_PASSWORD=Complexpass#123",
		"ZED_TELEMETRY=false",
		"ZED_SENTRY=false",
		"ZED_PROFILER=false",
		"ZED_LOG_LEVEL=info",
	)
	cmd.Env = append(cmd.Env, env...)
	return cmd
}

func TestMainVersion(t *testing.T) {
	out, err := mainCommand(t, "version").Output()
	require.NoError(t, err)
	assert.Equal(t, "zedsearch version "+meta.Version+"\n", string(out))
}

func TestMainStartupErrors(t *testing.T) {
	tests := []struct {
		name string
		env  []string
		want string
	}{
		{"invalid tracing endpoint", []string{"ZED_TRACING_ENABLE=true", "ZED_TRACING_ENDPOINT=ftp://127.0.0.1"}, "Tracing start"},
		{"cluster without etcd", []string{setupEnv + "=cluster"}, "Cluster start"},
		{"invalid replication mode", []string{setupEnv + "=replication"}, "Replication start"},
		{"accept fails", []string{setupEnv + "=accept"}, "Server closed unexpect"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := append([]string{"ZED_SERVER_ADDRESS=127.0.0.1", "ZED_SERVER_PORT=0"}, tt.env...)
			out, err := mainCommand(t, "", env...).CombinedOutput()
			assert.Error(t, err, string(out))
			assert.Contains(t, string(out), tt.want)
		})
	}
}

// restore resets each config.Global field after the test; copying the whole
// struct would race with background readers started by package init.
func restore[T any](t *testing.T, fields ...*T) {
	t.Helper()
	for _, p := range fields {
		old := *p
		t.Cleanup(func() { *p = old })
	}
}

func restoreTLS(t *testing.T) {
	t.Helper()
	restore(t, &config.Global.ServerTLSCertificateFile, &config.Global.ServerTLSKeyFile)
}

// writeCert writes a self-signed localhost certificate and key into dir.
func writeCert(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	return writeCertSerial(t, dir, 1)
}

// writeCertSerial writes a self-signed localhost certificate with serial and its key into dir.
func writeCertSerial(t *testing.T, dir string, serial int64) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	return certFile, keyFile
}

// acceptHandshake completes the TLS handshake for one connection on ln.
func acceptHandshake(ln net.Listener) <-chan error {
	errc := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			errc <- err
			return
		}
		defer func() { _ = conn.Close() }()
		errc <- conn.(*tls.Conn).Handshake()
	}()
	return errc
}

func TestListen(t *testing.T) {
	t.Run("plain tcp without certificate", func(t *testing.T) {
		restoreTLS(t)
		certFile, _ := writeCert(t, t.TempDir())
		for _, files := range [][2]string{{"", ""}, {certFile, ""}, {"", certFile}} {
			config.Global.ServerTLSCertificateFile = files[0]
			config.Global.ServerTLSKeyFile = files[1]
			ln, err := listen("127.0.0.1:0")
			require.NoError(t, err)
			_, isTCP := ln.(*net.TCPListener)
			assert.True(t, isTCP, "want plain tcp listener for %v", files)

			go func() {
				if conn, err := ln.Accept(); err == nil {
					_ = conn.Close()
				}
			}()
			conn, err := net.Dial("tcp", ln.Addr().String())
			require.NoError(t, err)
			require.NoError(t, conn.Close())
			require.NoError(t, ln.Close())
		}
	})

	t.Run("tls with certificate", func(t *testing.T) {
		restoreTLS(t)
		config.Global.ServerTLSCertificateFile, config.Global.ServerTLSKeyFile = writeCert(t, t.TempDir())
		ln, err := listen("127.0.0.1:0")
		require.NoError(t, err)
		defer func() { _ = ln.Close() }()

		errc := acceptHandshake(ln)
		conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // self-signed test certificate
			NextProtos:         []string{"http/1.1"},
		})
		require.NoError(t, err)
		state := conn.ConnectionState()
		require.NoError(t, conn.Close())
		require.NoError(t, <-errc)

		assert.GreaterOrEqual(t, state.Version, uint16(tls.VersionTLS12))
		assert.Equal(t, "http/1.1", state.NegotiatedProtocol)
		assert.Contains(t, zhttp.TLSNextProtos, state.NegotiatedProtocol)
	})

	t.Run("tls rejects versions below 1.2", func(t *testing.T) {
		restoreTLS(t)
		config.Global.ServerTLSCertificateFile, config.Global.ServerTLSKeyFile = writeCert(t, t.TempDir())
		ln, err := listen("127.0.0.1:0")
		require.NoError(t, err)
		defer func() { _ = ln.Close() }()

		errc := acceptHandshake(ln)
		_, err = tls.Dial("tcp", ln.Addr().String(), &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // self-signed test certificate
			MinVersion:         tls.VersionTLS10,
			MaxVersion:         tls.VersionTLS11,
		})
		assert.Error(t, err)
		assert.Error(t, <-errc)
	})

	t.Run("invalid certificate files", func(t *testing.T) {
		restoreTLS(t)
		dir := t.TempDir()
		config.Global.ServerTLSCertificateFile = filepath.Join(dir, "missing.pem")
		config.Global.ServerTLSKeyFile = filepath.Join(dir, "missing.key")
		_, err := listen("127.0.0.1:0")
		assert.Error(t, err)

		bad := filepath.Join(dir, "bad.pem")
		require.NoError(t, os.WriteFile(bad, []byte("not a pem"), 0o600))
		config.Global.ServerTLSCertificateFile = bad
		config.Global.ServerTLSKeyFile = bad
		_, err = listen("127.0.0.1:0")
		assert.Error(t, err)
	})

	t.Run("address in use", func(t *testing.T) {
		restoreTLS(t)
		config.Global.ServerTLSCertificateFile = ""
		config.Global.ServerTLSKeyFile = ""
		ln, err := listen("127.0.0.1:0")
		require.NoError(t, err)
		defer func() { _ = ln.Close() }()

		_, err = listen(ln.Addr().String())
		assert.Error(t, err)
	})
}

func TestTelemetryDisabled(t *testing.T) {
	restore(t, &config.Global.TelemetryEnable)
	config.Global.TelemetryEnable = false
	assert.NotPanics(t, telemetry)
}

func TestSentries(t *testing.T) {
	tests := []struct {
		name   string
		enable bool
		dsn    string
	}{
		{"disabled", false, "https://key@127.0.0.1/1"},
		{"enabled without dsn", true, ""},
		{"enabled with invalid dsn", true, "::not a dsn::"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore(t, &config.Global.SentryEnable)
			restore(t, &config.Global.SentryDSN)
			config.Global.SentryEnable = tt.enable
			config.Global.SentryDSN = tt.dsn
			assert.NotPanics(t, sentries)
		})
	}
}

func TestProfiling(t *testing.T) {
	tests := []struct {
		name   string
		enable bool
		server string
	}{
		{"disabled", false, "http://127.0.0.1:1"},
		{"enabled without server", true, ""},
		// the invalid tag in the app name fails Start before any profiler runs
		{"invalid profile id", true, "http://127.0.0.1:1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore(t, &config.Global.ProfilerEnable)
			restore(t, &config.Global.ProfilerServer)
			restore(t, &config.Global.ProfilerFriendlyProfileID)
			config.Global.ProfilerEnable = tt.enable
			config.Global.ProfilerServer = tt.server
			config.Global.ProfilerFriendlyProfileID = "x{bad-key=v}"
			assert.NotPanics(t, profiling)
		})
	}
}

// servedSerial returns the serial of the certificate r serves.
func servedSerial(t *testing.T, r *certReloader) int64 {
	t.Helper()
	cert, err := r.GetCertificate(nil)
	require.NoError(t, err)
	require.NotNil(t, cert.Leaf)
	return cert.Leaf.SerialNumber.Int64()
}

// touch moves the modification time of files forward, as a rewrite does.
func touch(t *testing.T, at time.Time, files ...string) {
	t.Helper()
	for _, f := range files {
		require.NoError(t, os.Chtimes(f, at, at))
	}
}

func TestCertReloader(t *testing.T) {
	t.Run("missing files", func(t *testing.T) {
		dir := t.TempDir()
		certFile, keyFile := writeCert(t, dir)
		_, err := newCertReloader(filepath.Join(dir, "missing.pem"), keyFile)
		assert.Error(t, err)
		_, err = newCertReloader(certFile, filepath.Join(dir, "missing.key"))
		assert.Error(t, err)
	})

	t.Run("invalid pair", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.pem")
		require.NoError(t, os.WriteFile(bad, []byte("not a pem"), 0o600))
		_, err := newCertReloader(bad, bad)
		assert.Error(t, err)
	})

	t.Run("reloads a renewed certificate", func(t *testing.T) {
		dir := t.TempDir()
		certFile, keyFile := writeCertSerial(t, dir, 1)
		r, err := newCertReloader(certFile, keyFile)
		require.NoError(t, err)
		assert.Equal(t, int64(1), servedSerial(t, r))
		assert.Equal(t, int64(1), servedSerial(t, r), "unchanged files keep the pair")

		writeCertSerial(t, dir, 2)
		touch(t, time.Now().Add(time.Minute), certFile, keyFile)
		assert.Equal(t, int64(2), servedSerial(t, r))
	})

	t.Run("reloads replaced files with unchanged modification times", func(t *testing.T) {
		dir := t.TempDir()
		certFile, keyFile := writeCertSerial(t, dir, 1)
		r, err := newCertReloader(certFile, keyFile)
		require.NoError(t, err)
		assert.Equal(t, int64(1), servedSerial(t, r))

		next := t.TempDir()
		newCert, newKey := writeCertSerial(t, next, 2)
		for src, dst := range map[string]string{newCert: certFile, newKey: keyFile} {
			info, err := os.Stat(dst)
			require.NoError(t, err)
			touch(t, info.ModTime(), src)
			require.NoError(t, os.Rename(src, dst))
		}
		assert.Equal(t, int64(2), servedSerial(t, r))
	})

	t.Run("keeps the previous certificate when a rewrite fails", func(t *testing.T) {
		dir := t.TempDir()
		certFile, keyFile := writeCertSerial(t, dir, 1)
		r, err := newCertReloader(certFile, keyFile)
		require.NoError(t, err)

		keyInfo, err := os.Stat(keyFile)
		require.NoError(t, err)
		failedAt := time.Now().Add(time.Minute)
		require.NoError(t, os.WriteFile(certFile, []byte("half written"), 0o600))
		touch(t, failedAt, certFile)
		assert.Equal(t, int64(1), servedSerial(t, r))

		// the repaired pair keeps the times seen at the failure: only a retry,
		// not a cached failed state, serves it
		writeCertSerial(t, dir, 2)
		touch(t, failedAt, certFile)
		touch(t, keyInfo.ModTime(), keyFile)
		assert.Equal(t, int64(2), servedSerial(t, r), "the failure is retried, not cached")

		require.NoError(t, os.Rename(keyFile, keyFile+".old"))
		assert.Equal(t, int64(2), servedSerial(t, r), "a missing file keeps the pair")
		require.NoError(t, os.Rename(keyFile+".old", keyFile))

		writeCertSerial(t, dir, 3)
		touch(t, time.Now().Add(2*time.Minute), certFile, keyFile)
		assert.Equal(t, int64(3), servedSerial(t, r))
		assert.Empty(t, r.failed)
	})
}

func TestListenReloadsCertificate(t *testing.T) {
	restoreTLS(t)
	dir := t.TempDir()
	certFile, keyFile := writeCertSerial(t, dir, 1)
	config.Global.ServerTLSCertificateFile, config.Global.ServerTLSKeyFile = certFile, keyFile
	ln, err := listen("127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	serial := func() int64 {
		errc := acceptHandshake(ln)
		conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // self-signed test certificate
		})
		require.NoError(t, err)
		peer := conn.ConnectionState().PeerCertificates[0]
		require.NoError(t, conn.Close())
		require.NoError(t, <-errc)
		return peer.SerialNumber.Int64()
	}
	assert.Equal(t, int64(1), serial())

	writeCertSerial(t, dir, 2)
	touch(t, time.Now().Add(time.Minute), certFile, keyFile)
	assert.Equal(t, int64(2), serial())
}
