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

package cluster

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	errFault = errors.New("injected store fault")
	// errLose makes Create and CompareAndSwap report a lost race instead of
	// failing.
	errLose = errors.New("lose the race")
)

// faultRule decides whether op on key fails, nil lets it through.
type faultRule func(op, key string) error

// failOn fails every op in ops on keys under prefix with err.
func failOn(err error, prefix string, ops ...string) faultRule {
	return func(op, key string) error {
		for _, want := range ops {
			if op == want && strings.HasPrefix(key, prefix) {
				return err
			}
		}
		return nil
	}
}

// faultStore wraps a Store and fails the calls its rule selects.
type faultStore struct {
	Store
	mu   sync.Mutex
	rule faultRule
}

func newFaultStore(rule faultRule) *faultStore {
	return &faultStore{Store: NewMemoryStore(), rule: rule}
}

func (s *faultStore) setRule(rule faultRule) {
	s.mu.Lock()
	s.rule = rule
	s.mu.Unlock()
}

func (s *faultStore) fault(op, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rule == nil {
		return nil
	}
	return s.rule(op, key)
}

func (s *faultStore) Grant(ctx context.Context, ttl time.Duration) (LeaseID, error) {
	if err := s.fault("Grant", ""); err != nil {
		return NoLease, err
	}
	return s.Store.Grant(ctx, ttl)
}

func (s *faultStore) KeepAlive(ctx context.Context, id LeaseID) error {
	if err := s.fault("KeepAlive", ""); err != nil {
		return err
	}
	return s.Store.KeepAlive(ctx, id)
}

func (s *faultStore) Revoke(ctx context.Context, id LeaseID) error {
	if err := s.fault("Revoke", ""); err != nil {
		return err
	}
	return s.Store.Revoke(ctx, id)
}

func (s *faultStore) Put(ctx context.Context, key string, value []byte, lease LeaseID) (int64, error) {
	if err := s.fault("Put", key); err != nil {
		return 0, err
	}
	return s.Store.Put(ctx, key, value, lease)
}

func (s *faultStore) Get(ctx context.Context, key string) (KV, error) {
	if err := s.fault("Get", key); err != nil {
		return KV{}, err
	}
	return s.Store.Get(ctx, key)
}

func (s *faultStore) List(ctx context.Context, prefix string) ([]KV, error) {
	if err := s.fault("List", prefix); err != nil {
		return nil, err
	}
	return s.Store.List(ctx, prefix)
}

func (s *faultStore) Create(ctx context.Context, key string, value []byte, lease LeaseID) (int64, bool, error) {
	if err := s.fault("Create", key); err != nil {
		if errors.Is(err, errLose) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return s.Store.Create(ctx, key, value, lease)
}

func (s *faultStore) CompareAndSwap(ctx context.Context, key string, value []byte, rev int64, lease LeaseID, guards ...Guard) (int64, bool, error) {
	if err := s.fault("CompareAndSwap", key); err != nil {
		if errors.Is(err, errLose) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return s.Store.CompareAndSwap(ctx, key, value, rev, lease, guards...)
}

func (s *faultStore) Watch(ctx context.Context, prefix string) (<-chan Event, error) {
	if err := s.fault("Watch", prefix); err != nil {
		return nil, err
	}
	return s.Store.Watch(ctx, prefix)
}

func (s *faultStore) Close() error {
	if err := s.fault("Close", ""); err != nil {
		return err
	}
	return s.Store.Close()
}
