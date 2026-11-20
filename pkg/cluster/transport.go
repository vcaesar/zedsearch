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
	"net/http"
	"sync"

	"github.com/vcaesar/zedsearch/pkg/tracing"
)

// peerIdleConns is how many idle connections are kept per node. The default
// transport keeps 2, so a scatter or a burst of replicated writes would
// dial a new connection for almost every call.
const peerIdleConns = 64

var peerTransport = sync.OnceValue(func() http.RoundTripper {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.MaxIdleConns = 0 // bounded per node instead
	base.MaxIdleConnsPerHost = peerIdleConns
	return tracing.Transport(base)
})

// Transport is the traced round tripper shared by every node to node call:
// forwarded requests, scatter-gather searches and replication.
func Transport() http.RoundTripper {
	return peerTransport()
}

// NoRedirect is the CheckRedirect of node to node clients. The internal
// endpoints never redirect, and following one would hand the caller's
// Authorization or the cluster secret to a host nobody selected, so the
// redirect is returned as the response instead.
func NoRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}
