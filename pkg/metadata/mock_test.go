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

package metadata

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

var errMock = errors.New("mock storage failure")

// mockStorage is an in-memory storage.Storager; err, when set, fails every call.
type mockStorage struct {
	data map[string][]byte
	err  error
}

func (m *mockStorage) List(prefix string, _, _ int) ([][]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	out := make([][]byte, 0, len(keys))
	for _, k := range keys {
		out = append(out, m.data[k])
	}
	return out, nil
}

func (m *mockStorage) Get(key string) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	v, ok := m.data[key]
	if !ok {
		return nil, ErrorKeyNotExists
	}
	return v, nil
}

func (m *mockStorage) Set(key string, value []byte) error {
	if m.err != nil {
		return m.err
	}
	m.data[key] = value
	return nil
}

func (m *mockStorage) Delete(key string) error {
	if m.err != nil {
		return m.err
	}
	delete(m.data, key)
	return nil
}

func (m *mockStorage) Close() error {
	return m.err
}

// useMock swaps the package storage for an in-memory mock for one test.
func useMock(t *testing.T) *mockStorage {
	t.Helper()
	m := &mockStorage{data: map[string][]byte{}}
	prev := db
	db = m
	t.Cleanup(func() { db = prev })
	return m
}
