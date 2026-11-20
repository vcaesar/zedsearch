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
*
Part of this work fall under the AtomAI Enterprise Edition (EE)
and are licensed under the Business Source License 1.1,
if you are the Cloud companies or your companies with revenue greater than $1M,
please refer to [AGPL-3.0](./LICENSE-AGPL)) and [LICENSE-EE](LICENSE-EE) for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.

The other parts of this work are licensed under the [Apache license](./LICENSE).
*/

package routes

import (
	"github.com/vcaesar/zedsearch/pkg/handlers/synonyms"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
)

// SetSynonymsRoutes sets up the synonyms sets endpoints, native and
// Elasticsearch compatible.
func SetSynonymsRoutes(r *zhttp.Engine) {
	r.GET("/api/_synonyms", AuthMiddleware("synonyms.List"), synonyms.ListSets)
	r.GET("/api/_synonyms/:set_id", AuthMiddleware("synonyms.Get"), synonyms.GetSet)
	r.PUT("/api/_synonyms/:set_id", AuthMiddleware("synonyms.Put"), synonyms.PutSet)
	r.DELETE("/api/_synonyms/:set_id", AuthMiddleware("synonyms.Delete"), synonyms.DeleteSet)
	r.GET("/api/_synonyms/:set_id/:rule_id", AuthMiddleware("synonyms.GetRule"), synonyms.GetRule)
	r.PUT("/api/_synonyms/:set_id/:rule_id", AuthMiddleware("synonyms.PutRule"), synonyms.PutRule)
	r.DELETE("/api/_synonyms/:set_id/:rule_id", AuthMiddleware("synonyms.DeleteRule"), synonyms.DeleteRule)
	r.POST("/api/:target/_reload_search_analyzers", AuthMiddleware("synonyms.ReloadSearchAnalyzers"), synonyms.ReloadSearchAnalyzers)

	r.GET("/es/_synonyms", AuthMiddleware("synonyms.List"), ESMiddleware, synonyms.ListSets)
	r.GET("/es/_synonyms/:set_id", AuthMiddleware("synonyms.Get"), ESMiddleware, synonyms.GetSet)
	r.PUT("/es/_synonyms/:set_id", AuthMiddleware("synonyms.Put"), ESMiddleware, synonyms.PutSet)
	r.DELETE("/es/_synonyms/:set_id", AuthMiddleware("synonyms.Delete"), ESMiddleware, synonyms.DeleteSet)
	r.GET("/es/_synonyms/:set_id/:rule_id", AuthMiddleware("synonyms.GetRule"), ESMiddleware, synonyms.GetRule)
	r.PUT("/es/_synonyms/:set_id/:rule_id", AuthMiddleware("synonyms.PutRule"), ESMiddleware, synonyms.PutRule)
	r.DELETE("/es/_synonyms/:set_id/:rule_id", AuthMiddleware("synonyms.DeleteRule"), ESMiddleware, synonyms.DeleteRule)
	r.POST("/es/:target/_reload_search_analyzers", AuthMiddleware("synonyms.ReloadSearchAnalyzers"), ESMiddleware, synonyms.ReloadSearchAnalyzers)
}
