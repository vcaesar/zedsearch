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

package core

import (
	"github.com/vcaesar/zedsearch/pkg/metadata"
)

// LookupSynonymSet returns the rules of the stored synonyms set id.
func LookupSynonymSet(id string) ([]string, error) {
	set, err := metadata.Synonyms.Get(id)
	if err != nil {
		return nil, err
	}
	rules := make([]string, len(set.Rules))
	for i, rule := range set.Rules {
		rules[i] = rule.Synonyms
	}
	return rules, nil
}
