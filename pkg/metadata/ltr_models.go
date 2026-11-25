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
please refer to [AGPL-3.0](./LICENSE-AGPL)) and [LICENSE-EE](./LICENSE-EE) for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.

The other parts of this work are licensed under the [Apache license](./LICENSE).
*/

package metadata

import (
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

type ltrModels struct{}

// LTRModels stores the learning to rank models.
var LTRModels = new(ltrModels)

func (t *ltrModels) List(offset, limit int) ([]*meta.LTRModel, error) {
	data, err := db.List(t.key(""), offset, limit)
	if err != nil {
		return nil, err
	}
	models := make([]*meta.LTRModel, 0, len(data))
	for _, d := range data {
		m := new(meta.LTRModel)
		if err = json.Unmarshal(d, m); err != nil {
			return nil, err
		}
		models = append(models, m)
	}
	return models, nil
}

func (t *ltrModels) Get(id string) (*meta.LTRModel, error) {
	data, err := db.Get(t.key(id))
	if err != nil {
		return nil, err
	}
	m := new(meta.LTRModel)
	err = json.Unmarshal(data, m)
	return m, err
}

func (t *ltrModels) Set(id string, val meta.LTRModel) error {
	data, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return db.Set(t.key(id), data)
}

func (t *ltrModels) Delete(id string) error {
	return db.Delete(t.key(id))
}

func (t *ltrModels) key(id string) string {
	return "/ltr_models/" + id
}
