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
*
Part of this work fall under the AtomAI Enterprise Edition (EE)
and are licensed under the Business Source License 1.1,
if you are the Cloud companies or your companies with revenue greater than $1M,
please refer to [AGPL-3.0](./LICENSE-EE)) and LICENSE-EE for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.
*/

package logs

import (
	"fmt"
	"time"
)

// CheckWindow rejects events older than upto or later than future relative to
// now, like OpenObserve's ZO_INGEST_ALLOWED_UPTO/ZO_INGEST_ALLOWED_IN_FUTURE.
// A zero bound is disabled.
func CheckWindow(event, now time.Time, upto, future time.Duration) error {
	if upto > 0 && event.Before(now.Add(-upto)) {
		return fmt.Errorf("event time [%s] is older than the allowed ingestion window [%s]", event.Format(time.RFC3339Nano), upto)
	}
	if future > 0 && event.After(now.Add(future)) {
		return fmt.Errorf("event time [%s] is too far in the future, allowed [%s]", event.Format(time.RFC3339Nano), future)
	}
	return nil
}
