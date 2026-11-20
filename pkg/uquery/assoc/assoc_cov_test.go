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

package assoc

import (
	"context"
	"errors"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	segment "github.com/vcaesar/bluge_segment_api"
	ice "github.com/vcaesar/ice"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/index"

	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

var errFault = errors.New("injected fault")

const faultyType = "faulty"

// faults injects reader errors. next returns how many entries a dictionary
// iteration of field over [start, end) yields before failing, -1 for none;
// stored fails every stored field visit.
type faults struct {
	next   func(field string, start, end []byte) int
	stored error
}

type faultySegment struct {
	segment.Segment
	f *faults
}

func (s *faultySegment) Type() string { return faultyType }

func (s *faultySegment) Dictionary(field string) (segment.Dictionary, error) {
	d, err := s.Segment.Dictionary(field)
	if err != nil {
		return nil, err
	}
	return &faultyDict{Dictionary: d, field: field, f: s.f}, nil
}

func (s *faultySegment) VisitStoredFields(num uint64, visitor segment.StoredFieldVisitor) error {
	if s.f.stored != nil {
		return s.f.stored
	}
	return s.Segment.VisitStoredFields(num, visitor)
}

type faultyDict struct {
	segment.Dictionary
	field string
	f     *faults
}

func (d *faultyDict) Iterator(a segment.Automaton, start, end []byte) segment.DictionaryIterator {
	it := d.Dictionary.Iterator(a, start, end)
	if d.f.next == nil {
		return it
	}
	if n := d.f.next(d.field, start, end); n >= 0 {
		return &faultyIterator{DictionaryIterator: it, left: n}
	}
	return it
}

type faultyIterator struct {
	segment.DictionaryIterator
	left int
}

func (i *faultyIterator) Next() (segment.DictionaryEntry, error) {
	if i.left == 0 {
		return nil, errFault
	}
	i.left--
	return i.DictionaryIterator.Next()
}

// faultyConfig is an in-memory config whose ice segments inject f.
func faultyConfig(f *faults) riot.Config {
	opts := ice.DefaultOptions()
	wrap := func(seg segment.Segment, err error) (segment.Segment, error) {
		if err != nil {
			return nil, err
		}
		return &faultySegment{Segment: seg, f: f}, nil
	}
	plugin := &index.SegmentPlugin{
		Type:    faultyType,
		Version: ice.Version,
		New: func(results []segment.Document, normCalc func(string, int) float32) (segment.Segment, uint64, error) {
			seg, n, err := opts.New(results, normCalc)
			seg, err = wrap(seg, err)
			return seg, n, err
		},
		Load: func(data *segment.Data) (segment.Segment, error) {
			return wrap(opts.Load(data))
		},
		Merge: func(segs []segment.Segment, drops []*roaring.Bitmap, size int) segment.Merger {
			inner := make([]segment.Segment, len(segs))
			for i, s := range segs {
				inner[i] = s.(*faultySegment).Segment
			}
			return ice.Merge(inner, drops, size)
		},
	}
	return riot.DefaultConfigWithIndexConfig(index.InMemoryOnlyConfig().
		WithSegmentPlugin(plugin).WithSegmentType(faultyType).WithSegmentVersion(ice.Version))
}

// searchFailConfig is an in-memory config whose searches fail.
func searchFailConfig() riot.Config {
	return riot.InMemoryOnlyConfig().WithSearchStartFunc(func(uint64) error { return errFault })
}

// openReader indexes docs as documents of index with config.
func openReader(t *testing.T, config riot.Config, index string, docs []*riot.Document) *riot.Reader {
	t.Helper()
	writer, err := riot.OpenWriter(config)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, writer.Close()) })
	batch := riot.NewBatch()
	for _, doc := range docs {
		batch.Update(doc.ID(), doc)
	}
	require.NoError(t, writer.Batch(batch))
	reader, err := writer.Reader()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reader.Close()) })
	return reader
}

func fixtureDocs(t *testing.T, index string, docs []testDoc) []*riot.Document {
	t.Helper()
	rv := make([]*riot.Document, 0, len(docs))
	for _, d := range docs {
		source, err := json.Marshal(map[string]interface{}{"body": d.body, "tag": d.tag, "n": d.n})
		require.NoError(t, err)
		rv = append(rv, riot.NewDocument(d.id).
			AddField(riot.NewTextField("body", d.body)).
			AddField(riot.NewKeywordField("tag", d.tag).Aggregatable()).
			AddField(riot.NewNumericField("n", d.n).Aggregatable()).
			AddField(riot.NewStoredOnlyField("_source", source)).
			AddField(riot.NewStoredOnlyField("_index", []byte(index))))
	}
	return rv
}

