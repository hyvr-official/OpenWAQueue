#!/usr/bin/env bash

echo "Building Go binaries"

if [ -d "dist" ]; then
    rm -rf dist
fi

export GORUN=true

goreleaser release --clean --skip=publish,validate

ESC="\033"

echo
echo -e "${ESC}[32mBuild complete! Files are in the /dist folder${ESC}[0m"

read -p "Press Enter to continue..."