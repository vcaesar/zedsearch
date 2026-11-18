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

package directory

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/vcaesar/zedsearch/pkg/config"
)

func TestNewMinioStore(t *testing.T) {
	_, err := newMinioStore(config.S3Config{})
	assert.Error(t, err, "bucket required")

	s, err := newMinioStore(config.S3Config{Endpoint: "localhost:9000", Bucket: "zed", AccessKeyID: "a", SecretAccessKey: "b"})
	assert.NoError(t, err)
	assert.Equal(t, "zed", s.bucket)

	s, err = newMinioStore(config.S3Config{Endpoint: "s3.amazonaws.com", Bucket: "zed", UseSSL: true, Region: "us-east-1"})
	assert.NoError(t, err, "credential chain when keys are empty")
	assert.NotNil(t, s.client)

	_, err = newMinioStore(config.S3Config{Endpoint: "http://bad endpoint", Bucket: "zed"})
	assert.Error(t, err)
}

// TestGetS3ConfigUnconfigured uses a local, unconfigured backend so it never
// touches a real bucket configured through the environment.
func TestGetS3ConfigUnconfigured(t *testing.T) {
	b := &backend{newStore: func() (objectStore, error) { return newMinioStore(config.S3Config{}) }}
	_, err := getObjectConfig(b, "idx", nil)
	assert.Error(t, err)
	assert.Error(t, removeObjectIndex(b, "idx"))
}

func TestNewMinioStoreConfig(t *testing.T) {
	_, err := newMinioStore(config.S3Config{Bucket: "zed", AccessKeyID: "a"})
	assert.Error(t, err, "secret missing")
	_, err = newMinioStore(config.S3Config{Bucket: "zed", SecretAccessKey: "b"})
	assert.Error(t, err, "key id missing")

	s, err := newMinioStore(config.S3Config{Bucket: "zed", UseSSL: true, AccessKeyID: "a", SecretAccessKey: "b"})
	assert.NoError(t, err)
	assert.Equal(t, "https://s3.amazonaws.com", s.client.EndpointURL().String(), "empty endpoint defaults to AWS")

	s, err = newMinioStore(config.S3Config{Endpoint: "http://minio:9000/", Bucket: "zed", UseSSL: true, AccessKeyID: "a", SecretAccessKey: "b"})
	assert.NoError(t, err, "scheme overrides use_ssl")
	assert.Equal(t, "http://minio:9000", s.client.EndpointURL().String())
}

func TestSplitEndpoint(t *testing.T) {
	for _, tt := range []struct {
		in, host string
		secure   bool
		want     bool
	}{
		{"minio:9000", "minio:9000", true, true},
		{"minio:9000", "minio:9000", false, false},
		{"https://s3.example.com", "s3.example.com", false, true},
		{"http://s3.example.com/", "s3.example.com", true, false},
	} {
		t.Run(tt.in, func(t *testing.T) {
			host, secure := splitEndpoint(tt.in, tt.secure)
			assert.Equal(t, tt.host, host)
			assert.Equal(t, tt.want, secure)
		})
	}
}

// fakeS3 is a minimal path-style S3 server backed by a MemoryObjectStore.
func fakeS3(t *testing.T, mem *MemoryObjectStore, status func(r *http.Request) int) *minioStore {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code := status(r); code != 0 {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(code)
			if code == http.StatusNotFound {
				fmt.Fprint(w, `<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`)
			} else {
				fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>denied</Message></Error>`)
			}
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/zed/")
		ctx := r.Context()
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
			objs, err := mem.List(ctx, r.URL.Query().Get("prefix"))
			if !assert.NoError(t, err) {
				return
			}
			var b strings.Builder
			b.WriteString(`<ListBucketResult><Name>zed</Name><IsTruncated>false</IsTruncated>`)
			for _, o := range objs {
				fmt.Fprintf(&b, `<Contents><Key>%s</Key><Size>%d</Size></Contents>`, o.Key, o.Size)
			}
			b.WriteString(`</ListBucketResult>`)
			fmt.Fprint(w, b.String())
		case r.Method == http.MethodGet || r.Method == http.MethodHead:
			rc, err := mem.Get(ctx, key)
			if err != nil {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`)
				return
			}
			data, err := io.ReadAll(rc)
			assert.NoError(t, errors.Join(err, rc.Close()))
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Header().Set("ETag", `"etag"`)
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			if r.Method == http.MethodGet {
				_, err = w.Write(data)
				assert.NoError(t, err)
			}
		case r.Method == http.MethodPut:
			assert.NoError(t, mem.Put(ctx, key, decodeAWSChunked(t, r), -1))
			w.Header().Set("ETag", `"etag"`)
		case r.Method == http.MethodDelete:
			assert.NoError(t, mem.Delete(ctx, key))
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	s, err := newMinioStore(config.S3Config{Endpoint: srv.URL, Bucket: "zed", Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "b"})
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	return s
}

