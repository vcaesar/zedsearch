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
	"github.com/vcaesar/zedsearch/pkg/handlers/ltr"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
)

// SetLTRRoutes sets up the learning to rank model endpoints, native and
// Elasticsearch compatible.
func SetLTRRoutes(r *zhttp.Engine) {
	for _, prefix := range []string{"/api", "/es"} {
		mw := []zhttp.HandlerFunc{}
		if prefix == "/es" {
			mw = append(mw, ESMiddleware)
		}
		route := func(permission string, h zhttp.HandlerFunc) []zhttp.HandlerFunc {
			return append(append([]zhttp.HandlerFunc{AuthMiddleware(permission)}, mw...), h)
		}
		r.GET(prefix+"/_ml/trained_models", route("ltr.GetModels", ltr.GetModels)...)
		r.GET(prefix+"/_ml/trained_models/:model_id", route("ltr.GetModels", ltr.GetModels)...)
		r.PUT(prefix+"/_ml/trained_models/:model_id", route("ltr.PutModel", ltr.PutModel)...)
		r.DELETE(prefix+"/_ml/trained_models/:model_id", route("ltr.DeleteModel", ltr.DeleteModel)...)
	}
}
