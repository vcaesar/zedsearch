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

package meta

// LTRModel is a learning to rank model, as the Elasticsearch trained models
// API stores it: inference_config.learning_to_rank lists the feature
// extractors, definition holds the model (trained_model tree or ensemble,
// linear or xgboost).
type LTRModel struct {
	ModelID         string                 `json:"model_id"`
	Description     string                 `json:"description,omitempty"`
	CreateTime      int64                  `json:"create_time,omitempty"`
	InferenceConfig map[string]interface{} `json:"inference_config"`
	Definition      map[string]interface{} `json:"definition"`
}
