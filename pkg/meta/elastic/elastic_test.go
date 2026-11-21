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

package elastic

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestNewESInfo(t *testing.T) {
	old := config.Global.Plugin.ES.Version
	t.Cleanup(func() { config.Global.Plugin.ES.Version = old })
	serverVersion := strings.TrimLeft(meta.Version, "v")

	tests := []struct {
		name       string
		userAgent  string
		configured string
		want       string
	}{
		{name: "no user agent", want: serverVersion},
		{name: "other client", userAgent: "curl/7.81.0", want: serverVersion},
		{
			name:      "elastic client reports its own version",
			userAgent: "elastic-transport-ruby/8.0.1 (RUBY_VERSION: 3.1.2; linux x86_64; Faraday v1.10.0)",
			want:      "8.0.1",
		},
		{name: "elastic client without version", userAgent: "Elasticsearch-Py", want: serverVersion},
		{name: "configured version wins", userAgent: "elastic-transport-ruby/8.0.1", configured: "7.17.0-snapshot", want: "7.17.0-SNAPSHOT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.Global.Plugin.ES.Version = tt.configured
			c, _ := utils.NewContext()
			c.Request.Header.Set("User-Agent", tt.userAgent)

			info := NewESInfo(c)
			assert.Equal(t, tt.want, info.Version.Number)
			assert.Equal(t, "zed", info.Name)
			assert.Equal(t, meta.CommitHash, info.Version.BuildHash)
			assert.Equal(t, meta.BuildDate, info.Version.BuildDate)
			assert.Equal(t, "You Know, for Search", info.Tagline)
		})
	}
}

func TestNewESLicense(t *testing.T) {
	c, _ := utils.NewContext()
	assert.Equal(t, &ESLicense{Status: "active"}, NewESLicense(c))
}

func TestNewESXPack(t *testing.T) {
	c, _ := utils.NewContext()
	data, err := json.Marshal(NewESXPack(c))
	require.NoError(t, err)
	assert.JSONEq(t, `{"build":{},"features":{},"license":{"status":"active"}}`, string(data))
}

func TestDataStream(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		c, w := utils.NewContext()
		utils.SetRequestParams(c, map[string]string{"target": "logs-app"})
		GetDataStream(c)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"data_streams":[{"name":"logs-app","timestamp_field":{"name":"@timestamp"}}]}`, w.Body.String())
	})

	t.Run("put", func(t *testing.T) {
		c, w := utils.NewContext()
		utils.SetRequestParams(c, map[string]string{"target": "logs-app"})
		PutDataStream(c)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"name":"logs-app","message":"ok"}`, w.Body.String())
	})
}

func TestMappings(t *testing.T) {
	m := NewMappings()
	assert.Equal(t, 0, m.Len())
	data, err := json.Marshal(m)
	require.NoError(t, err)
	assert.JSONEq(t, `{"properties":{}}`, string(data))

	m.SetProperty("title", Property{Type: "text", Analyzer: "standard"})
	m.SetProperty("tag", Property{Type: "keyword"})
	assert.Equal(t, 2, m.Len())

	prop, ok := m.GetProperty("title")
	assert.True(t, ok)
	assert.Equal(t, "standard", prop.Analyzer)
	_, ok = m.GetProperty("missing")
	assert.False(t, ok)

	list := m.ListProperty()
	assert.Equal(t, map[string]Property{"title": {Type: "text", Analyzer: "standard"}, "tag": {Type: "keyword"}}, list)
	list["extra"] = Property{Type: "date"}
	assert.Equal(t, 2, m.Len(), "the list is a copy")

	m.Source = &SourceMapping{Enabled: false}
	m.All = &SourceMapping{Enabled: true}
	data, err = json.Marshal(m)
	require.NoError(t, err)
	assert.JSONEq(t, `{"properties":{"title":{"type":"text","analyzer":"standard"},"tag":{"type":"keyword"}},
		"_source":{"enabled":false},"_all":{"enabled":true}}`, string(data))
}

// The encoder cannot fail on these types, so it is swapped to check that
// MarshalJSON hands its error back for each part of the mappings.
func TestMappings_MarshalJSON_EncodeError(t *testing.T) {
	errEncode := errors.New("encode failed")
	marshal := json.Marshal
	t.Cleanup(func() { json.Marshal = marshal })

	m := NewMappings()
	m.Source = &SourceMapping{Enabled: false}
	m.All = &SourceMapping{Enabled: true}

	tests := []struct {
		name string
		fail func(v interface{}) bool
	}{
		{name: "properties", fail: func(v interface{}) bool { _, ok := v.(map[string]Property); return ok }},
		{name: "_source", fail: func(v interface{}) bool { return v == interface{}(m.Source) }},
		{name: "_all", fail: func(v interface{}) bool { return v == interface{}(m.All) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			json.Marshal = func(v interface{}) ([]byte, error) {
				if tt.fail(v) {
					return nil, errEncode
				}
				return marshal(v)
			}
			data, err := m.MarshalJSON()
			assert.ErrorIs(t, err, errEncode)
			assert.Nil(t, data)
		})
	}
}

func TestNewProperty(t *testing.T) {
	prop := NewProperty("keyword")
	assert.Equal(t, "keyword", prop.Type)
	assert.NotNil(t, prop.Properties)
	assert.NotNil(t, prop.Fields)

	data, err := json.Marshal(prop)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"keyword"}`, string(data))
}
