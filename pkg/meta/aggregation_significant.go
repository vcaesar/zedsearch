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

package meta

const (
	DefaultSignificantTermsSize        = 10
	DefaultSignificantTermsMinDocCount = 3
	// MaxSignificantTermsSize bounds size and shard_size, so that
	// RequestSize cannot overflow.
	MaxSignificantTermsSize = 10000
)

// AggregationSignificantTerms is the significant_terms (and significant_text)
// aggregation. At most one heuristic is set, JLH is the default.
type AggregationSignificantTerms struct {
	Field            string      `json:"field"`
	Size             int         `json:"size"`          // default 10
	ShardSize        int         `json:"shard_size"`    // foreground terms collected before scoring
	MinDocCount      *int        `json:"min_doc_count"` // default 3
	BackgroundFilter interface{} `json:"background_filter"`
	Include          interface{} `json:"include"` // regexp string or array of terms
	Exclude          interface{} `json:"exclude"` // regexp string or array of terms

	JLH               *struct{}              `json:"jlh"`
	MutualInformation *SignificanceHeuristic `json:"mutual_information"`
	ChiSquare         *SignificanceHeuristic `json:"chi_square"`
	GND               *SignificanceHeuristic `json:"gnd"`
	Percentage        *struct{}              `json:"percentage"`

	// significant_text options, not supported: decoded only to reject
	// filter_duplicate_text: true and a non-empty source_fields; hidden from swagger
	FilterDuplicateText bool     `json:"filter_duplicate_text" swaggerignore:"true"`
	SourceFields        []string `json:"source_fields" swaggerignore:"true"`
}

// SignificanceHeuristic holds the options of the mutual_information,
// chi_square and gnd heuristics.
type SignificanceHeuristic struct {
	IncludeNegatives     bool  `json:"include_negatives"`      // default false
	BackgroundIsSuperset *bool `json:"background_is_superset"` // default true
}

// RequestSize is the number of foreground terms collected by the underlying
// terms aggregation: max(shard_size, size*5, 100).
func (a *AggregationSignificantTerms) RequestSize() int {
	return max(a.ShardSize, a.ResponseSize()*5, 100)
}

// ResponseSize is the number of buckets returned, default 10.
func (a *AggregationSignificantTerms) ResponseSize() int {
	if a.Size > 0 {
		return a.Size
	}
	return DefaultSignificantTermsSize
}
