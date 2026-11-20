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
 */

package query

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vcaesar/riot"
	"github.com/vcaesar/riot/search"
)

var errFake = errors.New("fake searcher error")

// fakeQuery wraps a query and injects errors into its searcher.
type fakeQuery struct {
	inner       riot.Query
	searcherErr error // returned by Searcher
	advanceErr  error // returned by Advance
	closeErr    error // returned by Close
	validateErr error // returned by Validate
}

func (q *fakeQuery) Searcher(r search.Reader, options search.SearcherOptions) (search.Searcher, error) {
	if q.searcherErr != nil {
		return nil, q.searcherErr
	}
	s, err := q.inner.Searcher(r, options)
	if err != nil {
		return nil, err
	}
	return &fakeSearcher{Searcher: s, query: q}, nil
}

func (q *fakeQuery) Validate() error { return q.validateErr }

type fakeSearcher struct {
	search.Searcher
	query *fakeQuery
}

func (s *fakeSearcher) Advance(ctx *search.Context, number uint64) (*search.DocumentMatch, error) {
	if s.query.advanceErr != nil {
		return nil, s.query.advanceErr
	}
	return s.Searcher.Advance(ctx, number)
}

func (s *fakeSearcher) Close() error {
	if err := s.Searcher.Close(); err != nil {
		return err
	}
	return s.query.closeErr
}

// advanceQuery drives the wrapped searcher with Advance only, so a search
// reaches the Advance method of the searcher under test.
type advanceQuery struct {
	inner riot.Query
}

func (q *advanceQuery) Searcher(r search.Reader, options search.SearcherOptions) (search.Searcher, error) {
	s, err := q.inner.Searcher(r, options)
	if err != nil {
		return nil, err
	}
	return &advanceSearcher{Searcher: s}, nil
}

type advanceSearcher struct {
	search.Searcher
	next uint64
}

func (s *advanceSearcher) Next(ctx *search.Context) (*search.DocumentMatch, error) {
	dm, err := s.Advance(ctx, s.next)
	if dm != nil {
		s.next = dm.Number + 1
	}
	return dm, err
}

// readerHook hands the search.Reader of a running search to fn, so tests can
// build and drive searchers directly.
type readerHook struct {
	fn func(r search.Reader, options search.SearcherOptions)
}

func (q readerHook) Searcher(r search.Reader, options search.SearcherOptions) (search.Searcher, error) {
	q.fn(r, options)
	return riot.NewMatchNoneQuery().Searcher(r, options)
}

// withSearchReader runs fn with the search.Reader of reader.
func withSearchReader(t *testing.T, reader *riot.Reader, fn func(r search.Reader, options search.SearcherOptions)) {
	t.Helper()
	called := false
	dmi, err := reader.Search(context.Background(), riot.NewTopNSearch(1, readerHook{fn: func(r search.Reader, options search.SearcherOptions) {
		called = true
		fn(r, options)
	}}))
	require.NoError(t, err)
	_, err = dmi.Next()
	require.NoError(t, err)
	require.True(t, called)
}
