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

func TestMulti(t *testing.T) {
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
			name: "multi",
			args: args{
				code: http.StatusOK,
				data: `{"Year": 1896, "City": "Athens", "Sport": "Aquatics", "Discipline": "Swimming", "Athlete": "HAJOS, Alfred", "Country": "HUN", "Gender": "Men", "Event": "100M Freestyle", "Medal": "Gold", "Season": "summer"}
				{"Year": 1896, "City": "Athens", "Sport": "Aquatics", "Discipline": "Swimming", "Athlete": "HERSCHMANN, Otto", "Country": "AUT", "Gender": "Men", "Event": "100M Freestyle", "Medal": "Silver", "Season": "summer"}
				{"Year": 1896, "City": "Athens", "Sport": "Aquatics", "Discipline": "Swimming", "Athlete": "HERSCHMANN, Otto", "Country": "AUT", "Gender": "Men", "Event": "100M Freestyle", "Medal": "Silver", "Season": "summer"}
				{"Year": 1896, "City": "Athens"}`,
				params: map[string]string{"target": "document.multi"},
				result: "",
			},
		},
		{
			name: "error",
			args: args{
				code: http.StatusBadRequest,
				data: `{"Year": 1896, "City": "Athens", "Sport": "Aquatics", "Discipline": "Swimming", "Athlete": "HAJOS, Alfred", "Country": "HUN", "Gender": "Men", "Event": "100M Freestyle", "Medal": "Gold", "Season": "summer"}
				{"Year": 1896, "City": "Athens"}`,
				params: map[string]string{"target": ""},
				result: "",
			},
		},
		{
			name: "error",
			args: args{
				code: http.StatusOK,
				data: `{"Year": 1896, "City": "Athens", "Sport": "Aquatics", "Discipline": "Swimming", "Athlete": "HAJOS, Alfred", "Country": "HUN", "Gender": "Men", "Event": "100M Freestyle", "Medal": "Gold", "Season": "summer"}
				{ "Year": 1896, "_id": "1"x } } `,
				params: map[string]string{"target": "document.multi"},
				result: "",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestData(c, tt.args.data)
			utils.SetRequestParams(c, tt.args.params)
			Multi(c)
			assert.Equal(t, tt.args.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.args.result)
		})
	}
}

func TestMultiErrors(t *testing.T) {
	indexName := "TestMultiErrors.index_1"
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
			name:   "skips invalid lines and keeps ids",
			data:   "not json\n" + `{"_id":"1","name":"a"}` + "\n" + `{"name":"b"}`,
			target: indexName,
			code:   http.StatusOK,
			result: `{"message":"multiple data inserted","record_count":2}`,
		},
		{
			name:   "invalid index name",
			data:   `{"name":"a"}`,
			target: "_TestMultiErrors.index_2",
			code:   http.StatusInternalServerError,
			result: `{"error":"index name cannot start with _"}`,
		},
		{
			name:   "invalid document",
			data:   `{"@timestamp":"bad"}`,
			target: indexName,
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
			Multi(c)
			assert.Equal(t, tt.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.result)
		})
	}
	waitDocCount(t, indexName, 2)
}
