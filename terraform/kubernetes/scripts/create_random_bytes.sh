#!/bin/bash

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
