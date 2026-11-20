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
	"fmt"
	"math"
	"regexp"
	"sort"

	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/analysis"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/uquery/query"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

// heuristic scores a term from its foreground (subset) and background
// (superset) document frequencies.
type heuristic func(fg, fgSize, bg, bgSize float64) float64

type significance struct {
	ctx       context.Context
	readers   []*riot.Reader
	mappings  *meta.Mappings
	analyzers map[string]*analysis.Analyzer
	// total loads the background size, numDocs once loaded
	total   func() (uint64, error)
	numDocs *uint64
}

// SignificantTerms rewrites the responses of the significant_terms and
// significant_text aggregations of req, collected by aggregation.Request as
// terms aggregations, into significance buckets: the foreground counts are
// scored against the background frequencies of the terms in the readers
// (or in the documents matching background_filter). fgCount is the number of
// documents matched by the search; nested aggregations use the doc_count of
// their parent bucket as foreground size. The readers are those of the
// search, so a time range query narrows the background to the shards in it.
func SignificantTerms(ctx context.Context, readers []*riot.Reader, aggs map[string]meta.AggregationResponse, req map[string]meta.Aggregations, fgCount uint64, mappings *meta.Mappings, analyzers map[string]*analysis.Analyzer) error {
	if len(req) == 0 || len(aggs) == 0 {
		return nil
	}
	if mappings == nil {
		mappings = meta.NewMappings()
	}
	s := &significance{ctx: ctx, readers: readers, mappings: mappings, analyzers: analyzers, total: func() (uint64, error) {
		return totalDocs(readers)
	}}
	return s.walk(aggs, req, fgCount)
}

// bgTotal is the number of documents of the readers, counted on first use.
func (s *significance) bgTotal() (uint64, error) {
	if s.numDocs == nil {
		n, err := s.total()
		if err != nil {
			return 0, err
		}
		s.numDocs = &n
	}
	return *s.numDocs, nil
}

func (s *significance) walk(resp map[string]meta.AggregationResponse, req map[string]meta.Aggregations, fgSize uint64) error {
	for name, agg := range req {
		r, ok := resp[name]
		if !ok {
			continue
		}
		kind, sig := "significant_terms", agg.SignificantTerms
		if sig == nil {
			kind, sig = "significant_text", agg.SignificantText
		}
		if sig != nil {
			if err := s.rewrite(kind, &r, sig, fgSize); err != nil {
				return err
			}
		}
		if len(agg.Aggregations) > 0 {
			if err := s.walkBuckets(&r, agg.Aggregations); err != nil {
				return err
			}
		}
		resp[name] = r
	}
	return nil
}

// walkBuckets descends into the sub-aggregations of every bucket of r.
func (s *significance) walkBuckets(r *meta.AggregationResponse, req map[string]meta.Aggregations) error {
	if r.Aggregations != nil && r.DocCount != nil {
		if err := s.walk(r.Aggregations, req, *r.DocCount); err != nil {
			return err
		}
	}
	switch buckets := r.Buckets.(type) {
	case []map[string]interface{}:
		for _, bucket := range buckets {
			if err := s.walkBucket(bucket, req); err != nil {
				return err
			}
		}
	case map[string]interface{}:
		for key, v := range buckets {
			sub, ok := v.(meta.AggregationResponse)
			if !ok || sub.Aggregations == nil || sub.DocCount == nil {
				continue
			}
			if err := s.walk(sub.Aggregations, req, *sub.DocCount); err != nil {
				return err
			}
			buckets[key] = sub
		}
	}
	return nil
}

func (s *significance) walkBucket(bucket map[string]interface{}, req map[string]meta.Aggregations) error {
	sub := make(map[string]meta.AggregationResponse)
	for name := range req {
		if v, ok := bucket[name].(meta.AggregationResponse); ok {
			sub[name] = v
		}
	}
	if len(sub) == 0 {
		return nil
	}
	fgSize, _ := zutils.ToUint64(bucket["doc_count"])
	if err := s.walk(sub, req, fgSize); err != nil {
		return err
	}
	for name, v := range sub {
		bucket[name] = v
	}
	return nil
}

type scoredBucket struct {
	key    string
	score  float64
	bucket map[string]interface{}
}

