/* Copyright 2022 Zinc Labs Inc. and Contributors
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

package document

import (
	"errors"
	"io"
	"net/http"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"

	"github.com/vcaesar/zedsearch/test/utils"
)

func TestBulk(t *testing.T) {
	type args struct {
		code   int
		data   string
		params map[string]string
		result string
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "bulk",
			args: args{
				code: http.StatusOK,
				data: `{ "index" : { "_index" : "document.bulk" } } 
				{"Year": 1896, "City": "Athens", "Sport": "Aquatics", "Discipline": "Swimming", "Athlete": "HAJOS, Alfred", "Country": "HUN", "Gender": "Men", "Event": "100M Freestyle", "Medal": "Gold", "Season": "summer"}
				{ "create" : { "_index" : "document.bulk" } } 
				{"Year": 1896, "City": "Athens", "Sport": "Aquatics", "Discipline": "Swimming", "Athlete": "HERSCHMANN, Otto", "Country": "AUT", "Gender": "Men", "Event": "100M Freestyle", "Medal": "Silver", "Season": "summer"}
				{ "update" : { "_index" : "document.bulk", "_id": "1" } } 
				{"Year": 1896, "City": "Athens", "Sport": "Aquatics", "Discipline": "Swimming", "Athlete": "HERSCHMANN, Otto", "Country": "AUT", "Gender": "Men", "Event": "100M Freestyle", "Medal": "Silver", "Season": "summer"}
				{ "delete" : { "_index" : "document.bulk", "_id": "1" } } `,
				params: map[string]string{"target": "document.bulk"},
				result: "",
			},
		},
		{
			name: "error",
			args: args{
				code: http.StatusOK,
				data: `{ "index" : { "_index" : "document.bulk" } } 
				{"Year": 1896, "City": "Athens", "Sport": "Aquatics", "Discipline": "Swimming", "Athlete": "HAJOS, Alfred", "Country": "HUN", "Gender": "Men", "Event": "100M Freestyle", "Medal": "Gold", "Season": "summer"}
				{ "delete" : { "_index" : "document.bulk", "_id": "1"x } } `,
				params: map[string]string{"target": "document.bulk"},
				result: "",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.args.data)
			utils.SetRequestParams(c, tt.args.params)
			Bulk(c)
			assert.Equal(t, tt.args.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.args.result)
		})
	}
}

func TestESBulk(t *testing.T) {
	type args struct {
		code   int
		data   string
		params map[string]string
		result string
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "bulk",
			args: args{
				code: http.StatusOK,
				data: `{ "index" : { "_index" : "document.esbulk" } } 
				{"Year": 1896, "City": "Athens", "Sport": "Aquatics", "Discipline": "Swimming", "Athlete": "HAJOS, Alfred", "Country": "HUN", "Gender": "Men", "Event": "100M Freestyle", "Medal": "Gold", "Season": "summer"}
				{ "create" : { "_index" : "document.esbulk" } } 
				{"Year": 1896, "City": "Athens", "Sport": "Aquatics", "Discipline": "Swimming", "Athlete": "HERSCHMANN, Otto", "Country": "AUT", "Gender": "Men", "Event": "100M Freestyle", "Medal": "Silver", "Season": "summer"}
				{ "update" : { "_index" : "document.esbulk", "_id": "1" } } 
				{"Year": 1896, "City": "Athens", "Sport": "Aquatics", "Discipline": "Swimming", "Athlete": "HERSCHMANN, Otto", "Country": "AUT", "Gender": "Men", "Event": "100M Freestyle", "Medal": "Silver", "Season": "summer"}
				{ "delete" : { "_index" : "document.esbulk", "_id": "1" } } `,
				params: map[string]string{"target": "document.esbulk"},
				result: "",
			},
		},
		{
			name: "error",
			args: args{
				code: http.StatusOK,
				data: `{ "index" : { "_index" : "document.esbulk" } } 
				{"Year": 1896, "City": "Athens", "Sport": "Aquatics", "Discipline": "Swimming", "Athlete": "HAJOS, Alfred", "Country": "HUN", "Gender": "Men", "Event": "100M Freestyle", "Medal": "Gold", "Season": "summer"}
				{ "delete" : { "_index" : "document.esbulk", "_id": "1"x } } `,
				params: map[string]string{"target": "document.esbulk"},
				result: "",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.args.data)
			utils.SetRequestParams(c, tt.args.params)
			ESBulk(c)
			assert.Equal(t, tt.args.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.args.result)
		})
	}
}

func TestBulkErrors(t *testing.T) {
	indexName := "TestBulkErrors.index_1"
	cleanupIndex(t, indexName)
	tests := []struct {
		name   string
		data   string
		body   io.Reader
		target string
		code   int
		result string
	}{
		{
			name:   "metadata without index uses target",
			data:   `{"index":{}}` + "\n" + `{"name":"a"}`,
			target: indexName,
			code:   http.StatusOK,
			result: `{"message":"bulk data inserted","record_count":1}`,
		},
		{
			name:   "unknown action is ignored",
			data:   `{"unknown":{}}`,
			target: indexName,
			code:   http.StatusOK,
			result: `{"message":"bulk data inserted","record_count":0}`,
		},
		{
			name:   "empty index without target",
			data:   `{"index":{"_index":""}}` + "\n" + `{"name":"a"}`,
			code:   http.StatusInternalServerError,
			result: `{"error":"bulk index data format error"}`,
		},
		{
			name:   "delete with empty index without target",
			data:   `{"delete":{"_index":"","_id":"1"}}`,
			code:   http.StatusInternalServerError,
			result: `{"error":"bulk index data format error"}`,
		},
		{
			name:   "delete with invalid index name",
			data:   `{"delete":{"_index":"_TestBulkErrors.index_2","_id":"1"}}`,
			code:   http.StatusInternalServerError,
			result: `{"error":"index name cannot start with _"}`,
		},
		{
			name:   "invalid document",
			data:   `{"index":{"_index":"` + indexName + `"}}` + "\n" + `{"@timestamp":"bad"}`,
			code:   http.StatusInternalServerError,
			result: "field [@timestamp] value [bad] parse err",
		},
		{
			name:   "body read error",
			body:   iotest.ErrReader(errors.New("read failed")),
			target: indexName,
			code:   http.StatusInternalServerError,
			result: `{"error":"read failed"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.data)
			if tt.body != nil {
				c.Request.Body = io.NopCloser(tt.body)
			}
			utils.SetRequestParams(c, map[string]string{"target": tt.target})
			Bulk(c)
			assert.Equal(t, tt.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.result)
		})
	}
}

func TestDoesExistInThisRequest(t *testing.T) {
	tests := []struct {
		name  string
		slice []string
		val   string
		want  int
	}{
		{name: "found", slice: []string{"a", "b", "c"}, val: "b", want: 1},
		{name: "not found", slice: []string{"a", "b"}, val: "c", want: -1},
		{name: "empty slice", slice: nil, val: "a", want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DoesExistInThisRequest(tt.slice, tt.val))
		})
	}
}
