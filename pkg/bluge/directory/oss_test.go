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

package directory

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vcaesar/zedsearch/pkg/config"
)

func TestNewOSSStore(t *testing.T) {
	_, err := newOSSStore(config.OSSConfig{})
	assert.Error(t, err, "bucket required")
	_, err = newOSSStore(config.OSSConfig{Bucket: "zed"})
	assert.Error(t, err, "endpoint required")
	_, err = newOSSStore(config.OSSConfig{Endpoint: "oss-cn-hangzhou.aliyuncs.com", Bucket: "zed"})
	assert.Error(t, err, "keys required")

	s, err := newOSSStore(config.OSSConfig{Endpoint: "oss-cn-hangzhou.aliyuncs.com", Bucket: "zed", Region: "cn-hangzhou", AccessKeyID: "a", SecretAccessKey: "b"})
	assert.NoError(t, err)
	assert.Equal(t, "zed", s.bucket)
	assert.Equal(t, "https://oss-cn-hangzhou.aliyuncs.com", s.client.EndpointURL().String())
}

// TestGetOSSConfigUnconfigured uses a local, unconfigured backend so it never
// touches a real bucket configured through the environment.
func TestGetOSSConfigUnconfigured(t *testing.T) {
	b := &backend{newStore: func() (objectStore, error) { return newOSSStore(config.OSSConfig{}) }}
	_, err := getObjectConfig(b, "idx", nil)
	assert.Error(t, err)
	assert.Error(t, removeObjectIndex(b, "idx"))
}

func TestOSSRegion(t *testing.T) {
	for _, tt := range []struct {
		name     string
		endpoint string
		region   string
		want     string
		wantErr  bool
	}{
		{"configured", "oss-cn-hangzhou.aliyuncs.com", "cn-hangzhou", "cn-hangzhou", false},
		{"oss prefix stripped", "oss-cn-hangzhou.aliyuncs.com", "oss-cn-hangzhou", "cn-hangzhou", false},
		{"from public endpoint", "oss-cn-hangzhou.aliyuncs.com", "", "cn-hangzhou", false},
		{"from s3 endpoint", "https://s3.oss-ap-southeast-1.aliyuncs.com", "", "ap-southeast-1", false},
		{"from internal endpoint", "oss-cn-beijing-internal.aliyuncs.com", "", "cn-beijing", false},
		{"custom domain needs region", "files.example.com", "", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ossRegion(config.OSSConfig{Endpoint: tt.endpoint, Region: tt.region})
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	// _, err := newOSSStore(config.OSSConfig{Endpoint: "http://oss-cn-hangzhou.aliyuncs.com", Bucket: "zed", AccessKeyID: "a", SecretAccessKey: "b"})
	// assert.Error(t, err, "plain http would sign uploads as aws-chunked")
	_, err := newOSSStore(config.OSSConfig{Endpoint: "files.example.com", Bucket: "zed", AccessKeyID: "a", SecretAccessKey: "b"})
	assert.Error(t, err, "region cannot be derived from a custom domain")
	s, err := newOSSStore(config.OSSConfig{Endpoint: "files.example.com", Bucket: "zed", Region: "cn-hangzhou", AccessKeyID: "a", SecretAccessKey: "b"})
	assert.NoError(t, err)
	assert.Equal(t, "https://files.example.com", s.client.EndpointURL().String())
}
