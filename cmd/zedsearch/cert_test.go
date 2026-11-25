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

package main

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
)

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

	t.Run("keeps the previous certificate when a rewrite fails", func(t *testing.T) {
		dir := t.TempDir()
		certFile, keyFile := writeCertSerial(t, dir, 1)
		r, err := newCertReloader(certFile, keyFile)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(certFile, []byte("half written"), 0o600))
		touch(t, time.Now().Add(time.Minute), certFile)
		assert.Equal(t, int64(1), servedSerial(t, r))
		assert.Equal(t, int64(1), servedSerial(t, r), "the failure is retried, not cached")

		require.NoError(t, os.Rename(keyFile, keyFile+".old"))
		assert.Equal(t, int64(1), servedSerial(t, r), "a missing file keeps the pair")
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
