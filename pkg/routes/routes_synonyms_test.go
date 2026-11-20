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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vcaesar/zedsearch/pkg/zhttp"
)

func TestSetSynonymsRoutes(t *testing.T) {
	r := zhttp.New()
	assert.NotPanics(t, func() {
		SetRoutes(r)
		SetSynonymsRoutes(r)
	})

	routes := []struct{ method, path string }{
		{http.MethodGet, "/_synonyms"},
		{http.MethodGet, "/_synonyms/set"},
		{http.MethodPut, "/_synonyms/set"},
		{http.MethodDelete, "/_synonyms/set"},
		{http.MethodGet, "/_synonyms/set/rule"},
		{http.MethodPut, "/_synonyms/set/rule"},
		{http.MethodDelete, "/_synonyms/set/rule"},
		{http.MethodPost, "/books/_reload_search_analyzers"},
	}
	for _, prefix := range []string{"/api", "/es"} {
		for _, route := range routes {
			t.Run(route.method+" "+prefix+route.path, func(t *testing.T) {
				w := serve(r, httptest.NewRequest(route.method, prefix+route.path, nil))
				assert.Equal(t, http.StatusUnauthorized, w.Code)
			})
		}
	}
}
