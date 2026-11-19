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
 */

package meta

import (
	"testing"

	"github.com/vcaesar/ice/vec"

	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

func BenchmarkSourceVectors(b *testing.B) {
	emb := make([]interface{}, 128)
	f32 := make([]float32, 128)
	for i := range emb {
		emb[i], f32[i] = float64(i)/128, float32(i)/128
	}
	src := map[string]interface{}{"title": "the quick brown fox", "price": 1.5, "emb": emb,
		"meta": map[string]interface{}{"emb": emb, "tag": "x"}}
	fields := []string{"emb", "meta.emb"}
	raw, err := vec.Encode(f32)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("exclude", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			stripped, marker, err := ExcludeSourceVectors(src, fields)
			if err != nil {
				b.Fatal(err)
			}
			_, _ = stripped, marker
		}
	})
	stripped, marker, err := ExcludeSourceVectors(src, fields)
	if err != nil {
		b.Fatal(err)
	}
	source, err := json.Marshal(stripped)
	if err != nil {
		b.Fatal(err)
	}
	v := NewSourceVectors(fields)
	b.Run("restore", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			v.Reset()
			v.Visit("emb", raw)
			v.Visit("meta.emb", raw)
			v.Visit(SourceVectorsField, marker)
			if _, err := v.Restore(source); err != nil {
				b.Fatal(err)
			}
		}
	})
}
