#!/bin/bash

# Copyright 2018- The Pixie Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# SPDX-License-Identifier: Apache-2.0

set -e

usage() {
    if [ "$#" -ne 2 ]; then
        echo "Illegal number of parameters"
        echo "Usage: $0 <characters_to_keep> <number_of_characters>"
        exit 1
    fi
}

usage "$@"

chars_to_keep=$1
num_chars=$2

bytes=$(< /dev/urandom tr -dc "${chars_to_keep}" | fold -w "$num_chars" | head -n 1)

jq -n --arg output "$bytes" '{"output":$output}'
