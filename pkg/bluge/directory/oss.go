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
please refer to [AGPL-3.0](./LICENSE-EE)) and LICENSE-EE for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.
*/

package directory

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/vcaesar/riot"

	"github.com/vcaesar/zedsearch/pkg/config"
)

var ossBackend = &backend{newStore: func() (objectStore, error) { return newOSSStore(config.Global.OSS) }}

// ossHostRe matches the public, internal and "s3." OSS endpoints and captures the region id.
var ossHostRe = regexp.MustCompile(`^(?:s3\.)?oss-([a-z0-9-]+?)(?:-internal)?\.aliyuncs\.com$`)

// GetOSSConfig returns a riot config that stores index items in the Alibaba
// Cloud OSS bucket from config.Global.OSS.
func GetOSSConfig(indexName string, timeRange ...int64) (riot.Config, error) {
	return getObjectConfig(ossBackend, indexName, timeRange)
}

// RemoveOSSIndex deletes every object stored for the index.
func RemoveOSSIndex(indexName string) error {
	return removeObjectIndex(ossBackend, indexName)
}

// newOSSStore talks to OSS through its S3-compatible API.
func newOSSStore(cfg config.OSSConfig) (*minioStore, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("oss storage is not configured: ZED_OSS_BUCKET is empty")
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("oss storage is not configured: ZED_OSS_ENDPOINT is empty")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("oss storage requires ZED_OSS_ACCESS_KEY_ID and ZED_OSS_SECRET_ACCESS_KEY")
	}
	// // over plain HTTP minio signs uploads as aws-chunked, which OSS rejects
	// if strings.HasPrefix(cfg.Endpoint, "http://") {
	// 	return nil, fmt.Errorf("oss storage requires an https endpoint, got [%s]", cfg.Endpoint)
	// }
	region, err := ossRegion(cfg)
	if err != nil {
		return nil, err
	}
	creds := credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, "")
	// OSS serves virtual-hosted requests only; minio would pick path style for custom domains
	return openMinioStore("oss", config.S3Config{
		Endpoint: cfg.Endpoint,
		Bucket:   cfg.Bucket,
		Region:   region,
		UseSSL:   true,
	}, creds, minio.BucketLookupDNS)
}

// ossRegion returns the region id requests are signed with, e.g. "cn-hangzhou".
// OSS rejects a signature made for "oss-cn-hangzhou", and minio cannot guess the
// region of an OSS host, so it is taken from the endpoint when not configured.
func ossRegion(cfg config.OSSConfig) (string, error) {
	if region := strings.TrimPrefix(cfg.Region, "oss-"); region != "" {
		return region, nil
	}
	host, _ := splitEndpoint(cfg.Endpoint, true)
	if m := ossHostRe.FindStringSubmatch(host); m != nil {
		return m[1], nil
	}
	return "", fmt.Errorf("oss storage requires ZED_OSS_REGION for endpoint [%s]", cfg.Endpoint)
}
