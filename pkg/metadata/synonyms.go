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

package metadata

import (
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

type synonyms struct{}

var Synonyms = new(synonyms)

func (t *synonyms) List(offset, limit int) ([]*meta.SynonymSet, error) {
	data, err := db.List(t.key(""), offset, limit)
	if err != nil {
		return nil, err
	}
	sets := make([]*meta.SynonymSet, 0, len(data))
	for _, d := range data {
		set := new(meta.SynonymSet)
		err = json.Unmarshal(d, set)
		if err != nil {
			return nil, err
		}
		sets = append(sets, set)
	}
	return sets, nil
}

func (t *synonyms) Get(id string) (*meta.SynonymSet, error) {
	data, err := db.Get(t.key(id))
	if err != nil {
		return nil, err
	}
	set := new(meta.SynonymSet)
	err = json.Unmarshal(data, set)
	return set, err
}

func (t *synonyms) Set(id string, val meta.SynonymSet) error {
	data, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return db.Set(t.key(id), data)
}

func (t *synonyms) Delete(id string) error {
	return db.Delete(t.key(id))
}

func (t *synonyms) key(id string) string {
	return "/synonyms/" + id
}
