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

package utils

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewContext(t *testing.T) {
	c, w := NewContext()
	assert.NotNil(t, c)
	assert.NotNil(t, w)
	assert.NotNil(t, c.Request.URL)
	assert.NotNil(t, c.Request.Header)

	c.JSON(http.StatusCreated, map[string]string{"ok": "yes"})
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.JSONEq(t, `{"ok":"yes"}`, w.Body.String())
}

func TestSetRequestData(t *testing.T) {
	tests := []struct {
		name    string
		data    interface{}
		want    string
		hasBody bool
	}{
		{name: "string", data: `{"a":1}`, want: `{"a":1}`, hasBody: true},
		{name: "map", data: map[string]interface{}{"a": "b"}, want: `{"a":"b"}`, hasBody: true},
		{name: "marshal error", data: map[string]interface{}{"a": make(chan int)}},
		{name: "unsupported type", data: 123},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := NewContext()
			SetRequestData(c, tt.data)
			assert.Equal(t, "application/json;charset=utf-8", c.Request.Header.Get("Content-Type"))
			if !tt.hasBody {
				assert.Nil(t, c.Request.Body)
				return
			}
			body, err := io.ReadAll(c.Request.Body)
			assert.NoError(t, err)
			assert.JSONEq(t, tt.want, string(body))
		})
	}
}

func TestSetRequestURL(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		params map[string]string
		query  string
	}{
		{name: "no params", path: "/api/index", query: ""},
		{name: "with params", path: "/api/search", params: map[string]string{"q": "x", "size": "10"}, query: "q=x&size=10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := NewContext()
			SetRequestURL(c, tt.path, tt.params)
			assert.Equal(t, tt.path, c.Path())
			assert.Equal(t, tt.query, c.Request.URL.RawQuery)
			for k, v := range tt.params {
				assert.Equal(t, v, c.Query(k))
			}
		})
	}
}

func TestSetRequestParams(t *testing.T) {
	c, _ := NewContext()
	SetRequestParams(c, map[string]string{"target": "idx", "id": "1"})
	assert.Equal(t, "idx", c.Param("target"))
	assert.Equal(t, "1", c.Param("id"))
	assert.Equal(t, "", c.Param("missing"))

	SetRequestParams(c, map[string]string{"id": "2"})
	assert.Equal(t, "2", c.Param("id"))
}
