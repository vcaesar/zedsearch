/* Copyright 2026 AtomAI.
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
or production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.
*/

package aggregation

import (
	"bytes"
	"sort"

	"github.com/vcaesar/riot/search"
)

// TopHit is one document kept by a [top_hits] aggregation. Source holds the
// raw _source bytes; they are copied because the stored field visitor reuses
// its buffer once Consume returns.
type TopHit struct {
	ID     string
	Index  string
	Score  float64
	Source []byte
	// Stored holds copies of the stored fields named by SetStoredFields.
	Stored map[string][]byte

	SortKey []byte // doc value of the sort field, nil when sorting by _score
}

// TopHitsAggregation implements the elasticsearch [top_hits] aggregation:
// the best documents of the enclosing bucket.
//
// Only a single sort field is supported; sortField "" sorts by descending
// _score, as elasticsearch does by default.
type TopHitsAggregation struct {
	size      int
	from      int
	sortField string
	sortDesc  bool

	sourceEnable bool
	sourceFields []string
	storedFields map[string]struct{}

	includeSortKey bool
}

// NewTopHitsAggregation returns the aggregation backing [top_hits].
func NewTopHitsAggregation(size, from int, sortField string, sortDesc bool) *TopHitsAggregation {
	return &TopHitsAggregation{size: size, from: from, sortField: sortField, sortDesc: sortDesc, sourceEnable: true}
}

// SetSource configures the _source filtering of the returned hits. The
// aggregation layer only carries the spec, the response layer applies it.
func (a *TopHitsAggregation) SetSource(enable bool, fields []string) {
	a.sourceEnable, a.sourceFields = enable, fields
}

// SetStoredFields names the stored fields kept with each hit besides _source,
// e.g. vectors the response layer restores into _source.
func (a *TopHitsAggregation) SetStoredFields(fields []string) {
	a.storedFields = make(map[string]struct{}, len(fields))
	for _, f := range fields {
		a.storedFields[f] = struct{}{}
	}
}

// SetIncludeSortKey asks the response layer to render the sort key of each
// hit, for the node that merges the hits of several nodes.
func (a *TopHitsAggregation) SetIncludeSortKey(include bool) {
	a.includeSortKey = include
}

func (a *TopHitsAggregation) Fields() []string {
	if a.sortField == "" {
		return nil
	}
	return []string{a.sortField}
}

func (a *TopHitsAggregation) Calculator() search.Calculator {
	return &TopHitsCalculator{
		size:         a.size,
		from:         a.from,
		sortField:    a.sortField,
		sortDesc:     a.sortDesc,
		sourceEnable: a.sourceEnable,
		sourceFields: a.sourceFields,
		storedFields: a.storedFields,

		includeSortKey: a.includeSortKey,
	}
}

// TopHitsCalculator keeps the best from+size documents of its bucket. It
// implements neither MetricCalculator nor BucketCalculator: the response layer
// type-switches on the concrete type.
type TopHitsCalculator struct {
	size      int
	from      int
	sortField string
	sortDesc  bool

	sourceEnable bool
	sourceFields []string
	storedFields map[string]struct{}

	includeSortKey bool

	hits  []TopHit
	total uint64
	err   error
}

// Source returns the _source filtering requested for the hits.
func (c *TopHitsCalculator) Source() (enable bool, fields []string) {
	return c.sourceEnable, c.sourceFields
}

// IncludeSortKey reports whether the hits are rendered with their sort key.
func (c *TopHitsCalculator) IncludeSortKey() bool { return c.includeSortKey }

func (c *TopHitsCalculator) Consume(d *search.DocumentMatch) {
	c.total++
	hit := TopHit{Score: d.Score}
	if c.sortField != "" {
		hit.SortKey = append([]byte(nil), search.Field(c.sortField).Value(d)...)
	}
	err := d.VisitStoredFields(func(field string, value []byte) bool {
		switch field {
		case "_id":
			hit.ID = string(value)
		case "_index":
			hit.Index = string(value)
		case "_source":
			hit.Source = append([]byte(nil), value...)
		default:
			if _, ok := c.storedFields[field]; ok {
				if hit.Stored == nil {
					hit.Stored = make(map[string][]byte)
				}
				hit.Stored[field] = append([]byte(nil), value...)
			}
		}
		return true
	})
	if err != nil {
		if c.err == nil {
			c.err = err
		}
		return
	}
	c.hits = append(c.hits, hit)
}

func (c *TopHitsCalculator) Merge(other search.Calculator) {
	o, ok := other.(*TopHitsCalculator)
	if !ok {
		return
	}
	c.total += o.total
	c.hits = append(c.hits, o.hits...)
	if c.err == nil {
		c.err = o.err
	}
	c.Finish()
}

// Finish keeps the from+size best hits, so that a later Merge still has the
// documents skipped by from. Hits applies the window.
func (c *TopHitsCalculator) Finish() {
	sort.SliceStable(c.hits, c.less)
	if keep := c.from + c.size; keep > 0 && keep < len(c.hits) {
		c.hits = c.hits[:keep]
	}
}

func (c *TopHitsCalculator) less(i, j int) bool {
	if c.sortField == "" {
		return c.hits[i].Score > c.hits[j].Score // best score first
	}
	// Documents without the sort field go last in both directions, as in elasticsearch.
	if mi, mj := len(c.hits[i].SortKey) == 0, len(c.hits[j].SortKey) == 0; mi != mj {
		return mj
	}
	// Doc value terms of both text and numeric fields sort in byte order.
	cmp := bytes.Compare(c.hits[i].SortKey, c.hits[j].SortKey)
	if c.sortDesc {
		return cmp > 0
	}
	return cmp < 0
}

// // StoredFields returns the names set by SetStoredFields.
// func (c *TopHitsCalculator) StoredFields() []string {
//     fields := make([]string, 0, len(c.storedFields))
//     for f := range c.storedFields {
//         fields = append(fields, f)
//     }
//     return fields
// }

// Err reports the first stored field error seen while collecting.
func (c *TopHitsCalculator) Err() error { return c.err }

// Total is the number of documents in the bucket, not the number of hits kept.
func (c *TopHitsCalculator) Total() uint64 { return c.total }

// MaxScore is the best score of the kept hits, 0 when there is none.
func (c *TopHitsCalculator) MaxScore() float64 {
	var max float64
	for i := range c.hits {
		if c.hits[i].Score > max {
			max = c.hits[i].Score
		}
	}
	return max
}

// Hits returns the requested window of the ranked hits.
func (c *TopHitsCalculator) Hits() []TopHit {
	if c.from >= len(c.hits) {
		return nil
	}
	return c.hits[c.from:min(c.from+c.size, len(c.hits))]
}
