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

package query

import (
	"fmt"
	"strings"

	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis"
	"github.com/vcaesar/riot/search"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

// MaxPinnedDocs is the Elasticsearch limit of pinned documents.
const MaxPinnedDocs = 100

// pinnedBaseScore puts pinned documents above any organic score, in the
// order they are listed.
const pinnedBaseScore = 1e30

// PinnedQuery promotes the listed documents above the organic query, in the
// listed order. docs entries are {"_index", "_id"}; _index is accepted but
// not checked, as an index only holds its own documents.
func PinnedQuery(query map[string]interface{}, mappings *meta.Mappings, analyzers map[string]*analysis.Analyzer) (riot.Query, error) {
	var ids []string
	var organic riot.Query
	hasIDs, hasDocs := false, false
	for k, v := range query {
		key := strings.ToLower(k)
		switch key {
		case "ids":
			hasIDs = true
			list, ok := v.([]interface{})
			if !ok {
				return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[pinned] ids doesn't support values of type: %T", v))
			}
			for _, item := range list {
				id, ok := idString(item)
				if !ok {
					return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[pinned] ids doesn't support values of type: %T", item))
				}
				ids = append(ids, id)
			}
		case "docs":
			hasDocs = true
			list, ok := v.([]interface{})
			if !ok {
				return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[pinned] docs doesn't support values of type: %T", v))
			}
			for _, item := range list {
				doc, ok := item.(map[string]interface{})
				if !ok {
					return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[pinned] docs doesn't support values of type: %T", item))
				}
				id, ok := idString(doc["_id"])
				if !ok || id == "" {
					return nil, errors.New(errors.ErrorTypeParsingException, "[pinned] docs entry requires [_id]")
				}
				ids = append(ids, id)
			}
		case "organic":
			vv, ok := v.(map[string]interface{})
			if !ok {
				return nil, errors.New(errors.ErrorTypeXContentParseException, fmt.Sprintf("[pinned] organic doesn't support values of type: %T", v))
			}
			subq, err := Query(vv, mappings, analyzers)
			if err != nil {
				return nil, errors.New(errors.ErrorTypeParsingException, "[pinned] failed to parse [organic]").Cause(err)
			}
			organic = subq
		case "boost", "_name":
			// accepted
		default:
			return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[pinned] unknown field [%s]", k))
		}
	}
	if hasIDs == hasDocs {
		return nil, errors.New(errors.ErrorTypeParsingException, "[pinned] requires one of [ids] or [docs]")
	}
	if organic == nil {
		return nil, errors.New(errors.ErrorTypeParsingException, "[pinned] requires [organic] query")
	}
	if len(ids) > MaxPinnedDocs {
		return nil, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[pinned] cannot pin more than %d documents", MaxPinnedDocs))
	}
	return NewPinnedQuery(ids, organic), nil
}

// NewPinnedQuery returns organic with the documents of ids promoted on top.
func NewPinnedQuery(ids []string, organic riot.Query) riot.Query {
	ids = uniqueStrings(ids)
	if len(ids) == 0 {
		return organic
	}
	q := &pinnedQuery{organic: organic}
	pinned := riot.NewBooleanQuery()
	for _, id := range ids {
		term := riot.NewTermQuery(id).SetField("_id")
		q.markers = append(q.markers, term)
		pinned.AddShould(term)
	}
	q.all = riot.NewBooleanQuery().AddShould(pinned, organic).SetMinShould(1)
	return q
}

type pinnedQuery struct {
	organic riot.Query
	all     *riot.BooleanQuery
	markers []riot.Query
}

func (q *pinnedQuery) Searcher(i search.Reader, options search.SearcherOptions) (search.Searcher, error) {
	return newRescoreSearcher(i, options, q.all, q.markers, func(dm *search.DocumentMatch, matched []bool) {
		for n, ok := range matched {
			if ok {
				dm.Score = pinnedBaseScore - float64(n)*pinnedBaseScore/(2*MaxPinnedDocs)
				return
			}
		}
	})
}

func (q *pinnedQuery) Validate() error {
	return validateAll(q.organic)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok || v == "" {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// idString returns a document id given as a string or a number.
func idString(v interface{}) (string, bool) {
	switch v.(type) {
	case string, float64, int, int64:
		id, err := zutils.ToString(v)
		return id, err == nil
	}
	return "", false
}
