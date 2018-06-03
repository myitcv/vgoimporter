#!/usr/bin/env bash

source "${BASH_SOURCE%/*}/common.bash"

go get -u golang.org/x/vgo

cd $(go list -f "{{.Dir}}" golang.org/x/vgo)

git checkout -qf $VGO_VERSION
if [ "${VGO_CL-}" != "" ]
then
	echo "$VGO_CL"
	eval "$VGO_CL"
fi
go install
