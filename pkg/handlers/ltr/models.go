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
*
Part of this work fall under the AtomAI Enterprise Edition (EE)
and are licensed under the Business Source License 1.1,
if you are the Cloud companies or your companies with revenue greater than $1M,
please refer to [AGPL-3.0](./LICENSE-AGPL)) and [LICENSE-EE](./LICENSE-EE) for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.

The other parts of this work are licensed under the [Apache license](./LICENSE).
*/

// Package ltr serves the learning to rank models, as the Elasticsearch
// trained models API does.
package ltr

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/vcaesar/zedsearch/pkg/errors"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	"github.com/vcaesar/zedsearch/pkg/uquery/ltr"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
	"github.com/vcaesar/zedsearch/pkg/zutils"
)

const (
	errorTypeNotFound = "resource_not_found_exception"
	errorTypeExists   = "resource_already_exists_exception"
)

// lock serializes the check and store of a model.
var lock sync.Mutex

// ListResponse lists the models.
type ListResponse struct {
	Count  int              `json:"count"`
	Models []*meta.LTRModel `json:"trained_model_configs"`
}

// PutModel stores a learning to rank model; models are immutable
//
// @Id PutTrainedModel
// @Summary Create learning to rank model for compatible ES
// @security BasicAuth
// @Tags    LearningToRank
// @Accept  json
// @Produce json
// @Param   model_id path string        true "Model id"
// @Param   model    body meta.LTRModel true "inference_config.learning_to_rank and definition"
// @Success 200 {object} meta.LTRModel
// @Failure 400 {object} map[string]interface{}
// @Router /es/_ml/trained_models/{model_id} [put]
func PutModel(c zhttp.Context) {
	id := c.Param("model_id")
	if id == "_all" || id == "*" {
		renderError(c, http.StatusBadRequest, errors.ErrorTypeIllegalArgumentException, fmt.Sprintf("trained model id [%s] is reserved", id))
		return
	}
	model := new(meta.LTRModel)
	if err := zutils.BindJSON(c, model); err != nil {
		renderError(c, http.StatusBadRequest, errors.ErrorTypeParsingException, err.Error())
		return
	}
	if _, err := ltr.Compile(model); err != nil {
		renderError(c, http.StatusBadRequest, errors.ErrorTypeParsingException, err.Error())
		return
	}
	model.ModelID = id
	model.CreateTime = time.Now().UnixMilli()

	lock.Lock()
	defer lock.Unlock()
	_, err := metadata.LTRModels.Get(id)
	switch {
	case err == nil:
		renderError(c, http.StatusBadRequest, errorTypeExists, fmt.Sprintf("trained machine learning model [%s] already exists", id))
		return
	case !errors.Is(err, errors.ErrKeyNotFound):
		renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
		return
	}
	if err := metadata.LTRModels.Set(id, *model); err != nil {
		renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
		return
	}
	zutils.RenderJSON(c, http.StatusOK, model)
}

// GetModels lists the learning to rank models sorted by id, or the one of model_id
//
// @Id GetTrainedModels
// @Summary Get learning to rank models for compatible ES
// @security BasicAuth
// @Tags    LearningToRank
// @Produce json
// @Param   model_id path  string false "Model id"
// @Param   from     query int    false "Models to skip, sorted by id" default(0)
// @Param   size     query int    false "Models to return" default(100)
// @Success 200 {object} ltr.ListResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /es/_ml/trained_models [get]
// @Router /es/_ml/trained_models/{model_id} [get]
func GetModels(c zhttp.Context) {
	id := c.Param("model_id")
	if id == "" || id == "_all" || id == "*" {
		from, err := strconv.Atoi(c.DefaultQuery("from", "0"))
		if err != nil || from < 0 {
			renderError(c, http.StatusBadRequest, errors.ErrorTypeIllegalArgumentException, "[from] must be a non-negative integer")
			return
		}
		size, err := strconv.Atoi(c.DefaultQuery("size", "100"))
		if err != nil || size < 0 {
			renderError(c, http.StatusBadRequest, errors.ErrorTypeIllegalArgumentException, "[size] must be a non-negative integer")
			return
		}
		models, err := metadata.LTRModels.List(0, 0)
		if err != nil {
			renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
			return
		}
		sort.Slice(models, func(i, j int) bool { return models[i].ModelID < models[j].ModelID })
		page := models[min(from, len(models)):]
		page = page[:min(size, len(page))]
		zutils.RenderJSON(c, http.StatusOK, ListResponse{Count: len(models), Models: page})
		return
	}
	model, ok := load(c, id)
	if !ok {
		return
	}
	zutils.RenderJSON(c, http.StatusOK, ListResponse{Count: 1, Models: []*meta.LTRModel{model}})
}

// DeleteModel deletes a learning to rank model
//
// @Id DeleteTrainedModel
// @Summary Delete learning to rank model for compatible ES
// @security BasicAuth
// @Tags    LearningToRank
// @Produce json
// @Param   model_id path string true "Model id"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /es/_ml/trained_models/{model_id} [delete]
func DeleteModel(c zhttp.Context) {
	lock.Lock()
	defer lock.Unlock()
	id := c.Param("model_id")
	if _, ok := load(c, id); !ok {
		return
	}
	if err := metadata.LTRModels.Delete(id); err != nil {
		renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
		return
	}
	zutils.RenderJSON(c, http.StatusOK, zhttp.H{"acknowledged": true})
}

func load(c zhttp.Context, id string) (*meta.LTRModel, bool) {
	model, err := metadata.LTRModels.Get(id)
	if err == nil {
		return model, true
	}
	if errors.Is(err, errors.ErrKeyNotFound) {
		renderError(c, http.StatusNotFound, errorTypeNotFound, fmt.Sprintf("Could not find trained model [%s]", id))
	} else {
		renderError(c, http.StatusInternalServerError, errors.ErrorTypeRuntimeException, err.Error())
	}
	return nil, false
}

func renderError(c zhttp.Context, code int, typ, reason string) {
	zutils.RenderJSON(c, code, zhttp.H{
		"error":  zhttp.H{"type": typ, "reason": reason},
		"status": code,
	})
}
