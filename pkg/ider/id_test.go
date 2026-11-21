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
 */

package ider

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sony/sonyflake/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/zutils/base62"
)

// Package variables are initialized before init runs, so init sees an
// out-of-range node id and its fatal is recorded here instead of exiting.
var (
	initNodeID = config.Global.NodeID
	initLogger = log.Logger
	initFatals = 0
	initOutput = func() *bytes.Buffer {
		out := new(bytes.Buffer)
		log.Logger = zerolog.New(out)
		zerolog.FatalExitFunc = func() { initFatals++ }
		config.Global.NodeID = maxNodeID + 1
		return out
	}()
)

func TestMain(m *testing.M) {
	config.Global.NodeID, log.Logger, zerolog.FatalExitFunc = initNodeID, initLogger, nil
	var err error
	if local, err = NewNode(initNodeID); err != nil {
		log.Fatal().Err(err).Msg("restore the local node")
	}
	os.Exit(m.Run())
}

func TestInit_NodeIDOutOfRange(t *testing.T) {
	assert.Equal(t, 1, initFatals)
	assert.Contains(t, initOutput.String(), "id generater init[local] err node id 1024 out of range [0, 1023]")
}

func TestGenerate(t *testing.T) {
	got := Generate()
	assert.NotEmpty(t, got)
}

func TestNewNode(t *testing.T) {
	for _, i := range []int{0, 1, maxNodeID} {
		node, err := NewNode(i)
		assert.NoError(t, err)
		assert.NotNil(t, node)
		id := node.Generate()
		assert.NotEmpty(t, id)
	}
	for _, i := range []int{-1, maxNodeID + 1, 2048} {
		node, err := NewNode(i)
		assert.Error(t, err, "node id %d must be rejected", i)
		assert.Nil(t, node)
	}
}

func TestNewNode_StartTimeAhead(t *testing.T) {
	old := epoch
	t.Cleanup(func() { epoch = old })
	epoch = time.Now().Add(time.Hour)

	node, err := NewNode(1)
	assert.ErrorIs(t, err, sonyflake.ErrStartTimeAhead)
	assert.Nil(t, node)
}

func TestGenerate_OverTimeLimit(t *testing.T) {
	oldEpoch, oldExit, oldLogger := epoch, zerolog.FatalExitFunc, log.Logger
	t.Cleanup(func() { epoch, zerolog.FatalExitFunc, log.Logger = oldEpoch, oldExit, oldLogger })

	// 41 bits of milliseconds overflow after about 69 years
	epoch = time.Now().AddDate(-70, 0, 0)
	var out bytes.Buffer
	log.Logger = zerolog.New(&out)
	exited := false
	zerolog.FatalExitFunc = func() { exited = true }

	node, err := NewNode(1)
	require.NoError(t, err)
	assert.Equal(t, base62.Encode(0), node.Generate())
	assert.True(t, exited, "an exhausted id clock is fatal")
	assert.Contains(t, out.String(), "id generate failed")
	assert.Contains(t, out.String(), sonyflake.ErrOverTimeLimit.Error())
}

func TestGenerate_Unique(t *testing.T) {
	node, err := NewNode(1)
	assert.NoError(t, err)
	const n = 10000
	seen := make(map[string]struct{}, n)
	prev := int64(0)
	for range n {
		raw, err := node.node.NextID()
		assert.NoError(t, err)
		assert.Greater(t, raw, prev, "ids must be strictly increasing")
		assert.Equal(t, int64(1), raw&1023, "low 10 bits carry the node id")
		prev = raw
		id := base62.Encode(raw)
		_, dup := seen[id]
		assert.False(t, dup, "duplicate id %s", id)
		seen[id] = struct{}{}
	}
}
