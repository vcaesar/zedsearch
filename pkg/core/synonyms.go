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
	"fmt"
	"slices"
	"sort"
	"sync"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

// synonymRefs orders the publications of indexes referencing synonyms sets
// against DeleteSynonymSet: storeIndex holds it shared, the delete exclusive.
var synonymRefs sync.RWMutex

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

// DeleteSynonymSet deletes the synonyms set id unless an index references
// it, returning the sorted names of those indexes instead.
func DeleteSynonymSet(id string) ([]string, error) {
	// snapshot before locking: GetOrCreate stores under the list lock
	live := ZED_INDEX_LIST.List()
	synonymRefs.Lock()
	defer synonymRefs.Unlock()

	stored, err := metadata.Index.List(0, 0)
	if err != nil {
		return nil, err
	}
	var used []string
	for _, index := range live {
		if settings := index.GetSettings(); settings != nil && slices.Contains(synonymSetIDs(settings.Analysis), id) {
			used = append(used, index.GetName())
		}
	}
	for _, index := range stored {
		if index.Settings != nil && slices.Contains(synonymSetIDs(index.Settings.Analysis), id) {
			used = append(used, index.Name)
		}
	}
	if len(used) > 0 {
		sort.Strings(used)
		return slices.Compact(used), nil
	}
	return nil, metadata.Synonyms.Delete(id)
}

// checkSynonymSets reports the first synonyms set index references that is
// not stored; the caller holds synonymRefs.
func checkSynonymSets(index *Index) error {
	settings := index.GetSettings()
	if settings == nil {
		return nil
	}
	for _, id := range synonymSetIDs(settings.Analysis) {
		if _, err := metadata.Synonyms.Get(id); err != nil {
			if errors.Is(err, errors.ErrKeyNotFound) {
				return fmt.Errorf("synonyms set [%s] not found", id)
			}
			return storageError(err)
		}
	}
	return nil
}

// synonymSetIDs returns the synonyms set ids the token filters of analysis use.
func synonymSetIDs(analysis *meta.IndexAnalysis) []string {
	if analysis == nil {
		return nil
	}
	var ids []string
	for _, filters := range []map[string]interface{}{analysis.TokenFilter, analysis.Filter} {
		for _, options := range filters {
			if id, err := zutils.GetStringFromMap(options, "synonyms_set"); err == nil {
				ids = append(ids, id)
			}
		}
	}
	return ids
}
