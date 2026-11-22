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
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests drive HTTPContext directly, as handler tests do, so they run
// under every backend.

var errWrite = errors.New("write failed")

// failWriter is a ResponseWriter whose body writes fail.
type failWriter struct{ *httptest.ResponseRecorder }

func (w failWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestHTTPContextRequest(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/req/a?q=x&flag&empty=", strings.NewReader("payload"))
	r.SetBasicAuth("admin", "secret")
	r.Header.Set("X-Test", "yes")
	c := NewHTTPContext(httptest.NewRecorder(), r)

	assert.Equal(t, http.MethodPost, c.Method())
	assert.Equal(t, "/req/a", c.Path())
	assert.Equal(t, "/req/a?q=x&flag&empty=", c.RequestURI())
	assert.Equal(t, "x", c.Query("q"))
	assert.Empty(t, c.Query("missing"))
	assert.Equal(t, "x", c.DefaultQuery("q", "dflt"))
	assert.Empty(t, c.DefaultQuery("empty", "dflt"))
	assert.Equal(t, "dflt", c.DefaultQuery("missing", "dflt"))
	v, ok := c.GetQuery("flag")
	assert.True(t, ok)
	assert.Empty(t, v)
	assert.Equal(t, "yes", c.GetHeader("X-Test"))
	user, pass, ok := c.BasicAuth()
	assert.True(t, ok)
	assert.Equal(t, "admin:secret", user+":"+pass)
	assert.Empty(t, c.FullPath())
	assert.Empty(t, c.Param("id"))

	c.SetParam("id", "1")
	c.SetParam("id", "2")
	assert.Equal(t, "2", c.Param("id"))

	body, err := io.ReadAll(c.Body())
	require.NoError(t, err)
	assert.Equal(t, "payload", string(body))
	c.SetBody([]byte("new"))
	assert.Equal(t, int64(3), c.Request.ContentLength)
	body, err = io.ReadAll(c.Body())
	require.NoError(t, err)
	assert.Equal(t, "new", string(body))

	c.SetContext(context.WithValue(c.Context(), ctxKey{}, "v"))
	assert.Equal(t, "v", c.Context().Value(ctxKey{}))
}

func TestHTTPContextNilBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Body = nil
	body, err := io.ReadAll(NewHTTPContext(httptest.NewRecorder(), r).Body())
	require.NoError(t, err)
	assert.Empty(t, body)
}

func TestHTTPContextResponse(t *testing.T) {
	tests := []struct {
		name   string
		write  func(c *HTTPContext)
		status int
		body   string
	}{
		{"json", func(c *HTTPContext) { c.JSON(http.StatusCreated, H{"a": 1}) }, http.StatusCreated, `{"a":1}`},
		{"indented", func(c *HTTPContext) { c.IndentedJSON(http.StatusOK, H{"a": 1}) }, http.StatusOK, "{\n    \"a\": 1\n}"},
		{"json error", func(c *HTTPContext) { c.JSON(http.StatusOK, make(chan int)) }, http.StatusInternalServerError, ""},
		{"data", func(c *HTTPContext) { c.Data(http.StatusAccepted, "text/plain", []byte("abc")) }, http.StatusAccepted, "abc"},
		{"status", func(c *HTTPContext) { c.Status(http.StatusNoContent) }, http.StatusNoContent, ""},
		{"abort", func(c *HTTPContext) { c.AbortWithStatus(http.StatusForbidden) }, http.StatusForbidden, ""},
		{"abort json", func(c *HTTPContext) { c.AbortWithStatusJSON(http.StatusConflict, H{"e": "x"}) }, http.StatusConflict, `{"e":"x"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c := NewHTTPContext(w, httptest.NewRequest(http.MethodGet, "/", nil))
			tt.write(c)
			assert.Equal(t, tt.status, w.Code)
			assert.Equal(t, tt.body, w.Body.String())
			assert.Equal(t, tt.status, c.ResponseStatus())
			assert.Equal(t, len(tt.body), c.ResponseSize())
		})
	}
}

func TestHTTPContextWriteError(t *testing.T) {
	w := failWriter{httptest.NewRecorder()}
	c := NewHTTPContext(w, httptest.NewRequest(http.MethodGet, "/", nil))
	c.Data(http.StatusOK, "text/plain", []byte("lost"))
	assert.Equal(t, http.StatusOK, c.ResponseStatus())
	assert.Zero(t, c.ResponseSize())
	assert.Equal(t, "4", w.Header().Get("Content-Length"))
}

func TestHTTPContextResponseController(t *testing.T) {
	w := httptest.NewRecorder()
	c := NewHTTPContext(w, httptest.NewRequest(http.MethodGet, "/", nil))
	// Flush reaches the recorder through Unwrap
	require.NoError(t, http.NewResponseController(c.writer).Flush())
	assert.True(t, w.Flushed)
}

func TestBufferedWriterFlush(t *testing.T) {
	t.Run("nothing written", func(t *testing.T) {
		w := httptest.NewRecorder()
		held := &bufferedWriter{ResponseWriter: w}
		require.NoError(t, held.flush(w))
		assert.False(t, w.Flushed)
		assert.Empty(t, w.Body.String())
	})
	t.Run("body without status", func(t *testing.T) {
		w := httptest.NewRecorder()
		held := &bufferedWriter{ResponseWriter: w}
		_, err := held.Write([]byte("ok"))
		require.NoError(t, err)
		require.NoError(t, held.flush(w))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "ok", w.Body.String())
	})
	t.Run("write error", func(t *testing.T) {
		w := failWriter{httptest.NewRecorder()}
		held := &bufferedWriter{ResponseWriter: w}
		held.WriteHeader(http.StatusCreated)
		_, err := held.Write([]byte("ok"))
		require.NoError(t, err)
		assert.ErrorIs(t, held.flush(w), errWrite)
		assert.Equal(t, http.StatusCreated, w.Code)
	})
}

func TestHTTPContextChain(t *testing.T) {
	var order []string
	handlers := []HandlerFunc{
		func(c Context) {
			flush := c.NextBuffered()
			order = append(order, "buffered")
			require.NoError(t, flush())
		},
		func(c Context) {
			c.ServeHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "wrapped "+r.URL.Path)
				w.WriteHeader(http.StatusTeapot)
				_, err := w.Write([]byte("tea"))
				assert.NoError(t, err)
			}))
			c.Next()
		},
		func(c Context) { order = append(order, "last") },
	}
	w := httptest.NewRecorder()
	c := newHTTPContext(w, httptest.NewRequest(http.MethodGet, "/chain", nil), handlers, nil, "/chain")
	c.Next()
	assert.Equal(t, []string{"wrapped /chain", "last", "buffered"}, order)
	assert.Equal(t, http.StatusTeapot, w.Code)
	assert.Equal(t, "tea", w.Body.String())
	assert.Equal(t, "/chain", c.FullPath())
}

func TestHTTPContextRedirect(t *testing.T) {
	w := httptest.NewRecorder()
	c := NewHTTPContext(w, httptest.NewRequest(http.MethodGet, "/old", nil))
	c.Redirect(http.StatusFound, "/new")
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/new", w.Header().Get("Location"))
	assert.Equal(t, http.StatusFound, c.ResponseStatus())
}
