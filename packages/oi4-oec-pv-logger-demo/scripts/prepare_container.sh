#!/bin/bash
rm -rf ./build

mkdir -p ./build/container
mkdir -p ./build/container/scripts
#mkdir -p ./build/container/dist

cp ./package.json ./build/container/package.json
cp ./scripts/entrypoint.sh ./build/container/scripts/entrypoint.sh

if [[ -f ./dist/app.js ]]
then
  echo "app.js found in build dist"
else
  echo "app.js not found in build dist"
  exit 1
fi

cp -r ./dist/ ./build/container/dist

cd ./build/container || exit
yarn install --production

if [[ -f ./dist/app.js ]]
then
  echo "app.js found"
else
  echo "app.js not found"
  exit 1
fi

cd ../..
