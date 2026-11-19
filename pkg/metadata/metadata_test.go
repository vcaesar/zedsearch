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

package metadata

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/meta"
)

// badTime cannot be marshaled: JSON times are limited to years 0-9999.
var badTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)

func TestAlias(t *testing.T) {
	m := useMock(t)

	got, err := Alias.Get()
	require.NoError(t, err)
	assert.Empty(t, got)

	want := map[string][]string{"TestAlias.alias_1": {"TestAlias.index_1"}}
	require.NoError(t, Alias.Set(want))
	got, err = Alias.Get()
	require.NoError(t, err)
	assert.Equal(t, want, got)

	m.data["/aliases/alias"] = []byte("{")
	_, err = Alias.Get()
	assert.Error(t, err)

	m.err = errMock
	_, err = Alias.Get()
	assert.ErrorIs(t, err, errMock)
	assert.ErrorIs(t, Alias.Set(want), errMock)
}

func TestIndex(t *testing.T) {
	m := useMock(t)
	name := "TestIndex.index_1"

	require.NoError(t, Index.Set(name, []byte(`{"name":"`+name+`","shard_num":1}`)))
	got, err := Index.Get(name)
	require.NoError(t, err)
	assert.Equal(t, name, got.Name)

	list, err := Index.List(0, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, name, list[0].Name)

	t.Run("invalid metadata", func(t *testing.T) {
		m.data["/index/broken"] = []byte(`{"name":1}`)
		defer delete(m.data, "/index/broken")
		_, err := Index.List(0, 0)
		assert.Error(t, err)
	})

	require.NoError(t, Index.Delete(name))
	_, err = Index.Get(name)
	assert.ErrorIs(t, err, ErrorKeyNotExists)

	m.err = errMock
	_, err = Index.List(0, 0)
	assert.ErrorIs(t, err, errMock)
}

func TestKV(t *testing.T) {
	m := useMock(t)
	key := "TestKV.key_1"

	require.NoError(t, KV.Set(key, []byte("value")))
	got, err := KV.Get(key)
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), got)

	list, err := KV.List(0, 0)
	require.NoError(t, err)
	assert.Equal(t, [][]byte{[]byte("value")}, list)

	require.NoError(t, KV.Delete(key))
	_, err = KV.Get(key)
	assert.ErrorIs(t, err, ErrorKeyNotExists)

	m.err = errMock
	_, err = KV.List(0, 0)
	assert.ErrorIs(t, err, errMock)
}

func TestPermission(t *testing.T) {
	p := &permission{ps: []string{}}
	assert.Empty(t, p.List())
	p.Add("TestPermission.read")
	p.Add("TestPermission.write")
	p.Add("TestPermission.read")
	assert.Equal(t, []string{"TestPermission.read", "TestPermission.write"}, p.List())
}

func TestRole(t *testing.T) {
	m := useMock(t)
	id := "TestRole.role_1"

	require.NoError(t, Role.Set(id, meta.Role{ID: id, Permission: []string{"read"}}))
	got, err := Role.Get(id)
	require.NoError(t, err)
	assert.Equal(t, []string{"read"}, got.Permission)

	list, err := Role.List(0, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, id, list[0].ID)

	require.NoError(t, Role.Delete(id))
	_, err = Role.Get(id)
	assert.ErrorIs(t, err, ErrorKeyNotExists)

	assert.Error(t, Role.Set("bad", meta.Role{CreatedAt: badTime}))

	m.data["/role/broken"] = []byte("{")
	_, err = Role.List(0, 0)
	assert.Error(t, err)

	m.err = errMock
	_, err = Role.List(0, 0)
	assert.ErrorIs(t, err, errMock)
}

func TestTemplate(t *testing.T) {
	m := useMock(t)
	id := "TestTemplate.template_1"
	tpl := meta.Template{Name: id, IndexTemplate: &meta.IndexTemplate{IndexPatterns: []string{"TestTemplate.*"}}}

	require.NoError(t, Template.Set(id, tpl))
	got, err := Template.Get(id)
	require.NoError(t, err)
	assert.Equal(t, tpl.IndexTemplate.IndexPatterns, got.IndexTemplate.IndexPatterns)

	list, err := Template.List(0, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, id, list[0].Name)

	require.NoError(t, Template.Delete(id))
	_, err = Template.Get(id)
	assert.ErrorIs(t, err, ErrorKeyNotExists)

	assert.Error(t, Template.Set("bad", meta.Template{IndexTemplate: &meta.IndexTemplate{CreatedAt: badTime}}))

	m.data["/template/broken"] = []byte("{")
	_, err = Template.List(0, 0)
	assert.Error(t, err)

	m.err = errMock
	_, err = Template.List(0, 0)
	assert.ErrorIs(t, err, errMock)
}

func TestUser(t *testing.T) {
	m := useMock(t)
	id := "TestUser.user_1"

	require.NoError(t, User.Set(id, meta.User{ID: id, Role: "admin"}))
	got, err := User.Get(id)
	require.NoError(t, err)
	assert.Equal(t, "admin", got.Role)

	list, err := User.List(0, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, id, list[0].ID)

	require.NoError(t, User.Delete(id))
	_, err = User.Get(id)
	assert.ErrorIs(t, err, ErrorKeyNotExists)

	assert.Error(t, User.Set("bad", meta.User{CreatedAt: badTime}))

	m.data["/user/broken"] = []byte("{")
	_, err = User.List(0, 0)
	assert.Error(t, err)

	m.err = errMock
	_, err = User.List(0, 0)
	assert.ErrorIs(t, err, errMock)
}

func TestILMErrors(t *testing.T) {
	m := useMock(t)

	assert.Error(t, ILM.Set("TestILMErrors.bad", ILMPolicyRecord{Policy: ILMPolicy{Phases: map[string]ILMPhase{
		"delete": {Actions: map[string]interface{}{"bad": make(chan int)}},
	}}}))

	m.data["/ilm/broken"] = []byte("{")
	_, err := ILM.List(0, 0)
	assert.Error(t, err)

	m.err = errMock
	_, err = ILM.List(0, 0)
	assert.ErrorIs(t, err, errMock)
	_, err = ILM.Get("TestILMErrors.missing")
	assert.ErrorIs(t, err, errMock)
}

func TestClose(t *testing.T) {
	m := useMock(t)
	assert.NoError(t, Close())
	m.err = errMock
	assert.ErrorIs(t, Close(), errMock)
}
