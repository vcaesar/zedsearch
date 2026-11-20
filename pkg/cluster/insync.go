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
please refer to [AGPL-3.0](./LICENSE-AGPL)) and [LICENSE-EE](./LICENSE-EE) for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.

The other parts of this work are licensed under the [Apache license](./LICENSE).
*/

package cluster

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// The in-sync set is OpenSearch's in-sync allocation ids: per index, the
// replicas that hold every write the owner acknowledged. Only the owner
// changes it - it adds a replica once it caught up and removes one that
// failed a write before acknowledging that write. The leader reads it to
// promote only replicas that cannot have lost an acknowledged write.
//
// The set and the assignments are separate keys, so each is written in a
// transaction with the revision of the other: the owner changes the set only
// while its partition is still at its epoch, the leader moves a partition
// only while the set is still the one it read. Whichever commits first makes
// the other fail, a replica dropped from the set is never promoted.
//
// An index with an entry, even an empty one, is replicated; a partition
// whose owner died with no live in-sync replica is left on the dead owner
// (unavailable) instead of being handed to a node with stale data.

// maxInSyncRetries bounds the compare-and-swap loop on the in-sync key.
const maxInSyncRetries = 16

// InSyncKey is the key holding the in-sync map.
func (a *Assigner) InSyncKey() string {
	return a.prefix + "insync"
}

// ListInSync returns index -> in-sync replicas and the revision it was read at.
func (a *Assigner) ListInSync(ctx context.Context) (map[string][]string, int64, error) {
	kv, err := a.store.Get(ctx, a.InSyncKey())
	if errors.Is(err, errors.ErrKeyNotFound) {
		return map[string][]string{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	insync := make(map[string][]string)
	if err := json.Unmarshal(kv.Value, &insync); err != nil {
		return nil, 0, err
	}
	return insync, kv.Revision, nil
}

// UpdateInSync sets the in-sync replicas of index, nil removes the entry.
// epoch is the ownership epoch of the caller, 0 while the partition is
// unassigned; the update fails once the partition moved.
func (a *Assigner) UpdateInSync(ctx context.Context, index string, nodes []string, epoch int64) error {
	if nodes != nil {
		nodes = append([]string{}, nodes...)
		sort.Strings(nodes)
	}
	owned := Guard{Key: a.key(PartitionOfN(index, a.partitions)), Revision: epoch}
	a.insyncMu.Lock()
	defer a.insyncMu.Unlock()
	for range maxInSyncRetries {
		insync, rev, err := a.ListInSync(ctx)
		if err != nil {
			return err
		}
		current, ok := insync[index]
		if nodes == nil && !ok || nodes != nil && ok && slices.Equal(current, nodes) {
			// nothing to write, but a caller whose partition moved still fails
			return a.checkOwned(ctx, owned, index)
		}
		if nodes == nil {
			delete(insync, index)
		} else {
			insync[index] = nodes
		}
		value, err := json.Marshal(insync)
		if err != nil {
			return err
		}
		_, swapped, err := a.store.CompareAndSwap(ctx, a.InSyncKey(), value, rev, NoLease, owned)
		if err != nil {
			return err
		}
		if swapped {
			return nil
		}
		// lost to another in-sync update, or the partition is not ours any more
		if err := a.checkOwned(ctx, owned, index); err != nil {
			return err
		}
	}
	return fmt.Errorf("cluster: in-sync set of %q kept changing", index)
}

// checkOwned fails with an *EpochError when the assignment of the partition
// of index is no longer at the revision of owned: the caller's writes were
// not fenced by the new owner and must not be acknowledged.
func (a *Assigner) checkOwned(ctx context.Context, owned Guard, index string) error {
	assignment, err := a.store.Get(ctx, owned.Key)
	if err != nil && !errors.Is(err, errors.ErrKeyNotFound) {
		return err
	}
	if assignment.Revision == owned.Revision {
		return nil
	}
	var value assignmentValue
	if len(assignment.Value) > 0 {
		if err := json.Unmarshal(assignment.Value, &value); err != nil {
			return err
		}
	}
	moved := &EpochError{Index: index, Owner: value.Owner, Have: owned.Revision, Want: assignment.Revision}
	return fmt.Errorf("cluster: in-sync set of %q not changed, its partition moved: %w", index, moved)
}

// EligibleOwners maps every partition holding a replicated index to the
// nodes that are in sync for all of its indexes. Partitions without a
// replicated index are absent: any node may own them.
func EligibleOwners(insync map[string][]string, partitions int) map[int][]string {
	out := make(map[int][]string)
	for index, nodes := range insync {
		partition := PartitionOfN(index, partitions)
		current, seen := out[partition]
		if !seen {
			out[partition] = append([]string{}, nodes...)
			continue
		}
		out[partition] = slices.DeleteFunc(current, func(node string) bool {
			return !slices.Contains(nodes, node)
		})
	}
	for partition := range out {
		sort.Strings(out[partition])
	}
	return out
}

// ConstrainChanges drops rebalance moves that would hand a replicated
// partition to a node that is not in sync. A partition whose owner is dead
// goes to a live in-sync node instead, or nowhere when there is none.
func ConstrainChanges(changes, owners map[int]string, nodes []string, eligible map[int][]string) map[int]string {
	if len(changes) == 0 || len(eligible) == 0 {
		return changes
	}
	alive := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		alive[node] = true
	}
	out := make(map[int]string, len(changes))
	for partition, node := range changes {
		allowed, constrained := eligible[partition]
		if !constrained || slices.Contains(allowed, node) {
			out[partition] = node
			continue
		}
		if alive[owners[partition]] {
			continue // the owner stays until a replica caught up
		}
		for _, candidate := range allowed {
			if alive[candidate] {
				out[partition] = candidate
				break
			}
		}
	}
	return out
}
