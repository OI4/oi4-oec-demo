#!/bin/bash
rm -rf ./build

mkdir -p ./build/container
mkdir -p ./build/container/scripts
mkdir -p ./build/container/dist

cp ./package.json ./build/container/package.json
cp ./scripts/entrypoint.sh ./build/container/scripts/entrypoint.sh
cp -r ./dist/ ./build/container/dist

cd ./build/container || exit
yarn install --production
cd ../..