func TestMinioStoreHTTP(t *testing.T) {
	mem := NewMemoryObjectStore()
	var fail func(r *http.Request) int
	s := fakeS3(t, mem, func(r *http.Request) int {
		if fail != nil {
			return fail(r)
		}
		return 0
	})
	ctx := context.Background()

	assert.NoError(t, s.Put(ctx, "idx/000000000001.seg", strings.NewReader("one"), 3))
	objs, err := s.List(ctx, "idx/")
	assert.NoError(t, err)
	assert.Equal(t, []objectInfo{{Key: "idx/000000000001.seg", Size: 3}}, objs)

	rc, err := s.Get(ctx, "idx/000000000001.seg")
	if assert.NoError(t, err) {
		data, err := io.ReadAll(rc)
		assert.NoError(t, errors.Join(err, rc.Close()))
		assert.Equal(t, "one", string(data))
	}

	_, err = s.Get(ctx, "idx/missing.seg")
	assert.ErrorIs(t, err, fs.ErrNotExist)

	assert.NoError(t, s.Delete(ctx, "idx/000000000001.seg"))

	t.Run("delete of a missing key is not an error", func(t *testing.T) {
		fail = func(r *http.Request) int {
			if r.Method == http.MethodDelete {
				return http.StatusNotFound
			}
			return 0
		}
		defer func() { fail = nil }()
		assert.NoError(t, s.Delete(ctx, "idx/gone.seg"))
	})

	t.Run("access denied is not a missing object", func(t *testing.T) {
		fail = func(*http.Request) int { return http.StatusForbidden }
		defer func() { fail = nil }()
		_, err := s.Get(ctx, "idx/000000000001.seg")
		assert.Error(t, err)
		assert.NotErrorIs(t, err, fs.ErrNotExist)
		_, err = s.List(ctx, "idx/")
		assert.Error(t, err)
		assert.Error(t, s.Put(ctx, "idx/x.seg", strings.NewReader("x"), 1))
		assert.Error(t, s.Delete(ctx, "idx/x.seg"))
	})

	t.Run("cancelled list fails instead of returning a partial result", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := s.List(cctx, "idx/")
		assert.ErrorIs(t, err, context.Canceled)
	})
}

// decodeAWSChunked returns the payload of a PUT, unwrapping the aws-chunked
// streaming signature encoding minio uses over plain HTTP.
func decodeAWSChunked(t *testing.T, r *http.Request) io.Reader {
	t.Helper()
	if !strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
		return r.Body
	}
	br := bufio.NewReader(r.Body)
	var out bytes.Buffer
	for {
		line, err := br.ReadString('\n')
		if !assert.NoError(t, err) {
			return &out
		}
		size, err := strconv.ParseInt(strings.SplitN(strings.TrimSpace(line), ";", 2)[0], 16, 64)
		if !assert.NoError(t, err) || size == 0 {
			return &out
		}
		_, err = io.CopyN(&out, br, size)
		assert.NoError(t, err)
		_, err = br.Discard(2) // chunk CRLF
		assert.NoError(t, err)
	}
}
