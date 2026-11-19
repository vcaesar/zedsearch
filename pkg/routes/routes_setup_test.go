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

package routes

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
)

func stubFrontend(t *testing.T, front fs.FS, err error) {
	t.Helper()
	previous := frontendAssets
	frontendAssets = func() (fs.FS, error) { return front, err }
	t.Cleanup(func() { frontendAssets = previous })
}

func enableSwagger(t *testing.T) {
	t.Helper()
	previous := config.Global.ZedSwaggerEnable
	config.Global.ZedSwaggerEnable = true
	t.Cleanup(func() { config.Global.ZedSwaggerEnable = previous })
}

func TestSetRoutesWithFrontend(t *testing.T) {
	enableSwagger(t)
	stubFrontend(t, fstest.MapFS{
		"index.html":            {Data: []byte(`<div id="root"></div>`)},
		"swagger-ui/index.html": {Data: []byte(`<div id="swagger-ui"></div>`)},
		"assets/app.js":         {Data: []byte(`console.log(1)`)},
		"assets/app.css":        {Data: []byte(`body{}`)},
	}, nil)
	r := zhttp.New()
	SetRoutes(r)

	tests := []struct {
		method, path string
		code         int
		body         string
		header       map[string]string // values the header must contain
	}{
		{method: http.MethodGet, path: "/", code: http.StatusFound},
		{method: http.MethodGet, path: "/ui", code: http.StatusMovedPermanently},
		{method: http.MethodGet, path: "/ui/", code: http.StatusOK, body: `id="root"`},
		{method: http.MethodGet, path: "/ui/search", code: http.StatusOK, body: `id="root"`},
		{method: http.MethodGet, path: "/ui/assets/app.js", code: http.StatusOK, body: "console.log", header: map[string]string{
			"Cache-Control": "public, max-age=2592000",
			"Content-Type":  "javascript",
		}},
		{method: http.MethodHead, path: "/ui/assets/app.css", code: http.StatusOK, header: map[string]string{
			"Content-Type": "text/css; charset=utf-8",
		}},
		{method: http.MethodGet, path: "/ui/assets/missing.js", code: http.StatusNotFound, body: "404 page not found"},
		{method: http.MethodGet, path: "/swagger", code: http.StatusMovedPermanently},
		{method: http.MethodGet, path: "/swagger/", code: http.StatusOK, body: `id="swagger-ui"`},
		{method: http.MethodGet, path: "/swagger/index.html", code: http.StatusOK, body: `id="swagger-ui"`},
		{method: http.MethodGet, path: "/swagger/doc.json", code: http.StatusOK, body: `"swagger"`},
		{method: http.MethodGet, path: "/swagger/missing", code: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			w := serve(r, httptest.NewRequest(tt.method, tt.path, nil))
			assert.Equal(t, tt.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.body)
			for key, value := range tt.header {
				assert.Contains(t, w.Header().Get(key), value, key)
			}
		})
	}
}

func TestSetRoutesWithBrokenFrontend(t *testing.T) {
	enableSwagger(t)
	// neither the console index nor the swagger page was built
	stubFrontend(t, fstest.MapFS{"assets/app.js": {Data: []byte(`1`)}}, nil)
	r := zhttp.New()
	SetRoutes(r)

	assert.Equal(t, http.StatusInternalServerError, serve(r, httptest.NewRequest(http.MethodGet, "/ui/search", nil)).Code)
	assert.Equal(t, http.StatusNotFound, serve(r, httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil)).Code)
}

func TestSetRoutesFrontendError(t *testing.T) {
	stubFrontend(t, nil, errors.New("no assets"))
	r := zhttp.New()
	SetRoutes(r)

	w := serve(r, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"version"`)

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/es/"},
		{http.MethodHead, "/es/"},
		{http.MethodGet, "/es/_license"},
		{http.MethodGet, "/es/_xpack"},
	} {
		w := serve(r, httptest.NewRequest(route.method, route.path, nil))
		assert.Equal(t, http.StatusOK, w.Code, route.path)
		assert.Equal(t, "Elasticsearch", w.Header().Get("X-elastic-product"), route.path)
	}
}

func TestSetup(t *testing.T) {
	previous := config.Global.GinMode
	t.Cleanup(func() { config.Global.GinMode = previous })

	tests := []struct {
		mode  string
		pprof int
	}{
		{mode: "debug", pprof: http.StatusOK},
		{mode: "release", pprof: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			config.Global.GinMode = tt.mode
			r := zhttp.New()
			Setup(r)

			assert.Equal(t, tt.pprof, serve(r, httptest.NewRequest(http.MethodGet, "/debug/pprof/cmdline", nil)).Code)
			assert.Equal(t, http.StatusOK, serve(r, httptest.NewRequest(http.MethodGet, "/healthz", nil)).Code)
			for _, route := range []struct{ method, path string }{
				{http.MethodGet, "/api/cluster/nodes"},
				{http.MethodGet, "/api/index/logs/_tier"},
				{http.MethodGet, "/es/_ilm/policy"},
				{http.MethodPost, "/api/_sql"},
			} {
				w := serve(r, httptest.NewRequest(route.method, route.path, nil))
				assert.Equal(t, http.StatusUnauthorized, w.Code, route.path)
			}
		})
	}
}
