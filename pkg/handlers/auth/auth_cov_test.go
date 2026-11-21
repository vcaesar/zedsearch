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

package auth

import (
	"errors"
	"net/http"
	"testing"
	_ "unsafe" // go:linkname

	"github.com/stretchr/testify/assert"

	"github.com/vcaesar/zedsearch/pkg/metadata/storage"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
	"github.com/vcaesar/zedsearch/test/utils"
)

// metadataDB is the unexported storage of pkg/metadata, swapped to inject failures.
//
//go:linkname metadataDB github.com/vcaesar/zedsearch/pkg/metadata.db
var metadataDB storage.Storager

var errMock = errors.New("mock storage failure")

// failStorage wraps the metadata storage, failing the calls whose error is set.
type failStorage struct {
	storage.Storager
	list, del, set error
}

func (f *failStorage) Set(key string, value []byte) error {
	if f.set != nil {
		return f.set
	}
	return f.Storager.Set(key, value)
}

func (f *failStorage) List(prefix string, offset, limit int) ([][]byte, error) {
	if f.list != nil {
		return nil, f.list
	}
	return f.Storager.List(prefix, offset, limit)
}

func (f *failStorage) Delete(key string) error {
	if f.del != nil {
		return f.del
	}
	return f.Storager.Delete(key)
}

// useFailStorage swaps the metadata storage for f for one test.
func useFailStorage(t *testing.T, f *failStorage) {
	t.Helper()
	prev := metadataDB
	f.Storager = prev
	metadataDB = f
	t.Cleanup(func() { metadataDB = prev })
}

func TestAuth_StorageErrors(t *testing.T) {
	tests := []struct {
		name    string
		storage *failStorage
		h       func(zhttp.Context)
		params  map[string]string
	}{
		{"list roles", &failStorage{list: errMock}, ListRole, nil},
		{"delete role", &failStorage{del: errMock}, DeleteRole, map[string]string{"id": "TestAuth_StorageErrors.role_1"}},
		{"list users", &failStorage{list: errMock}, ListUser, nil},
		{"delete user", &failStorage{del: errMock}, DeleteUser, map[string]string{"id": "TestAuth_StorageErrors.user_1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useFailStorage(t, tt.storage)
			c, w := utils.NewContext()
			utils.SetRequestParams(c, tt.params)
			tt.h(c)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
			assert.JSONEq(t, `{"error":"mock storage failure"}`, w.Body.String())
		})
	}
}
