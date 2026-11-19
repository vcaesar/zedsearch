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

package token

import (
	"testing"

	"github.com/go-ego/gse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot/analysis"
)

func TestStopTokenFilter(t *testing.T) {
	seg := new(gse.Segmenter)
	seg.SkipLog = true
	require.NoError(t, seg.LoadStopStr("的\n"))

	f := NewStopTokenFilter(seg, []string{"了"})
	assert.True(t, seg.IsStop("了"), "extra stop words are added to the segmenter")

	input := analysis.TokenStream{
		{Term: []byte("我"), PositionIncr: 1},
		{Term: []byte("的"), PositionIncr: 1},
		{Term: []byte("了"), PositionIncr: 1},
		{Term: []byte("书"), PositionIncr: 1},
	}
	got := f.Filter(input)
	require.Len(t, got, 2)
	assert.Equal(t, "我", string(got[0].Term))
	assert.Equal(t, 1, got[0].PositionIncr)
	assert.Equal(t, "书", string(got[1].Term))
	assert.Equal(t, 3, got[1].PositionIncr, "skipped positions carry over")
}
