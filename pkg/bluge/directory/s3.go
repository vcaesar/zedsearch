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
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/vcaesar/riot"

	"github.com/vcaesar/zedsearch/pkg/config"
)

const defaultS3Endpoint = "s3.amazonaws.com"

var s3Backend = &backend{newStore: func() (objectStore, error) { return newMinioStore(config.Global.S3) }}

// GetS3Config returns a riot config that stores index items as objects under
// "<indexName>/" in the bucket from config.Global.S3. Works with AWS S3, MinIO
// or any S3-compatible service.
func GetS3Config(indexName string, timeRange ...int64) (riot.Config, error) {
	return getObjectConfig(s3Backend, indexName, timeRange)
}

// RemoveS3Index deletes every object stored for the index.
func RemoveS3Index(indexName string) error {
	return removeObjectIndex(s3Backend, indexName)
}

// minioStore is the objectStore for every S3-compatible backend (S3, MinIO, GCS, OSS).
type minioStore struct {
	client *minio.Client
	bucket string
}

func newMinioStore(cfg config.S3Config) (*minioStore, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3 storage is not configured: ZED_S3_BUCKET is empty")
	}
	// minio silently signs nothing when only one half of a key pair is set
	if (cfg.AccessKeyID == "") != (cfg.SecretAccessKey == "") {
		return nil, fmt.Errorf("s3 storage requires both ZED_S3_ACCESS_KEY_ID and ZED_S3_SECRET_ACCESS_KEY, or neither")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultS3Endpoint
	}
	var creds *credentials.Credentials
	if cfg.AccessKeyID != "" {
		creds = credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, "")
	} else {
		creds = credentials.NewChainCredentials([]credentials.Provider{
			&credentials.EnvAWS{},
			&credentials.FileAWSCredentials{},
			&credentials.IAM{},
		})
	}
	return openMinioStore("s3", cfg, creds, minio.BucketLookupAuto)
}

// openMinioStore builds the client. An "http://" or "https://" scheme on the
// endpoint overrides cfg.UseSSL, since minio rejects a scheme it disagrees with.
func openMinioStore(name string, cfg config.S3Config, creds *credentials.Credentials, lookup minio.BucketLookupType) (*minioStore, error) {
	endpoint, secure := splitEndpoint(cfg.Endpoint, cfg.UseSSL)
	client, err := minio.New(endpoint, &minio.Options{
		Creds:        creds,
		Secure:       secure,
		Region:       cfg.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, fmt.Errorf("%s client: %w", name, err)
	}
	return &minioStore{client: client, bucket: cfg.Bucket}, nil
}

func splitEndpoint(endpoint string, secure bool) (string, bool) {
	if rest, ok := strings.CutPrefix(endpoint, "https://"); ok {
		endpoint, secure = rest, true
	} else if rest, ok := strings.CutPrefix(endpoint, "http://"); ok {
		endpoint, secure = rest, false
	}
	return strings.TrimSuffix(endpoint, "/"), secure
}

func (s *minioStore) List(ctx context.Context, prefix string) ([]objectInfo, error) {
	noOwner := false
	var objs []objectInfo
	for obj := range s.client.ListObjectsIter(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true, FetchOwner: &noOwner}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("list %s: %w", prefix, obj.Err)
		}
		objs = append(objs, objectInfo{Key: obj.Key, Size: obj.Size})
	}
	// a listing cancelled between pages just stops; a partial result would
	// hide the newest items and make riot reuse their ids
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list %s: %w", prefix, err)
	}
	return objs, nil
}

func (s *minioStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	// GetObject is lazy; Stat sends the GET, so a missing key fails here.
	if _, err := obj.Stat(); err != nil {
		return nil, errors.Join(notExist(key, err), obj.Close())
	}
	return obj, nil
}

// notExist wraps S3 NoSuchKey errors with fs.ErrNotExist, which riot uses to
// tell a missing optional item (an HNSW graph not built yet) from a failure.
func notExist(key string, err error) error {
	if isNoSuchKey(err) {
		return fmt.Errorf("object %s: %w: %w", key, fs.ErrNotExist, err)
	}
	return err
}

func isNoSuchKey(err error) bool {
	return minio.ToErrorResponse(err).Code == minio.NoSuchKey
}

func (s *minioStore) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if _, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{}); err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}

// Delete removes key; a key that is already gone (GCS answers 404) is not an error.
func (s *minioStore) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil && !isNoSuchKey(err) {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}
