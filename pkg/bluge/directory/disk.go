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
*
Part of this work fall under the AtomAI Enterprise Edition (EE)
and are licensed under the Business Source License 1.1,
if you are the Cloud companies or your companies with revenue greater than $1M,
please refer to [AGPL-3.0](./LICENSE-EE)) and LICENSE-EE for details,
or production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.
*/

package directory

import (
	"path"

	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/index"
)

// GetDiskConfig returns a bluge config that will store index data in local disk
// rootPath: the root path of data
// indexName: the name of the index to use.
func GetDiskConfig(rootPath string, indexName string, timeRange ...int64) riot.Config {
	config := index.DefaultConfig(path.Join(rootPath, indexName))
	// no persister nap: a batch's segments are already on disk when it is
	// introduced, so a nap only delays every Batch by its length
	config = config.WithPersisterNapTimeMSec(0)
	if len(timeRange) == 2 {
		if timeRange[0] <= timeRange[1] {
			config = config.WithTimeRange(timeRange[0], timeRange[1])
		}
	}
	return riot.DefaultConfigWithIndexConfig(config)
}