const covIndex = "TestAssocCov.index_1"

func faultyReaders(t *testing.T, f *faults) []*riot.Reader {
	t.Helper()
	return []*riot.Reader{openReader(t, faultyConfig(f), covIndex, fixtureDocs(t, covIndex, testDocs))}
}

func searchFailReaders(t *testing.T) []*riot.Reader {
	t.Helper()
	return []*riot.Reader{openReader(t, searchFailConfig(), covIndex, fixtureDocs(t, covIndex, testDocs))}
}

// failField fails every iteration of field after n entries.
func failField(field string, n int) func(string, []byte, []byte) int {
	return func(f string, _, _ []byte) int {
		if f == field {
			return n
		}
		return -1
	}
}

// isTerm reports a single term iteration of docFreq.
func isTerm(start, end []byte) bool {
	return len(end) == len(start)+1 && end[len(end)-1] == 0 && string(end[:len(start)]) == string(start)
}

func TestFaultyReader_Passthrough(t *testing.T) {
	readers := faultyReaders(t, &faults{})
	n, err := docFreq(context.Background(), readers, "body", "quick")
	require.NoError(t, err)
	assert.Equal(t, uint64(4), n)
	docs, err := searchDocs(context.Background(), readers, riot.NewTermQuery("7").SetField("_id"), 1)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Equal(t, "food", docs[0].Source["tag"])
}

func TestHelpers_ReaderErrors(t *testing.T) {
	t.Run("dictionary iterator fails at once", func(t *testing.T) {
		_, err := docFreq(context.Background(), faultyReaders(t, &faults{next: failField("body", 0)}), "body", "quick")
		assert.ErrorIs(t, err, errFault)
	})
	t.Run("dictionary iterator fails after a term", func(t *testing.T) {
		_, err := prefixTerms(context.Background(), faultyReaders(t, &faults{next: failField("body", 1)}), "body", "")
		assert.ErrorIs(t, err, errFault)
	})
	t.Run("countMatches search fails", func(t *testing.T) {
		_, err := countMatches(context.Background(), searchFailReaders(t), riot.NewMatchAllQuery())
		assert.ErrorIs(t, err, errFault)
	})
	t.Run("searchDocs search fails", func(t *testing.T) {
		_, err := searchDocs(context.Background(), searchFailReaders(t), riot.NewMatchAllQuery(), 1)
		assert.ErrorIs(t, err, errFault)
	})
	t.Run("stored fields fail", func(t *testing.T) {
		_, err := searchDocs(context.Background(), faultyReaders(t, &faults{stored: errFault}), riot.NewMatchAllQuery(), 1)
		assert.ErrorIs(t, err, errFault)
	})
	t.Run("invalid _source", func(t *testing.T) {
		doc := riot.NewDocument("1").AddField(riot.NewStoredOnlyField("_source", []byte("{not json")))
		reader := openReader(t, riot.InMemoryOnlyConfig(), covIndex, []*riot.Document{doc})
		_, err := searchDocs(context.Background(), []*riot.Reader{reader}, riot.NewMatchAllQuery(), 1)
		assert.Error(t, err)
	})
}

func TestFieldAnalyzer_Mapping(t *testing.T) {
	m := meta.NewMappings()
	prop := meta.NewProperty("text")
	prop.Analyzer = "whitespace"
	prop.SearchAnalyzer = "simple"
	m.SetProperty("both", prop)
	indexOnly := meta.NewProperty("text")
	indexOnly.Analyzer = "whitespace"
	m.SetProperty("index_only", indexOnly)

	tests := []struct {
		name      string
		field     string
		searching bool
		want      string
	}{
		{"search analyzer when searching", "both", true, "hello"},
		{"index analyzer when indexing", "both", false, "Hello"},
		{"index analyzer without search analyzer", "index_only", true, "Hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ana, err := fieldAnalyzer(nil, m, tt.field, "", tt.searching)
			require.NoError(t, err)
			tokens := tokenize(ana, "Hello")
			require.Len(t, tokens, 1)
			assert.Equal(t, tt.want, string(tokens[0].Term))
		})
	}
}