func (s *significance) rewrite(kind string, r *meta.AggregationResponse, sig *meta.AggregationSignificantTerms, fgSize uint64) error {
	score, superset, err := sigHeuristic(kind, sig)
	if err != nil {
		return err
	}
	include, err := parseTermFilter(kind, "include", sig.Include)
	if err != nil {
		return err
	}
	exclude, err := parseTermFilter(kind, "exclude", sig.Exclude)
	if err != nil {
		return err
	}
	minDocCount := uint64(meta.DefaultSignificantTermsMinDocCount)
	if sig.MinDocCount != nil {
		minDocCount = uint64(max(*sig.MinDocCount, 0))
	}

	var bgFilter riot.Query
	var bgSize uint64
	if sig.BackgroundFilter != nil {
		if bgFilter, err = query.Query(sig.BackgroundFilter, s.mappings, s.analyzers); err != nil {
			return errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[%s] background_filter parse err", kind)).Cause(err)
		}
		bgSize, err = countMatches(s.ctx, s.readers, bgFilter)
	} else {
		bgSize, err = s.bgTotal()
	}
	if err != nil {
		return err
	}
	if superset && fgSize > bgSize {
		return notSuperset(kind, "subset size", fgSize, "superset size", bgSize)
	}
	prop, _ := s.mappings.GetProperty(sig.Field)
	useDict := bgFilter == nil && (prop.Type == "" || prop.Type == "text" || prop.Type == "keyword")

	buckets, _ := r.Buckets.([]map[string]interface{})
	scored := make([]scoredBucket, 0, len(buckets))
	for _, bucket := range buckets {
		key, _ := zutils.ToString(bucket["key"])
		if v, ok := bucket["key_as_string"].(string); ok {
			key = v
		}
		fg, _ := zutils.ToUint64(bucket["doc_count"])
		if fg < minDocCount || (include != nil && !include.match(key)) || (exclude != nil && exclude.match(key)) {
			continue
		}
		var bg uint64
		if useDict {
			bg, err = docFreq(s.ctx, s.readers, sig.Field, key)
		} else {
			bg, err = s.filteredFreq(sig.Field, key, bgFilter)
		}
		if err != nil {
			return err
		}
		if superset && fg > bg {
			return notSuperset(kind, "subset freq of ["+key+"]", fg, "superset freq", bg)
		}
		// fgSize-fg > bgSize-bg, written without unsigned underflow
		if superset && fgSize+bg > bgSize+fg {
			return notSuperset(kind, "subset docs without ["+key+"]", fgSize-min(fg, fgSize), "superset docs without it", bgSize-min(bg, bgSize))
		}
		v := score(float64(fg), float64(fgSize), float64(bg), float64(bgSize))
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			continue
		}
		out := make(map[string]interface{}, len(bucket)+2)
		for k, vv := range bucket {
			out[k] = vv
		}
		out["score"] = v
		out["bg_count"] = bg
		scored = append(scored, scoredBucket{key: key, score: v, bucket: out})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].key < scored[j].key
	})
	if size := sig.ResponseSize(); len(scored) > size {
		scored = scored[:size]
	}
	rv := make([]map[string]interface{}, len(scored))
	for i, b := range scored {
		rv[i] = b.bucket
	}
	r.Buckets = rv
	r.DocCount = &fgSize
	r.BgCount = &bgSize
	return nil
}

// filteredFreq counts the documents holding value in field, within the
// background filter when set.
func (s *significance) filteredFreq(field, value string, filter riot.Query) (uint64, error) {
	tq, err := query.TermQuery(map[string]interface{}{field: value}, s.mappings)
	if err != nil {
		return 0, err
	}
	q := riot.NewBooleanQuery().AddMust(tq)
	if filter != nil {
		q.AddMust(filter)
	}
	return countMatches(s.ctx, s.readers, q)
}

// notSuperset reports foreground counts above the background ones, which
// background_is_superset forbids.
func notSuperset(kind, fgName string, fg uint64, bgName string, bg uint64) error {
	return errors.New(errors.ErrorTypeIllegalArgumentException,
		fmt.Sprintf("[%s] %s [%d] > %s [%d], try to set background_is_superset: false", kind, fgName, fg, bgName, bg))
}

// sigHeuristic returns the requested score and whether it requires the
// background to be a superset of the foreground.
func sigHeuristic(kind string, sig *meta.AggregationSignificantTerms) (heuristic, bool, error) {
	var rv heuristic
	var isSuperset bool
	n := 0
	set := func(h heuristic, superset bool) {
		rv, isSuperset = h, superset
		n++
	}
	if sig.JLH != nil {
		set(jlhScore, false)
	}
	if sig.Percentage != nil {
		set(percentageScore, false)
	}
	if h := sig.MutualInformation; h != nil {
		set(func(fg, fgSize, bg, bgSize float64) float64 {
			return mutualInformationScore(fg, fgSize, bg, bgSize, h.IncludeNegatives, superset(h))
		}, superset(h))
	}
	if h := sig.ChiSquare; h != nil {
		set(func(fg, fgSize, bg, bgSize float64) float64 {
			return chiSquareScore(fg, fgSize, bg, bgSize, h.IncludeNegatives, superset(h))
		}, superset(h))
	}
	if h := sig.GND; h != nil {
		set(func(fg, fgSize, bg, bgSize float64) float64 {
			return gndScore(fg, fgSize, bg, bgSize, superset(h))
		}, superset(h))
	}
	switch n {
	case 0:
		return jlhScore, false, nil
	case 1:
		return rv, isSuperset, nil
	default:
		return nil, false, errors.New(errors.ErrorTypeParsingException, fmt.Sprintf("[%s] only one significance heuristic is allowed", kind))
	}
}

func superset(h *meta.SignificanceHeuristic) bool {
	return h.BackgroundIsSuperset == nil || *h.BackgroundIsSuperset
}

