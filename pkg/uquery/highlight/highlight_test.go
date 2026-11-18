/* Copyright 2022 Zinc Labs Inc. and Contributors
* Copyright 2026 AtomAI
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

package highlight

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcaesar/riot/analysis"
	"github.com/vcaesar/riot/analysis/analyzer"
	"github.com/vcaesar/riot/search"
	"github.com/vcaesar/riot/search/highlight"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

func TestRequest(t *testing.T) {
	t.Run("no fields is a noop", func(t *testing.T) {
		h := &meta.Highlight{}
		assert.NoError(t, Request(h))
		assert.Equal(t, 0, h.NumberOfFragments)
	})

	t.Run("defaults propagate to fields", func(t *testing.T) {
		h := &meta.Highlight{
			FragmentSize: 100,
			Fields: map[string]*meta.Highlight{
				"title": {},
				"body":  {FragmentSize: 20, NumberOfFragments: 1},
			},
		}
		assert.NoError(t, Request(h))
		assert.Equal(t, 3, h.NumberOfFragments)
		assert.Equal(t, 100, h.Fields["title"].FragmentSize)
		assert.Equal(t, 3, h.Fields["title"].NumberOfFragments)
		// explicit per-field values are kept
		assert.Equal(t, 20, h.Fields["body"].FragmentSize)
		assert.Equal(t, 1, h.Fields["body"].NumberOfFragments)
	})

	t.Run("zero global fragment size leaves field untouched", func(t *testing.T) {
		h := &meta.Highlight{NumberOfFragments: 5, Fields: map[string]*meta.Highlight{"f": {}}}
		assert.NoError(t, Request(h))
		assert.Equal(t, 0, h.Fields["f"].FragmentSize)
		assert.Equal(t, 5, h.Fields["f"].NumberOfFragments)
	})
}

func TestLocations(t *testing.T) {
	std := analyzer.NewStandardAnalyzer()
	value := []byte("Quick fox, quick brown fox")
	tests := []struct {
		name    string
		zer     *analysis.Analyzer
		value   []byte
		matched search.TermLocationMap
		want    search.TermLocationMap
	}{
		{
			name:    "term and position, offsets zero in the index",
			zer:     std,
			value:   value,
			matched: search.TermLocationMap{"quick": {{Pos: 3}}, "brown": {{Pos: 4}}},
			want: search.TermLocationMap{
				"quick": {{Pos: 3, Start: 11, End: 16}},
				"brown": {{Pos: 4, Start: 17, End: 22}},
			},
		},
		{
			name:    "shifted positions fall back to terms",
			zer:     std,
			value:   value,
			matched: search.TermLocationMap{"fox": {{Pos: 102}}},
			want:    search.TermLocationMap{"fox": {{Pos: 2, Start: 6, End: 9}, {Pos: 5, Start: 23, End: 26}}},
		},
		{
			name:    "no match",
			zer:     std,
			value:   value,
			matched: search.TermLocationMap{"slow": {{Pos: 1}}},
			want:    search.TermLocationMap{},
		},
		{
			name:    "keyword",
			value:   []byte("New York"),
			matched: search.TermLocationMap{"New York": {{Pos: 1}}},
			want:    search.TermLocationMap{"New York": {{Pos: 1, Start: 0, End: 8}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Locations(tt.zer, tt.value, tt.matched))
		})
	}

	got := highlight.NewHTMLHighlighter().BestFragments(Locations(std, value, tests[0].matched), value, 1)
	assert.Equal(t, []string{"Quick fox, <mark>quick</mark> <mark>brown</mark> fox"}, got)
}
