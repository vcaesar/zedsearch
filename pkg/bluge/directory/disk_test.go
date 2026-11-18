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

package directory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"
)

// A batch on a disk index is not held back by a persister nap.
func TestGetDiskConfigNoPersisterNap(t *testing.T) {
	w, err := riot.OpenWriter(GetDiskConfig(t.TempDir(), "TestGetDiskConfigNoPersisterNap.index_1"))
	require.NoError(t, err)
	defer func() { assert.NoError(t, w.Close()) }()

	for _, id := range []string{"1", "2"} {
		batch := riot.NewBatch()
		batch.Insert(riot.NewDocument(id).AddField(riot.NewTextField("body", "quick brown fox")))
		require.NoError(t, w.Batch(batch))
	}
	assert.Zero(t, w.Status().TotPersisterNapPauseCompleted)

	r, err := w.Reader()
	require.NoError(t, err)
	defer func() { assert.NoError(t, r.Close()) }()
	n, err := r.Count()
	require.NoError(t, err)
	assert.Equal(t, uint64(2), n)
}
