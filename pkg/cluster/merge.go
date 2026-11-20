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

package cluster

import (
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/uquery/aggregation"
	"github.com/vcaesar/zedsearch/pkg/uquery/source"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// PartialAggName is the sub-aggregation a node attaches to an average so
// the coordinator can weight it: {"count": n, "sum": s}.
const PartialAggName = "_partial"

// ScatterQuery is the query every owner runs for a scattered search: the
// whole window, so the merge can re-sort it, and more terms buckets than
// asked for, so a term outside the top of one node still counts when it is
// in the merged top. query is left alone, MergeSearch needs it as asked.
func ScatterQuery(query *meta.ZedQuery) *meta.ZedQuery {
	sub := *query
	sub.From = 0
	if query.From+query.Size > 0 {
		sub.Size = query.From + query.Size
	}
	sub.Aggregations = scatterAggs(query.Aggregations)
	return &sub
}

// scatterAggs copies defs with every terms size raised to Elasticsearch's
// shard_size default, size * 1.5 + 10, and every top_hits asked for its
// whole window and its sort field: the merge applies from and orders by it.
// Pipelines are left out, they read the merged buckets.
func scatterAggs(defs map[string]meta.Aggregations) map[string]meta.Aggregations {
	if len(defs) == 0 {
		return defs
	}
	out := make(map[string]meta.Aggregations, len(defs))
	for name, def := range defs {
		if isPipeline(def) {
			continue
		}
		switch {
		case def.Terms != nil:
			terms := *def.Terms
			terms.Size = termsSize(def)*3/2 + 10
			def.Terms = &terms
		case def.MultiTerms != nil:
			terms := *def.MultiTerms
			terms.Size = termsSize(def)*3/2 + 10
			def.MultiTerms = &terms
		case def.TopHits != nil:
			def.TopHits = scatterTopHits(def.TopHits)
		}
		def.Aggregations = scatterAggs(def.Aggregations)
		out[name] = def
	}
	return out
}

// scatterTopHits is the top_hits an owner runs, def itself when the merge
// needs nothing more than was asked for.
func scatterTopHits(def *meta.AggregationTopHits) *meta.AggregationTopHits {
	fields, widened := sortSource(def)
	paged := def.From > 0 && def.Size >= 0
	if !widened && !paged {
		return def
	}
	hits := *def
	if paged {
		hits.From, hits.Size = 0, def.From+topHitsSize(def)
	}
	if widened {
		hits.Source = fields
	}
	return &hits
}

// sortSource is the _source an owner returns for a top_hits sorted by a
// field its _source leaves out: the hits carry no sort values, the merge
// reads the field from _source and then filters _source as asked. False
// when the field is there anyway.
func sortSource(def *meta.AggregationTopHits) ([]interface{}, bool) {
	field, _, _ := parseSort(def.Sort)
	if field == "" || field == "_score" {
		return nil, false
	}
	switch fields := def.Source.(type) {
	case bool:
		if !fields {
			return []interface{}{field}, true
		}
	case []interface{}:
		// an empty list is the whole _source
		if len(fields) > 0 && !slices.Contains(fields, interface{}(field)) {
			return append(slices.Clone(fields), field), true
		}
	}
	return nil, false
}

// topHitsSize is the number of hits a top_hits returns, the default included.
func topHitsSize(def *meta.AggregationTopHits) int {
	if def.Size == 0 {
		return 3 // elasticsearch default, as on the nodes
	}
	return def.Size
}

// MergeSearch combines the per-owner responses of a scatter-gather search
// into the single response the client expects.
//
// Hits are re-sorted globally and cut down to the requested window, counts
// are summed and aggregations are merged according to the aggregation type
// taken from the original query, which the owners ran as ScatterQuery.
// Aggregations that cannot be merged exactly (cardinality, percentiles)
// keep the first node's value and are logged, they are approximations
// across nodes. Pipeline aggregations are evaluated here, over the merged
// buckets.
func MergeSearch(responses []*meta.SearchResponse, query *meta.ZedQuery) *meta.SearchResponse {
	out := &meta.SearchResponse{Hits: meta.Hits{Hits: []meta.Hit{}}}
	aggs := make(map[string]map[string]interface{})
	aggDocs := make(map[string]float64) // docs matched by the responses merged so far
	var names []string

	for _, resp := range responses {
		if resp == nil {
			continue
		}
		if resp.Took > out.Took {
			out.Took = resp.Took
		}
		out.TimedOut = out.TimedOut || resp.TimedOut
		out.Shards.Total += resp.Shards.Total
		out.Shards.Successful += resp.Shards.Successful
		out.Shards.Skipped += resp.Shards.Skipped
		out.Shards.Failed += resp.Shards.Failed
		if out.Error == "" {
			out.Error = resp.Error
		}
		out.Hits.Total.Value += resp.Hits.Total.Value
		if resp.Hits.MaxScore > out.Hits.MaxScore {
			out.Hits.MaxScore = resp.Hits.MaxScore
		}
		out.Hits.Hits = append(out.Hits.Hits, resp.Hits.Hits...)

		docs := float64(resp.Hits.Total.Value)
		for name, agg := range resp.Aggregations {
			generic := aggToGeneric(agg)
			existing, ok := aggs[name]
			if !ok {
				names = append(names, name)
				aggs[name] = generic
			} else {
				aggs[name] = mergeAgg(existing, generic, aggDef(query, name), name, aggDocs[name], docs)
			}
			aggDocs[name] += docs
		}
	}

	sortHits(out.Hits.Hits, query)
	out.Hits.Hits = windowHits(out.Hits.Hits, query)

	if len(names) > 0 {
		out.Aggregations = make(map[string]meta.AggregationResponse, len(names))
		for _, name := range names {
			stripPartial(aggs[name])
			finishAgg(aggs[name], aggDef(query, name))
			out.Aggregations[name] = aggFromGeneric(aggs[name])
		}
		if query != nil && hasPipeline(query.Aggregations) {
			nodeForm(out.Aggregations, query.Aggregations)
			// as on a single node, a pipeline that fails is left out
			if err := aggregation.ApplyPipelines(out.Aggregations, query.Aggregations); err != nil {
				log.Error().Err(err).Msg("cluster: apply pipeline aggregations")
			}
		}
	}
	return out
}

// isPipeline reports whether def reads the buckets of other aggregations
// instead of documents. An owner only has its own buckets, before the merge
// ordered and cut them, so pipelines run once, in MergeSearch.
func isPipeline(def meta.Aggregations) bool {
	return def.AvgBucket != nil || def.SumBucket != nil || def.MinBucket != nil || def.MaxBucket != nil ||
		def.StatsBucket != nil || def.CumulativeSum != nil || def.Derivative != nil || def.SerialDiff != nil ||
		def.MovingFn != nil || def.BucketScript != nil || def.BucketSelector != nil || def.BucketSort != nil
}

func hasPipeline(defs map[string]meta.Aggregations) bool {
	for _, def := range defs {
		if isPipeline(def) || hasPipeline(def.Aggregations) {
			return true
		}
	}
	return false
}

// nodeForm renders merged aggregations the way a node does, which is what
// ApplyPipelines reads: bucket lists as maps, the sub-aggregations of a
// bucket typed.
func nodeForm(aggs map[string]meta.AggregationResponse, defs map[string]meta.Aggregations) {
	for name, def := range defs {
		agg, ok := aggs[name]
		if !ok {
			continue
		}
		switch buckets := agg.Buckets.(type) {
		case []interface{}:
			list := make([]map[string]interface{}, 0, len(buckets))
			for _, value := range buckets {
				bucket, ok := value.(map[string]interface{})
				if !ok {
					continue
				}
				subs := make(map[string]meta.AggregationResponse, len(def.Aggregations))
				for sub := range def.Aggregations {
					if generic, ok := bucket[sub].(map[string]interface{}); ok {
						subs[sub] = aggFromGeneric(generic)
					}
				}
				nodeForm(subs, def.Aggregations)
				for sub, typed := range subs {
					bucket[sub] = typed
				}
				list = append(list, bucket)
			}
			agg.Buckets = list
		case map[string]interface{}: // filters
			for key, value := range buckets {
				if generic, ok := value.(map[string]interface{}); ok {
					bucket := aggFromGeneric(generic)
					nodeForm(bucket.Aggregations, def.Aggregations)
					buckets[key] = bucket
				}
			}
		default: // a single bucket: filter, nested, ...
			nodeForm(agg.Aggregations, def.Aggregations)
		}
		aggs[name] = agg
	}
}

// stripPartial removes the internal averaging counters, they are of no
// interest to the client.
func stripPartial(value interface{}) {
	switch node := value.(type) {
	case map[string]interface{}:
		delete(node, PartialAggName)
		for _, child := range node {
			stripPartial(child)
		}
	case []interface{}:
		for _, child := range node {
			stripPartial(child)
		}
	}
}

// DecodeSearchResponse decodes a response received from another node.
//
// meta.AggregationResponse marshals its sub-aggregations inline but has no
// matching unmarshaller, so a plain decode silently drops them, including
// the "_partial" counters. This restores every object key that is not a
// field of AggregationResponse as a sub-aggregation.
func DecodeSearchResponse(data []byte) (*meta.SearchResponse, error) {
	// aggregations are decoded generically only: a sub-aggregation named
	// like a field of AggregationResponse ("max") fails the typed decode
	var wire struct {
		Took         int                    `json:"took"`
		TimedOut     bool                   `json:"timed_out"`
		Shards       meta.Shards            `json:"_shards"`
		Hits         meta.Hits              `json:"hits"`
		Aggregations map[string]interface{} `json:"aggregations"`
		Error        string                 `json:"error,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}
	resp := &meta.SearchResponse{
		Took: wire.Took, TimedOut: wire.TimedOut, Shards: wire.Shards, Hits: wire.Hits, Error: wire.Error,
	}
	if len(wire.Aggregations) == 0 {
		return resp, nil
	}

	resp.Aggregations = make(map[string]meta.AggregationResponse, len(wire.Aggregations))
	for name, value := range wire.Aggregations {
		generic, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		resp.Aggregations[name] = aggFromGeneric(generic)
	}
	return resp, nil
}

// aggFields are the keys AggregationResponse itself owns, every other key
// of an aggregation object is a sub-aggregation.
var aggFields = map[string]bool{
	"value": true, "buckets": true, "interval": true,
	"count": true, "min": true, "max": true, "avg": true, "sum": true,
	"sum_of_squares": true, "variance": true, "std_deviation": true,
	"std_deviation_bounds": true, "values": true, "doc_count": true, "hits": true,
}

// aggObjectFields are the aggFields whose value is an object, any other
// object valued key is a sub-aggregation, even one named "max" or "sum".
var aggObjectFields = map[string]bool{
	"value": true, "buckets": true, "std_deviation_bounds": true, "values": true, "hits": true,
}

// subAgg returns the sub-aggregation stored under key, if it is one.
func subAgg(key string, value interface{}) (map[string]interface{}, bool) {
	sub, ok := value.(map[string]interface{})
	if !ok || aggObjectFields[key] {
		return nil, false
	}
	return sub, true
}

// aggToGeneric renders a typed aggregation as the map form the merge works
// on, sub-aggregations included.
func aggToGeneric(agg meta.AggregationResponse) map[string]interface{} {
	data, err := json.Marshal(agg)
	if err != nil {
		log.Error().Err(err).Msg("cluster: encode aggregation")
		return map[string]interface{}{}
	}
	out := make(map[string]interface{})
	if err := json.Unmarshal(data, &out); err != nil {
		log.Error().Err(err).Msg("cluster: decode aggregation")
		return map[string]interface{}{}
	}
	return out
}

// aggFromGeneric is the inverse of aggToGeneric.
func aggFromGeneric(m map[string]interface{}) meta.AggregationResponse {
	own := make(map[string]interface{}, len(m))
	for key, value := range m {
		if _, ok := subAgg(key, value); !ok && aggFields[key] {
			own[key] = value
		}
	}
	data, err := json.Marshal(own)
	if err != nil {
		log.Error().Err(err).Msg("cluster: encode aggregation")
		return meta.AggregationResponse{}
	}
	var agg meta.AggregationResponse
	if err := json.Unmarshal(data, &agg); err != nil {
		log.Error().Err(err).Msg("cluster: decode aggregation")
		return meta.AggregationResponse{}
	}
	for key, value := range m {
		sub, ok := subAgg(key, value)
		if !ok {
			continue
		}
		if agg.Aggregations == nil {
			agg.Aggregations = make(map[string]meta.AggregationResponse)
		}
		agg.Aggregations[key] = aggFromGeneric(sub)
	}
	return agg
}

func aggDef(query *meta.ZedQuery, name string) meta.Aggregations {
	if query == nil {
		return meta.Aggregations{}
	}
	return query.Aggregations[name]
}

// aggKind classifies an aggregation by the query that asked for it.
func aggKind(def meta.Aggregations) string {
	switch {
	case def.Sum != nil, def.Count != nil, def.ValueCount != nil:
		return "sum"
	case def.Max != nil:
		return "max"
	case def.Min != nil:
		return "min"
	case def.Avg != nil:
		return "avg"
	case def.Stats != nil, def.ExtendedStats != nil:
		return "stats"
	case def.TopHits != nil:
		return "top_hits"
	case def.Terms != nil, def.MultiTerms != nil, def.Histogram != nil,
		def.DateHistogram != nil, def.AutoDateHistogram != nil,
		def.Range != nil, def.DateRange != nil, def.IPRange != nil, def.Filters != nil:
		return "bucket"
	case def.Filter != nil, def.Global != nil, def.Nested != nil,
		def.ReverseNested != nil, def.Missing != nil:
		return "single"
	default:
		return "unknown"
	}
}

// mergeAgg merges one aggregation of two responses. dstDocs and srcDocs are
// the documents each side matched in the enclosing scope: a scope without
// documents renders min and max as 0, that placeholder must not win.
func mergeAgg(dst, src map[string]interface{}, def meta.Aggregations, name string, dstDocs, srcDocs float64) map[string]interface{} {
	kind := aggKind(def)
	if kind == "unknown" {
		// no definition (or one we cannot merge): fall back to the shape
		if _, ok := dst["buckets"]; ok {
			kind = "bucket"
		} else if _, ok := dst["doc_count"]; ok && dst["value"] == nil {
			kind = "single"
		}
	}

	switch kind {
	case "sum":
		dst["value"] = numeric(dst["value"]) + numeric(src["value"])
	case "max", "min":
		// a node without matching docs reports null or 0, it must not win
		if src["value"] == nil || srcDocs == 0 {
			break
		}
		less := numeric(src["value"]) < numeric(dst["value"])
		if dst["value"] == nil || dstDocs == 0 || less == (kind == "min") {
			dst["value"] = numeric(src["value"])
		}
	case "avg":
		mergeAvg(dst, src, name, dstDocs, srcDocs)
	case "stats":
		mergeStats(dst, src, def)
	case "top_hits":
		mergeTopHits(dst, src)
	case "bucket":
		dst["buckets"] = mergeBuckets(dst["buckets"], src["buckets"], def)
		sumDocCount(dst, src)
	case "single":
		dstCount, srcCount := numeric(dst["doc_count"]), numeric(src["doc_count"])
		sumDocCount(dst, src)
		mergeSubAggs(dst, src, def.Aggregations, dstCount, srcCount)
	default:
		log.Warn().Str("aggregation", name).
			Msg("cluster: aggregation cannot be merged across nodes, keeping first node result")
	}
	return dst
}

// mergeAvg weights the per-node averages by the "_partial" counters the
// owning node attached. Without them (averages inside buckets) each side is
// weighted by the documents of its scope, exact when the averaged field is
// present in every document.
func mergeAvg(dst, src map[string]interface{}, name string, dstDocs, srcDocs float64) {
	dstPartial, dstOK := partial(dst)
	srcPartial, srcOK := partial(src)
	if !dstOK || !srcOK {
		log.Debug().Str("aggregation", name).Msg("cluster: avg without _partial counters, weighted by doc count")
		dstPartial = [2]float64{dstDocs, numeric(dst["value"]) * dstDocs}
		srcPartial = [2]float64{srcDocs, numeric(src["value"]) * srcDocs}
	}

	count := dstPartial[0] + srcPartial[0]
	sum := dstPartial[1] + srcPartial[1]
	dst[PartialAggName] = map[string]interface{}{"count": count, "sum": sum}
	if count > 0 {
		dst["value"] = sum / count
	} else {
		dst["value"] = float64(0)
	}
}

// mergeStats adds up the counters of two stats results and derives the
// other values from them, like a node does over its shards. A side without
// values renders min and max as 0, that placeholder must not win.
func mergeStats(dst, src map[string]interface{}, def meta.Aggregations) {
	dstCount, srcCount := numeric(dst["count"]), numeric(src["count"])
	if srcCount > 0 {
		if dstCount == 0 || numeric(src["min"]) < numeric(dst["min"]) {
			dst["min"] = numeric(src["min"])
		}
		if dstCount == 0 || numeric(src["max"]) > numeric(dst["max"]) {
			dst["max"] = numeric(src["max"])
		}
	}
	count := dstCount + srcCount
	sum := numeric(dst["sum"]) + numeric(src["sum"])
	avg := float64(0)
	if count > 0 {
		avg = sum / count
	}
	dst["count"], dst["sum"], dst["avg"] = count, sum, avg
	if def.ExtendedStats == nil {
		return
	}

	squares := numeric(dst["sum_of_squares"]) + numeric(src["sum_of_squares"])
	variance := float64(0)
	if count > 0 {
		variance = max(squares/count-avg*avg, 0) // population variance
	}
	deviation := math.Sqrt(variance)
	sigma := def.ExtendedStats.Sigma
	if sigma == 0 {
		sigma = 2
	}
	dst["sum_of_squares"], dst["variance"], dst["std_deviation"] = squares, variance, deviation
	dst["std_deviation_bounds"] = map[string]interface{}{
		"upper": avg + sigma*deviation,
		"lower": avg - sigma*deviation,
	}
}

// mergeTopHits pools the hits of two top_hits results, finishAgg orders
// them and applies the window once every node is in.
func mergeTopHits(dst, src map[string]interface{}) {
	srcHits, ok := src["hits"].(map[string]interface{})
	if !ok {
		return
	}
	dstHits, ok := dst["hits"].(map[string]interface{})
	if !ok {
		dst["hits"] = srcHits
		return
	}
	dstTotal, _ := dstHits["total"].(map[string]interface{})
	srcTotal, _ := srcHits["total"].(map[string]interface{})
	dstHits["total"] = map[string]interface{}{"value": numeric(dstTotal["value"]) + numeric(srcTotal["value"])}
	dstHits["max_score"] = max(numeric(dstHits["max_score"]), numeric(srcHits["max_score"]))
	dstList, _ := dstHits["hits"].([]interface{})
	srcList, _ := srcHits["hits"].([]interface{})
	dstHits["hits"] = append(dstList, srcList...)
}

// windowTopHits orders the pooled hits like the nodes do, by score or by
// the single sort field, and cuts them to the requested window. The hits
// carry no sort values, the field is read from _source, which the owners
// were asked to return it in: hits without it go last and keep the order
// they arrived in.
func windowTopHits(agg map[string]interface{}, def *meta.AggregationTopHits) {
	body, _ := agg["hits"].(map[string]interface{})
	hits, ok := body["hits"].([]interface{})
	if !ok {
		return
	}
	field, desc, _ := parseSort(def.Sort)
	byScore := field == "" || field == "_score"
	sort.SliceStable(hits, func(i, j int) bool {
		left, _ := hits[i].(map[string]interface{})
		right, _ := hits[j].(map[string]interface{})
		if byScore {
			if l, r := numeric(left["_score"]), numeric(right["_score"]); l != r {
				return l > r
			}
			return stringify(left["_id"]) < stringify(right["_id"])
		}
		leftValue, leftOK := sourceValue(left["_source"], field)
		rightValue, rightOK := sourceValue(right["_source"], field)
		if !leftOK || !rightOK {
			return leftOK && !rightOK
		}
		if cmp := compareSource(leftValue, rightValue); cmp != 0 {
			return (cmp > 0) == desc
		}
		return stringify(left["_id"]) < stringify(right["_id"])
	})
	from := min(max(def.From, 0), len(hits))
	hits = hits[from:min(from+max(topHitsSize(def), 0), len(hits))]
	if _, widened := sortSource(def); widened {
		filterSource(hits, def.Source)
	}
	body["hits"] = hits
}

// compareSource orders two _source values of a sort field like an owner
// orders its doc values: text by bytes, so "10" is before "2", dates by
// time and anything else like compareValues.
func compareSource(left, right interface{}) int {
	leftText, leftOK := left.(string)
	rightText, rightOK := right.(string)
	if !leftOK || !rightOK {
		return compareValues(left, right)
	}
	leftTime, leftErr := time.Parse(time.RFC3339, leftText)
	rightTime, rightErr := time.Parse(time.RFC3339, rightText)
	if leftErr == nil && rightErr == nil {
		return leftTime.Compare(rightTime)
	}
	return strings.Compare(leftText, rightText)
}

// filterSource cuts the _source of hits down to spec, the _source the
// client asked for, like an owner does.
func filterSource(hits []interface{}, spec interface{}) {
	fields, err := source.Request(spec)
	if err != nil {
		return // not a _source an owner answers
	}
	for _, value := range hits {
		hit, _ := value.(map[string]interface{})
		doc, ok := hit["_source"]
		if !ok {
			continue
		}
		data, err := json.Marshal(doc)
		if err != nil {
			log.Error().Err(err).Msg("cluster: encode top_hits _source")
			delete(hit, "_source") // never more than was asked for
			continue
		}
		hit["_source"] = source.Response(fields, data)
	}
}

// sourceValue reads a top level field of a _source, the first value of a list.
func sourceValue(source interface{}, field string) (interface{}, bool) {
	doc, _ := source.(map[string]interface{})
	value := doc[field]
	if list, ok := value.([]interface{}); ok && len(list) > 0 {
		value = list[0]
	}
	switch value.(type) {
	case nil, []interface{}:
		return nil, false
	}
	return value, true
}

// partial reads the {count, sum} sidecar of an average aggregation.
func partial(agg map[string]interface{}) ([2]float64, bool) {
	value, ok := agg[PartialAggName].(map[string]interface{})
	if !ok {
		return [2]float64{}, false
	}
	count, countOK := value["count"]
	sum, sumOK := value["sum"]
	if !countOK || !sumOK {
		return [2]float64{}, false
	}
	return [2]float64{numeric(count), numeric(sum)}, true
}

func sumDocCount(dst, src map[string]interface{}) {
	if _, ok := dst["doc_count"]; !ok {
		if _, ok := src["doc_count"]; !ok {
			return
		}
	}
	dst["doc_count"] = numeric(dst["doc_count"]) + numeric(src["doc_count"])
}

// mergeSubAggs merges every sub-aggregation of a single bucket, dstDocs and
// srcDocs are the doc counts of the bucket on each side.
func mergeSubAggs(dst, src map[string]interface{}, defs map[string]meta.Aggregations, dstDocs, srcDocs float64) {
	for key, value := range src {
		srcSub, ok := subAgg(key, value)
		if !ok {
			continue
		}
		dstSub, ok := dst[key].(map[string]interface{})
		if !ok {
			dst[key] = srcSub
			continue
		}
		dst[key] = mergeAgg(dstSub, srcSub, defs[key], key, dstDocs, srcDocs)
	}
}

// mergeBuckets unions buckets by key. Both the array form and the keyed
// map form Elasticsearch uses for ranges and filters are supported.
func mergeBuckets(dst, src interface{}, def meta.Aggregations) interface{} {
	defs := def.Aggregations
	switch dstBuckets := dst.(type) {
	case []interface{}:
		srcBuckets, ok := src.([]interface{})
		if !ok {
			return dst
		}
		return mergeBucketList(dstBuckets, srcBuckets, def)
	case map[string]interface{}:
		srcBuckets, ok := src.(map[string]interface{})
		if !ok {
			return dst
		}
		for key, value := range srcBuckets {
			srcBucket, ok := value.(map[string]interface{})
			if !ok {
				continue
			}
			dstBucket, ok := dstBuckets[key].(map[string]interface{})
			if !ok {
				dstBuckets[key] = srcBucket
				continue
			}
			mergeBucket(dstBucket, srcBucket, defs)
		}
		return dstBuckets
	default:
		if dst == nil {
			return src
		}
		return dst
	}
}

func mergeBucketList(dst, src []interface{}, def meta.Aggregations) []interface{} {
	defs := def.Aggregations
	out := make([]interface{}, 0, len(dst)+len(src))
	index := make(map[string]map[string]interface{}, len(dst))
	for _, bucket := range dst {
		value, ok := bucket.(map[string]interface{})
		if !ok {
			continue
		}
		index[bucketKey(value)] = value
		out = append(out, value)
	}
	for _, bucket := range src {
		value, ok := bucket.(map[string]interface{})
		if !ok {
			continue
		}
		key := bucketKey(value)
		existing, ok := index[key]
		if !ok {
			index[key] = value
			out = append(out, value)
			continue
		}
		mergeBucket(existing, value, defs)
	}

	switch {
	case def.Terms != nil, def.MultiTerms != nil:
		// ordered and cut by finishAgg once every node is merged
	case def.Range != nil, def.DateRange != nil, def.IPRange != nil, def.Filters != nil:
		// every node returns the requested ranges in request order
	default:
		sortBuckets(out)
	}
	return out
}

// finishAgg orders and cuts what only the complete result can decide, in
// agg and in every aggregation below it: terms buckets to the requested
// size, top_hits to the requested window. Cutting between two nodes would
// drop a term or a hit the next one has.
func finishAgg(agg map[string]interface{}, def meta.Aggregations) {
	if def.TopHits != nil {
		windowTopHits(agg, def.TopHits)
		return
	}
	if def.Terms != nil || def.MultiTerms != nil {
		if buckets, ok := agg["buckets"].([]interface{}); ok {
			sortTerms(buckets, termsOrder(def))
			if size := termsSize(def); size > 0 && len(buckets) > size {
				buckets = buckets[:size]
			}
			agg["buckets"] = buckets
		}
	}
	if len(def.Aggregations) == 0 {
		return
	}
	switch buckets := agg["buckets"].(type) {
	case []interface{}:
		for _, bucket := range buckets {
			finishSubAggs(bucket, def.Aggregations)
		}
	case map[string]interface{}:
		for _, bucket := range buckets {
			finishSubAggs(bucket, def.Aggregations)
		}
	default:
		finishSubAggs(agg, def.Aggregations) // a single bucket: filter, nested, ...
	}
}

func finishSubAggs(bucket interface{}, defs map[string]meta.Aggregations) {
	node, ok := bucket.(map[string]interface{})
	if !ok {
		return
	}
	for name, def := range defs {
		if sub, ok := node[name].(map[string]interface{}); ok {
			finishAgg(sub, def)
		}
	}
}

// termsSize is the bucket limit of a terms aggregation, the default included.
func termsSize(def meta.Aggregations) int {
	size := 0
	if def.Terms != nil {
		size = def.Terms.Size
	} else {
		size = def.MultiTerms.Size
	}
	if size <= 0 {
		size = config.Global.AggregationTermsSize
	}
	return size
}

func termsOrder(def meta.Aggregations) map[string]string {
	if def.Terms != nil {
		return def.Terms.Order
	}
	return def.MultiTerms.Order
}

// sortTerms orders like the terms aggregation: by the requested order, which
// defaults to doc_count descending, then by key ascending on ties.
func sortTerms(buckets []interface{}, order map[string]string) {
	by, asc := "_count", false
	for key, dir := range order {
		by, asc = key, strings.EqualFold(dir, "asc")
	}
	value := func(bucket map[string]interface{}) interface{} {
		switch by {
		case "_count":
			return numeric(bucket["doc_count"])
		case "_key":
			return bucket["key"]
		}
		// sub-aggregation order, "name" or "name.metric"
		name, metric := by, "value"
		if _, ok := bucket[name]; !ok {
			if i := strings.LastIndex(by, "."); i > 0 {
				name, metric = by[:i], by[i+1:]
			}
		}
		sub, _ := bucket[name].(map[string]interface{})
		return numeric(sub[metric])
	}
	sort.SliceStable(buckets, func(i, j int) bool {
		left, _ := buckets[i].(map[string]interface{})
		right, _ := buckets[j].(map[string]interface{})
		if cmp := compareValues(value(left), value(right)); cmp != 0 {
			return (cmp < 0) == asc
		}
		return bucketKey(left) < bucketKey(right)
	})
}

// mergeBucket adds src into dst: doc_count sums, sub-aggregations recurse,
// the key and its formatted forms stay as they are.
func mergeBucket(dst, src map[string]interface{}, defs map[string]meta.Aggregations) {
	dstDocs, srcDocs := numeric(dst["doc_count"]), numeric(src["doc_count"])
	dst["doc_count"] = dstDocs + srcDocs
	for key, value := range src {
		switch key {
		case "key", "key_as_string", "doc_count", "from", "to", "from_as_string", "to_as_string":
			continue
		}
		srcSub, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		dstSub, ok := dst[key].(map[string]interface{})
		if !ok {
			dst[key] = srcSub
			continue
		}
		dst[key] = mergeAgg(dstSub, srcSub, defs[key], key, dstDocs, srcDocs)
	}
}

func bucketKey(bucket map[string]interface{}) string {
	if key, ok := bucket["key"]; ok {
		return stringify(key)
	}
	if key, ok := bucket["key_as_string"]; ok {
		return stringify(key)
	}
	return ""
}

// sortBuckets keeps the merged list deterministic: numeric keys ascending
// (histograms, dates), everything else by doc_count descending like terms.
func sortBuckets(buckets []interface{}) {
	numericKeys := true
	for _, bucket := range buckets {
		value, ok := bucket.(map[string]interface{})
		if !ok {
			return
		}
		if _, err := strconv.ParseFloat(bucketKey(value), 64); err != nil {
			numericKeys = false
			break
		}
	}

	sort.SliceStable(buckets, func(i, j int) bool {
		left, lok := buckets[i].(map[string]interface{})
		right, rok := buckets[j].(map[string]interface{})
		if !lok || !rok {
			return false
		}
		if numericKeys {
			a, _ := strconv.ParseFloat(bucketKey(left), 64)
			b, _ := strconv.ParseFloat(bucketKey(right), 64)
			return a < b
		}
		lcount, rcount := numeric(left["doc_count"]), numeric(right["doc_count"])
		if lcount != rcount {
			return lcount > rcount
		}
		return bucketKey(left) < bucketKey(right)
	})
}

// sortHits orders the merged hits: by the single sort field when the query
// asked for one, by score descending otherwise.
func sortHits(hits []meta.Hit, query *meta.ZedQuery) {
	field, desc, ok := sortField(query)
	if !ok {
		sort.SliceStable(hits, func(i, j int) bool {
			if hits[i].Score != hits[j].Score {
				return hits[i].Score > hits[j].Score
			}
			return hits[i].ID < hits[j].ID
		})
		return
	}

	sort.SliceStable(hits, func(i, j int) bool {
		left, lok := hitValue(hits[i], field)
		right, rok := hitValue(hits[j], field)
		switch {
		case !lok && !rok:
			return hits[i].ID < hits[j].ID
		case !lok:
			return false // missing values sort last
		case !rok:
			return true
		}
		cmp := compareValues(left, right)
		if cmp == 0 {
			return hits[i].ID < hits[j].ID
		}
		if desc {
			return cmp > 0
		}
		return cmp < 0
	})
}

// sortField reads the first sort clause of the query, the merge supports a
// single field. "_score" is reported as not set, score order is the default.
func sortField(query *meta.ZedQuery) (string, bool, bool) {
	if query == nil || query.Sort == nil {
		return "", false, false
	}
	field, desc, ok := parseSort(query.Sort)
	if !ok || field == "" || field == "_score" {
		return "", false, false
	}
	return field, desc, true
}

func parseSort(value interface{}) (string, bool, bool) {
	switch sortValue := value.(type) {
	case string:
		switch {
		case strings.HasPrefix(sortValue, "-"):
			return sortValue[1:], true, true
		case strings.HasPrefix(sortValue, "+"):
			return sortValue[1:], false, true
		default:
			return sortValue, false, true
		}
	case []interface{}:
		if len(sortValue) == 0 {
			return "", false, false
		}
		return parseSort(sortValue[0])
	case []string:
		if len(sortValue) == 0 {
			return "", false, false
		}
		return parseSort(sortValue[0])
	case map[string]interface{}:
		names := make([]string, 0, len(sortValue))
		for name := range sortValue {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return "", false, false
		}
		name := names[0]
		return name, sortOrderDesc(sortValue[name]), true
	default:
		return "", false, false
	}
}

func sortOrderDesc(value interface{}) bool {
	switch order := value.(type) {
	case string:
		return strings.EqualFold(order, "desc")
	case map[string]interface{}:
		if inner, ok := order["order"].(string); ok {
			return strings.EqualFold(inner, "desc")
		}
	}
	return false
}

func hitValue(hit meta.Hit, field string) (interface{}, bool) {
	// _source may be filtered, the hit always carries its timestamp
	if field == meta.TimeFieldName && !hit.Timestamp.IsZero() {
		return hit.Timestamp, true
	}
	if value, ok := hit.Fields[field]; ok {
		if list, isList := value.([]interface{}); isList {
			if len(list) == 0 {
				return nil, false
			}
			return list[0], true
		}
		return value, true
	}
	source, ok := hit.Source.(map[string]interface{})
	if !ok {
		return nil, false
	}
	value, ok := source[field]
	return value, ok
}

// compareValues orders numbers, strings and timestamps, returning -1, 0, 1.
func compareValues(left, right interface{}) int {
	leftNum, leftOK := toFloat(left)
	rightNum, rightOK := toFloat(right)
	if leftOK && rightOK {
		switch {
		case leftNum < rightNum:
			return -1
		case leftNum > rightNum:
			return 1
		default:
			return 0
		}
	}
	return strings.Compare(stringify(left), stringify(right))
}

func windowHits(hits []meta.Hit, query *meta.ZedQuery) []meta.Hit {
	if query == nil {
		return hits
	}
	if query.Size == 0 {
		return []meta.Hit{}
	}
	if query.From > 0 {
		if query.From >= len(hits) {
			return []meta.Hit{}
		}
		hits = hits[query.From:]
	}
	if query.Size > 0 && len(hits) > query.Size {
		hits = hits[:query.Size]
	}
	return hits
}

func numeric(value interface{}) float64 {
	number, _ := toFloat(value)
	return number
}

func toFloat(value interface{}) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case uint64:
		return float64(number), true
	case string:
		if parsed, err := strconv.ParseFloat(number, 64); err == nil {
			return parsed, true
		}
		if parsed, err := time.Parse(time.RFC3339, number); err == nil {
			return float64(parsed.UnixNano()), true
		}
		return 0, false
	case time.Time:
		return float64(number.UnixNano()), true
	default:
		return 0, false
	}
}

func stringify(value interface{}) string {
	switch text := value.(type) {
	case string:
		return text
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(text, 'f', -1, 64)
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(data)
	}
}
