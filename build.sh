#!/bin/sh
mkdir -p dist
go build -ldflags=-w -ldflags=-s -o dist/oniontalk github.com/nthnn/oniontalk
cp -r static dist/
