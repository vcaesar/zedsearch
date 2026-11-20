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

package metadata

import (
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

type queryRules struct{}

var QueryRules = new(queryRules)

func (t *queryRules) List(offset, limit int) ([]*meta.QueryRuleset, error) {
	data, err := db.List(t.key(""), offset, limit)
	if err != nil {
		return nil, err
	}
	rulesets := make([]*meta.QueryRuleset, 0, len(data))
	for _, d := range data {
		rs := new(meta.QueryRuleset)
		err = json.Unmarshal(d, rs)
		if err != nil {
			return nil, err
		}
		rulesets = append(rulesets, rs)
	}
	return rulesets, nil
}

func (t *queryRules) Get(id string) (*meta.QueryRuleset, error) {
	data, err := db.Get(t.key(id))
	if err != nil {
		return nil, err
	}
	rs := new(meta.QueryRuleset)
	err = json.Unmarshal(data, rs)
	return rs, err
}

func (t *queryRules) Set(id string, val meta.QueryRuleset) error {
	data, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return db.Set(t.key(id), data)
}

func (t *queryRules) Delete(id string) error {
	return db.Delete(t.key(id))
}

func (t *queryRules) key(id string) string {
	return "/query_rules/" + id
}
