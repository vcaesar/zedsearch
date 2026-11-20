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
	"github.com/vcaesar/zedsearch/pkg/handlers/rules"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
)

// SetQueryRulesRoutes sets up the query rules endpoints, native and
// Elasticsearch compatible.
func SetQueryRulesRoutes(r *zhttp.Engine) {
	for _, prefix := range []string{"/api", "/es"} {
		mw := []zhttp.HandlerFunc{}
		if prefix == "/es" {
			mw = append(mw, ESMiddleware)
		}
		route := func(permission string, h zhttp.HandlerFunc) []zhttp.HandlerFunc {
			return append(append([]zhttp.HandlerFunc{AuthMiddleware(permission)}, mw...), h)
		}
		r.GET(prefix+"/_query_rules", route("rules.ListRulesets", rules.ListRulesets)...)
		r.GET(prefix+"/_query_rules/:ruleset_id", route("rules.GetRuleset", rules.GetRuleset)...)
		r.PUT(prefix+"/_query_rules/:ruleset_id", route("rules.PutRuleset", rules.PutRuleset)...)
		r.DELETE(prefix+"/_query_rules/:ruleset_id", route("rules.DeleteRuleset", rules.DeleteRuleset)...)
		r.POST(prefix+"/_query_rules/:ruleset_id/_test", route("rules.TestRuleset", rules.TestRuleset)...)
		r.GET(prefix+"/_query_rules/:ruleset_id/_rule/:rule_id", route("rules.GetRule", rules.GetRule)...)
		r.PUT(prefix+"/_query_rules/:ruleset_id/_rule/:rule_id", route("rules.PutRule", rules.PutRule)...)
		r.DELETE(prefix+"/_query_rules/:ruleset_id/_rule/:rule_id", route("rules.DeleteRule", rules.DeleteRule)...)
	}
}
