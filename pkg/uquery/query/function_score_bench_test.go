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

package query

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/search"

	"github.com/vcaesar/zedsearch/pkg/bluge/aggregation"
	"github.com/vcaesar/zedsearch/pkg/meta"
)

// BenchmarkFunctionScore compares a plain query with the same query wrapped
// in the function_score the console sends, both with a date histogram.
func BenchmarkFunctionScore(b *testing.B) {
	writer, err := riot.OpenWriter(riot.InMemoryOnlyConfig())
	require.NoError(b, err)
	defer func() { require.NoError(b, writer.Close()) }()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	batch := riot.NewBatch()
	for i := range 100_000 {
		id := strconv.Itoa(i)
		batch.Update(riot.Identifier(id), riot.NewDocument(id).
			AddField(riot.NewTextField("name", "gold item "+id)).
			AddField(riot.NewNumericField("age", float64(i%100)).Aggregatable()).
			AddField(riot.NewDateTimeField(meta.TimeFieldName, start.Add(time.Duration(i)*time.Second)).Aggregatable()))
	}
	require.NoError(b, writer.Batch(batch))
	reader, err := writer.Reader()
	require.NoError(b, err)
	defer func() { require.NoError(b, reader.Close()) }()

	inner := map[string]interface{}{"match": map[string]interface{}{"name": "gold"}}
	plain, err := Query(inner, testMappings(), nil)
	require.NoError(b, err)
	scored, err := FunctionScoreQuery(map[string]interface{}{
		"query":              inner,
		"field_value_factor": map[string]interface{}{"field": "age", "modifier": "sqrt", "missing": 1},
	}, testMappings(), nil)
	require.NoError(b, err)

	for _, bc := range []struct {
		name  string
		query riot.Query
	}{{"plain", plain}, {"function_score", scored}} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				req := riot.NewTopNSearch(50, bc.query).SortBy([]string{"-_score", "-" + meta.TimeFieldName}).WithStandardAggregations()
				req.AddAggregation("histogram", aggregation.NewDateHistogramAggregation(search.Field(meta.TimeFieldName), "", int64(time.Hour), "", time.UTC, nil, nil, 0, 0))
				dmi, err := reader.Search(context.Background(), req)
				require.NoError(b, err)
				for match, err := dmi.Next(); match != nil || err != nil; match, err = dmi.Next() {
					require.NoError(b, err)
				}
			}
		})
	}
}