// jlhScore is the Elasticsearch JLH score: absolute times relative change of
// the term probability between background and foreground.
func jlhScore(fg, fgSize, bg, bgSize float64) float64 {
	if fgSize == 0 || bgSize == 0 {
		return 0
	}
	if bg == 0 {
		bg = 1 // background not a strict superset
	}
	fgPct, bgPct := fg/fgSize, bg/bgSize
	if fgPct <= bgPct {
		return 0
	}
	return (fgPct - bgPct) * (fgPct / bgPct)
}

func percentageScore(fg, _, bg, _ float64) float64 {
	if bg == 0 {
		return 0
	}
	return fg / bg
}

// nxy are the document counts of the 2x2 contingency table: N<term><class>.
type nxy struct {
	n00, n01, n10, n11, n0_, n1_, n_0, n_1, n float64
}

func frequencies(fg, fgSize, bg, bgSize float64, backgroundIsSuperset bool) nxy {
	if backgroundIsSuperset {
		return nxy{
			n00: bgSize - bg - (fgSize - fg),
			n01: fgSize - fg,
			n10: bg - fg,
			n11: fg,
			n0_: bgSize - bg,
			n1_: bg,
			n_0: bgSize - fgSize,
			n_1: fgSize,
			n:   bgSize,
		}
	}
	return nxy{
		n00: bgSize - bg,
		n01: fgSize - fg,
		n10: bg,
		n11: fg,
		n0_: bgSize - bg + fgSize - fg,
		n1_: bg + fg,
		n_0: bgSize,
		n_1: fgSize,
		n:   bgSize + fgSize,
	}
}

// negative reports a term more frequent outside the foreground than in it.
func (f nxy) negative() bool {
	return f.n11/f.n_1 < f.n10/f.n_0
}

func mutualInformationScore(fg, fgSize, bg, bgSize float64, includeNegatives, backgroundIsSuperset bool) float64 {
	f := frequencies(fg, fgSize, bg, bgSize, backgroundIsSuperset)
	score := (miTerm(f.n00, f.n0_, f.n_0, f.n) +
		miTerm(f.n01, f.n0_, f.n_1, f.n) +
		miTerm(f.n10, f.n1_, f.n_0, f.n) +
		miTerm(f.n11, f.n1_, f.n_1, f.n)) / math.Ln2
	if math.IsNaN(score) || (!includeNegatives && f.negative()) {
		return math.Inf(-1)
	}
	return score
}

func miTerm(nxy, nx_, n_y, n float64) float64 {
	numerator := math.Abs(n * nxy)
	denominator := math.Abs(nx_ * n_y)
	factor := math.Abs(nxy / n)
	if numerator < 1e-7 && factor < 1e-7 {
		return 0
	}
	return factor * math.Log(numerator/denominator)
}

func chiSquareScore(fg, fgSize, bg, bgSize float64, includeNegatives, backgroundIsSuperset bool) float64 {
	f := frequencies(fg, fgSize, bg, bgSize, backgroundIsSuperset)
	if !includeNegatives && f.negative() {
		return math.Inf(-1)
	}
	d := f.n11*f.n00 - f.n01*f.n10
	return f.n * d * d / (f.n_1 * f.n1_ * f.n0_ * f.n_0)
}

// gndScore is the Google normalized distance, inverted so that related terms
// score high.
func gndScore(fg, fgSize, bg, bgSize float64, backgroundIsSuperset bool) float64 {
	fx, fy, fxy, n := bg, fgSize, fg, bgSize
	if !backgroundIsSuperset {
		fx += fg
		n += fgSize
	}
	if fxy == 0 {
		return 0
	}
	fx = max(fx, fxy)
	if fx == fy && fx == fxy {
		return 1
	}
	score := (math.Max(math.Log(fx), math.Log(fy)) - math.Log(fxy)) /
		(math.Log(n) - math.Min(math.Log(fx), math.Log(fy)))
	return math.Exp(-score)
}

// termFilter is an include / exclude clause: a regexp or a list of terms.
type termFilter struct {
	re    *regexp.Regexp
	terms map[string]struct{}
}

func parseTermFilter(kind, key string, v interface{}) (*termFilter, error) {
	switch v := v.(type) {
	case nil:
		return nil, nil
	case string:
		re, err := regexp.Compile("^(?:" + v + ")$")
		if err != nil {
			return nil, errors.New(errors.ErrorTypeIllegalArgumentException, fmt.Sprintf("[%s] invalid %s regexp [%s]", kind, key, v)).Cause(err)
		}
		return &termFilter{re: re}, nil
	case []interface{}:
		f := &termFilter{terms: make(map[string]struct{}, len(v))}
		for _, vv := range v {
			s, err := zutils.ToString(vv)
			if err != nil {
				return nil, err
			}
			f.terms[s] = struct{}{}
		}
		return f, nil
	default:
		return nil, errors.New(errors.ErrorTypeNotImplemented, fmt.Sprintf("[%s] %s doesn't support values of type: %T", kind, key, v))
	}
}

func (f *termFilter) match(term string) bool {
	if f.re != nil {
		return f.re.MatchString(term)
	}
	_, ok := f.terms[term]
	return ok
}