func TestMoreLikeThis_ReaderErrors(t *testing.T) {
	tests := []struct {
		name    string
		readers func(t *testing.T) []*riot.Reader
		body    map[string]interface{}
	}{
		{"like id lookup fails", searchFailReaders, map[string]interface{}{"like": map[string]interface{}{"_id": "1"}}},
		{"unlike id lookup fails", searchFailReaders, map[string]interface{}{"like": "quick fox", "unlike": map[string]interface{}{"_id": "1"}}},
		{"liked artificial doc id lookup fails", searchFailReaders,
			map[string]interface{}{"like": map[string]interface{}{"_id": "1", "doc": map[string]interface{}{"body": "quick"}}}},
		{"doc freq fails", func(t *testing.T) []*riot.Reader { return faultyReaders(t, &faults{next: failField("body", 0)}) },
			map[string]interface{}{"like": "quick", "fields": []interface{}{"body"}, "min_term_freq": 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := MoreLikeThis(context.Background(), tt.readers(t), tt.body, testMappings(), nil)
			assert.ErrorIs(t, err, errFault)
		})
	}
}

func TestMoreLikeThis_FieldTieBreak(t *testing.T) {
	// "alpha" has the same tf and df in body and tag: the field orders them
	reader := newIndexReader(t, "TestMoreLikeThis_FieldTieBreak.index_1", []testDoc{
		{"1", "alpha", "alpha", 1},
		{"2", "beta", "beta", 2},
	})
	q, err := MoreLikeThis(context.Background(), []*riot.Reader{reader}, map[string]interface{}{
		"like": "alpha", "fields": []interface{}{"tag", "body"}, "min_term_freq": 1, "min_doc_freq": 1,
	}, testMappings(), nil)
	require.NoError(t, err)
	terms, _ := mltTerms(t, q)
	assert.Equal(t, []string{"body:alpha", "tag:alpha"}, terms)
}

func TestSignificantTerms_ReaderErrors(t *testing.T) {
	t.Run("filtered freq search fails", func(t *testing.T) {
		resp := map[string]meta.AggregationResponse{"sig": {Buckets: []map[string]interface{}{{"key": float64(1), "doc_count": uint64(1)}}}}
		req := map[string]meta.Aggregations{"sig": {SignificantTerms: &meta.AggregationSignificantTerms{Field: "n", MinDocCount: intPtr(1)}}}
		assert.ErrorIs(t, SignificantTerms(context.Background(), searchFailReaders(t), resp, req, 1, testMappings(), nil), errFault)
	})
	t.Run("non numeric key of numeric field", func(t *testing.T) {
		resp := map[string]meta.AggregationResponse{"sig": {Buckets: []map[string]interface{}{{"key": "abc", "doc_count": uint64(1)}}}}
		req := map[string]meta.Aggregations{"sig": {SignificantTerms: &meta.AggregationSignificantTerms{Field: "n", MinDocCount: intPtr(1)}}}
		assert.Error(t, SignificantTerms(context.Background(), testReaders(t), resp, req, 1, testMappings(), nil))
	})
	t.Run("include list of numbers", func(t *testing.T) {
		f, err := parseTermFilter("significant_terms", "include", []interface{}{float64(1), true})
		require.NoError(t, err)
		assert.True(t, f.match("1"))
		assert.True(t, f.match("true"))
	})
}

func TestSuggest_ReaderErrors(t *testing.T) {
	body := faults{next: failField("body", 0)}
	tag := faults{next: failField("tag", 0)}
	// docFreq of the input passes, the prefix iteration fails
	prefix := faults{next: func(_ string, start, end []byte) int {
		if isTerm(start, end) {
			return -1
		}
		return 0
	}}
	// the doc freq of the candidate "quick" fails
	candidate := faults{next: func(_ string, start, end []byte) int {
		if isTerm(start, end) && string(start) == "quick" {
			return 0
		}
		return -1
	}}
	tests := []struct {
		name string
		f    *faults
		body map[string]interface{}
	}{
		{"term doc freq", &body, term("quikc", map[string]interface{}{})},
		{"term prefix terms", &prefix, term("quikc", map[string]interface{}{})},
		{"term candidate doc freq", &candidate, term("quikc", map[string]interface{}{})},
		{"phrase candidates", &body, phrase("quikc", map[string]interface{}{})},
		{"completion regex terms", &tag, map[string]interface{}{"s": map[string]interface{}{"regex": "t.*", "completion": map[string]interface{}{"field": "tag"}}}},
		{"completion prefix terms", &tag, completion("te", map[string]interface{}{})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Suggest(context.Background(), faultyReaders(t, tt.f), tt.body, testMappings(), nil)
			assert.ErrorIs(t, err, errFault)
		})
	}

	t.Run("completion docs search", func(t *testing.T) {
		_, err := Suggest(context.Background(), searchFailReaders(t), completion("te", map[string]interface{}{}), testMappings(), nil)
		assert.ErrorIs(t, err, errFault)
	})
}

