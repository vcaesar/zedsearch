package index

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
	"github.com/vcaesar/zedsearch/pkg/zutils/json"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestESMapping_GetConverted(t *testing.T) {
	config.Global.EnableTextKeywordMapping = true

	t.Run("create index", func(t *testing.T) {
		index, err := core.NewIndex("TestEsMapping.index_1", "disk", 2)
		assert.NoError(t, err)
		assert.NotNil(t, index)

		err = core.StoreIndex(index)
		assert.NoError(t, err)
	})

	t.Run("set mapping", func(t *testing.T) {
		type args struct {
			code    int
			data    map[string]interface{}
			rawData string
			target  string
			result  string
		}
		tests := []struct {
			name    string
			args    args
			wantErr bool
		}{
			{
				name: "normal",
				args: args{
					code: http.StatusOK,
					data: map[string]interface{}{
						"properties": map[string]interface{}{
							"Athlete": map[string]interface{}{
								"type":          "text",
								"index":         true,
								"store":         false,
								"sortable":      false,
								"aggregatable":  false,
								"highlightable": false,
							},
							"City": map[string]interface{}{
								"type":          "keyword",
								"index":         true,
								"store":         false,
								"sortable":      false,
								"aggregatable":  true,
								"highlightable": false,
							},
							"Gender": map[string]interface{}{
								"type":          "bool",
								"index":         true,
								"store":         false,
								"sortable":      false,
								"aggregatable":  true,
								"highlightable": false,
							},
							"time": map[string]interface{}{
								"type":          "date",
								"index":         true,
								"store":         false,
								"sortable":      false,
								"aggregatable":  true,
								"highlightable": false,
							},
							"obj": map[string]interface{}{
								"type":          "text",
								"index":         true,
								"store":         false,
								"sortable":      false,
								"aggregatable":  true,
								"highlightable": false,
							},
							"obj.sub_field": map[string]interface{}{
								"type":          "text",
								"index":         true,
								"store":         false,
								"sortable":      false,
								"aggregatable":  true,
								"highlightable": false,
							},
						},
					},
					target: "TestEsMapping.index_1",
					result: `{"message":"ok"}`,
				},
				wantErr: false,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				c, w := utils.NewContext()
				if tt.args.data != nil {
					utils.SetRequestData(c, tt.args.data)
				}
				if tt.args.rawData != "" {
					utils.SetRequestData(c, tt.args.rawData)
				}
				utils.SetRequestParams(c, map[string]string{"target": tt.args.target})
				SetMapping(c)
				assert.Equal(t, tt.args.code, w.Code)
				assert.Equal(t, tt.args.result, w.Body.String())
			})
		}
	})

	t.Run("get mapping", func(t *testing.T) {
		type args struct {
			code   int
			target string
			result string
		}
		tests := []struct {
			name    string
			args    args
			wantErr bool
		}{
			{
				name: "normal",
				args: args{
					code:   http.StatusOK,
					target: "TestEsMapping.index_1",
					result: `{"@timestamp":{"type":"date"}`,
				},
				wantErr: false,
			},
			{
				name: "empty",
				args: args{
					code:   http.StatusBadRequest,
					target: "",
					result: `{"error":"index  does not exists"}`,
				},
				wantErr: false,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				c, w := utils.NewContext()
				utils.SetRequestParams(c, map[string]string{"target": tt.args.target})
				GetESMapping(c)
				assert.Equal(t, tt.args.code, w.Code)
				assert.Contains(t, w.Body.String(), tt.args.result)
			})
		}
	})

	t.Run("delete index", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			_ = core.DeleteIndex(fmt.Sprintf("TestEsMapping.index_%d", i))
		}
	})

	config.Global.EnableTextKeywordMapping = false
}

// Both mapping endpoints return the options a vector field was created with.
func TestMapping_VectorOptions(t *testing.T) {
	name := "TestMapping_VectorOptions.index_1"
	c, w := utils.NewContext()
	utils.SetRequestData(c, map[string]interface{}{"properties": map[string]interface{}{"emb": map[string]interface{}{
		"type": "dense_vector", "dims": 3, "similarity": "l2_norm",
		"index_options": map[string]interface{}{"type": "hnsw", "m": 8, "ef_construction": 64},
	}}})
	utils.SetRequestParams(c, map[string]string{"target": name})
	SetMapping(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	t.Cleanup(func() { assert.NoError(t, core.DeleteIndex(name)) })

	tests := []struct {
		name string
		get  func(zhttp.Context)
	}{
		{name: "es", get: GetESMapping},
		{name: "native", get: GetMapping},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestParams(c, map[string]string{"target": name})
			tt.get(c)
			require.Equal(t, http.StatusOK, w.Code)
			var resp map[string]struct {
				Mappings struct {
					Properties map[string]struct {
						Type         string                   `json:"type"`
						Dims         int                      `json:"dims"`
						Similarity   string                   `json:"similarity"`
						IndexOptions *meta.VectorIndexOptions `json:"index_options"`
					} `json:"properties"`
				} `json:"mappings"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			emb := resp[name].Mappings.Properties["emb"]
			assert.Equal(t, "vector", emb.Type)
			assert.Equal(t, 3, emb.Dims)
			assert.Equal(t, "l2", emb.Similarity)
			assert.Equal(t, &meta.VectorIndexOptions{Type: "hnsw", M: 8, EfConstruction: 64}, emb.IndexOptions)
		})
	}
}
