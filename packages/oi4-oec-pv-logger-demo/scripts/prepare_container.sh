#!/bin/bash
rm -rf ./build

mkdir -p ./build/container
mkdir -p ./build/container/scripts

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
yarn install --production

cd ../..