func TestSuggest_TermTieBreak(t *testing.T) {
	// cats, cots and cuts are one edit from cxts: the freq, then the text
	// order the equal scores
	reader := newIndexReader(t, "TestSuggest_TermTieBreak.index_1", []testDoc{
		{"1", "cats", "a", 1},
		{"2", "cats", "a", 2},
		{"3", "cuts", "a", 3},
		{"4", "cots", "a", 4},
	})
	rv, err := Suggest(context.Background(), []*riot.Reader{reader}, term("cxts", map[string]interface{}{}), testMappings(), nil)
	require.NoError(t, err)
	entries := rv["s"].([]interface{})
	require.Len(t, entries, 1)
	var got []string
	for _, o := range entries[0].(map[string]interface{})["options"].([]interface{}) {
		got = append(got, o.(map[string]interface{})["text"].(string))
	}
	assert.Equal(t, []string{"cats", "cots", "cuts"}, got)
}

func TestSuggest_CompletionOptions(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]interface{}
		want  []string
	}{
		{"accepted analyzer", completion("an", map[string]interface{}{"analyzer": "simple", "size": 1}), []string{"animal"}},
		{"fuzzy auto short input", completion("te", map[string]interface{}{"fuzzy": map[string]interface{}{"min_length": 1, "unicode_aware": true}, "skip_duplicates": true}), []string{"tech"}},
		{"fuzzy auto long input", completion("animmall", map[string]interface{}{"fuzzy": true, "skip_duplicates": true}), []string{"animal"}},
		{"size reached across terms", completion("f", map[string]interface{}{"field": "body", "size": 1}), []string{"fast"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := suggestEntries(t, tt.input)
			require.Len(t, entries, 1)
			assert.Equal(t, tt.want, texts(entries[0].options))
		})
	}
}

// TestContext_Canceled pins that the dictionary walks and searches of the
// package stop with the error of a done request context.
func TestContext_Canceled(t *testing.T) {
	readers := testReaders(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		run  func() error
	}{
		{"docFreq", func() error {
			_, err := docFreq(ctx, readers, "body", "quick")
			return err
		}},
		{"docFreq of an absent term", func() error {
			_, err := docFreq(ctx, readers, "body", "absent")
			return err
		}},
		{"docFreq without readers", func() error {
			_, err := docFreq(ctx, nil, "body", "quick")
			return err
		}},
		{"prefixTerms", func() error {
			_, err := prefixTerms(ctx, readers, "body", "")
			return err
		}},
		{"countMatches", func() error {
			_, err := countMatches(ctx, readers, riot.NewMatchAllQuery())
			return err
		}},
		{"searchDocs", func() error {
			_, err := searchDocs(ctx, readers, riot.NewMatchAllQuery(), 1)
			return err
		}},
		{"Suggest", func() error {
			_, err := Suggest(ctx, readers, term("quikc", map[string]interface{}{"prefix_length": 0}), testMappings(), nil)
			return err
		}},
		{"MoreLikeThis", func() error {
			_, err := MoreLikeThis(ctx, readers, map[string]interface{}{"like": "quick brown fox", "fields": []interface{}{"body"}, "min_term_freq": 1}, testMappings(), nil)
			return err
		}},
		{"SignificantTerms", func() error {
			resp := map[string]meta.AggregationResponse{"sig": textResponse()}
			req := map[string]meta.Aggregations{"sig": {SignificantText: &meta.AggregationSignificantTerms{Field: "body", MinDocCount: intPtr(1)}}}
			return SignificantTerms(ctx, readers, resp, req, 4, testMappings(), nil)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ErrorIs(t, tt.run(), context.Canceled)
		})
	}
}

// countdownCtx is canceled once Err has been asked n times.
type countdownCtx struct {
	context.Context
	n int
}

func (c *countdownCtx) Err() error {
	if c.n <= 0 {
		return context.Canceled
	}
	c.n--
	return nil
}

// TestSuggest_CanceledMidScan pins that every cancellation point of the term
// and completion scans, including the loops over the collected terms, stops
// the suggest with the context error.
func TestSuggest_CanceledMidScan(t *testing.T) {
	readers := testReaders(t)
	tests := []struct {
		name    string
		suggest map[string]interface{}
	}{
		{"term", term("quikc", map[string]interface{}{"prefix_length": 0})},
		{"completion regex", map[string]interface{}{"s": map[string]interface{}{"regex": ".*", "completion": map[string]interface{}{"field": "tag"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for n := 0; ; n++ {
				_, err := Suggest(&countdownCtx{Context: context.Background(), n: n}, readers, tt.suggest, testMappings(), nil)
				if err == nil {
					assert.Positive(t, n)
					return
				}
				require.ErrorIs(t, err, context.Canceled, "n=%d", n)
			}
		})
	}
}
