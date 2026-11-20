#!/bin/sh

cd "$(dirname "$0")/.." || exit 1

if ! command -v swag &> /dev/null
then
    echo "swag could not be found. Make sure it is installed and available on the environment"
    echo "please refer: https://github.com/swaggo/swag"
    exit
fi

echo "Running swag command"
swag init -g cmd/zedsearch/main.go --exclude test0,frontend,web || exit 1

# swag can't emit a file header: prepend the license to the generated Go file
{
    cat <<'EOF'
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

EOF
    cat docs/docs.go
} > docs/docs.go.tmp && mv docs/docs.go.tmp docs/docs.go
