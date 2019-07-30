#!/usr/bin/env bash

set -e

if [[ -z "${AWS_ACCESS_KEY_ID}" ]]; then
    echo "AWS_ACCESS_KEY_ID is undefined"
    exit 1
elif [[ -z "${AWS_SECRET_ACCESS_KEY}" ]]; then
    echo "AWS_SECRET_ACCESS_KEY is undefined"
    exit 1
fi

sleep 10

docker run \
    --rm -t $(tty &>/dev/null && echo "-i") \
    --user $(id -u):ciagent \
    -e "AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID}" \
    -e "AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY}" \
    -v "$(pwd):/plugin/kibana-extra/go-langserver" \
    code-lsp-go-langserver-package \
    /bin/bash -c "set -x && \
                  for filename in packages/go-langserver-*.zip; do
                    if [[ \$filename == *\"SNAPSHOT\"* ]]; then
                        aws s3 cp \$filename s3://download.elasticsearch.org/code/go-langserver/snapshot/
                    else
                        aws s3 cp \$filename s3://download.elasticsearch.org/code/go-langserver/release/
                    fi
                  done"