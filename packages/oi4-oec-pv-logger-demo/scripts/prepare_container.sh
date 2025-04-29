#!/bin/bash
rm -rf ./build
rm -rf ./node_modules
rm -rf .yarncache

yarn cache clean

mkdir -p ./build/container
mkdir -p ./build/container/scripts
mkdir .yarncache

cp ./package.json ./build/container/package.json
cp ./scripts/entrypoint.sh ./build/container/scripts/entrypoint.sh

if [[ ! -f ./dist/app.js ]]
then
  echo "build step not executed or failed"
  exit 1
fi

cp -r ./dist/ ./build/container/dist

if [[ ! -f ./dist/app.js ]]
then
  echo "Copying dist failed"
  exit 1
fi

cd ./build/container || exit
yarn install --production --cache-folder ./.yarncache
 rm -rf .yarncache

cd ../..
