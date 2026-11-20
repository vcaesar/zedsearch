/* Copyright 2022 Zinc Labs Inc. and Contributors
* Copyright 2026 AtomAI.
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
please refer to [AGPL-3.0](./LICENSE-EE)) and LICENSE-EE for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.
*/

package core

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis"

	zedsearch "github.com/vcaesar/zedsearch/pkg/bluge/search"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/uquery"
	"github.com/vcaesar/zedsearch/pkg/uquery/assoc"
	"github.com/vcaesar/zedsearch/pkg/uquery/timerange"
)

func MultiSearch(indexNames []string, query *meta.ZedQuery) (*meta.SearchResponse, error) {
	var readers []*riot.Reader
	var shardNum int64
	indexes := make(map[string]*Index)

	timeMin, timeMax := timerange.Query(query.Query)
	isMatched := false
	hasIndex := false
	for _, index := range ZED_INDEX_LIST.List() {
		if len(indexNames) > 0 {
			for _, indexName := range indexNames {
				isMatched = isMatchIndex(index.GetName(), indexName)
				if isMatched {
					hasIndex = true
					break
				}
			}
			if !isMatched {
				continue
			}
		}

		reader, err := index.GetReaders(timeMin, timeMax)
		if err != nil {
			return nil, err
		}
		readers = append(readers, reader...)
		shardNum += index.GetShardNum()
		indexes[index.GetName()] = index
	}

	if len(readers) == 0 {
		if !hasIndex {
			return nil, fmt.Errorf("core.MultiSearchV2: error accessing reader: no index found")
		}
		// matched indexes without readers still validate and answer suggest
		resp := &meta.SearchResponse{}
		if len(query.Suggest) > 0 {
			mappings, analyzers := unionMappings(indexes)
			suggest, err := assoc.Suggest(nil, query.Suggest, mappings, analyzers)
			if err != nil {
				return nil, err
			}
			resp.Suggest = suggest
		}
		return resp, nil
	}

	defer func() {
		for _, reader := range readers {
			reader.Close()
		}
	}()

	mappings, analyzers := unionMappings(indexes)

	// the deadline also covers preparing the query and the extension work
	ctx := context.Background()
	var cancel context.CancelFunc
	if query.Timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(query.Timeout)*time.Second)
		defer cancel()
	}

	// nested and join queries are resolved before query is parsed, and hidden
	// nested documents are excluded from results.
	// rule, more_like_this and rescore need the readers before query is parsed
	ext, err := prepareSearch(query, mappings, analyzers, readers)
	if err != nil {
		return nil, err
	}
	defer ext.restore()
	if err := rewriteJoinQuery(query, mappings, analyzers, readers); err != nil {
		return nil, err
	}

	_, err = uquery.ParseQueryDSL(query, mappings, analyzers)
	if err != nil {
		return nil, err
	}

	// dmi, err := riot.MultiSearch(ctx, searchRequest, readers...)
	dmi, err := zedsearch.MultiSearch(ctx, query, mappings, analyzers, readers...)
	if err != nil {
		log.Printf("core.MultiSearchV2: error executing search: %s", err.Error())
		if err == context.DeadlineExceeded {
			return &meta.SearchResponse{
				TimedOut: true,
				Error:    err.Error(),
				Hits:     meta.Hits{Hits: []meta.Hit{}},
			}, nil
		}
		return nil, err
	}

	resp, err := searchV2(shardNum, int64(len(readers)), dmi, query, mappings, analyzers, indexes)
	if err != nil {
		return nil, err
	}
	if err := ext.finish(ctx, resp); err != nil {
		if err == context.DeadlineExceeded {
			return &meta.SearchResponse{
				TimedOut: true,
				Error:    err.Error(),
				Hits:     meta.Hits{Hits: []meta.Hit{}},
			}, nil
		}
		return nil, err
	}
	return resp, nil
}

// unionMappings returns the mappings and analyzers the query of a search over
// indexes is parsed with: the fields and analyzers of every index, so default
// fields, aggregations and top_hits cover them all. An index earlier by name
// wins a name the indexes define differently, and _all is searched only when
// every index has it. Hits are still rendered with their own index.
func unionMappings(indexes map[string]*Index) (*meta.Mappings, map[string]*analysis.Analyzer) {
	names := make([]string, 0, len(indexes))
	for name := range indexes {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 1 {
		return indexes[names[0]].GetMappings(), indexes[names[0]].GetAnalyzers()
	}

	mappings := meta.NewMappings()
	analyzers := make(map[string]*analysis.Analyzer)
	all := true
	var shadowed []string // vector fields a name defined earlier hides
	for _, name := range names {
		m := indexes[name].GetMappings()
		all = all && m.AllEnabled()
		m.RangeProperty(func(field string, prop meta.Property) bool {
			first, ok := mappings.Properties[field]
			if !ok {
				mappings.Properties[field] = prop
			} else if prop.Type == "vector" && prop.Index && (first.Type != "vector" || !first.Index) && !slices.Contains(shadowed, field) {
				shadowed = append(shadowed, field)
			}
			return true
		})
		for name, zer := range indexes[name].GetAnalyzers() {
			if _, ok := analyzers[name]; !ok {
				analyzers[name] = zer
			}
		}
	}
	mappings.SetAllEnabled(all)
	mappings.AddVectorFields(shadowed...)
	return mappings, analyzers
}

// isMatchIndex("abc", "a")  false
// isMatchIndex("abc", "a*") true
// isMatchIndex("abc", "*bc") true
// isMatchIndex("abc", "bc") false
// isMatchIndex("abc", "abc") true
func isMatchIndex(zedIndexName, indexName string) bool {
	if indexName == "" {
		return true
	}

	// eg.: *-test
	if strings.HasPrefix(indexName, "*") {
		return strings.HasSuffix(zedIndexName, indexName[1:])
	}

	// eg.: test-*
	if strings.HasSuffix(indexName, "*") {
		return strings.HasPrefix(zedIndexName, indexName[:len(indexName)-1])
	}

	return zedIndexName == indexName
}
