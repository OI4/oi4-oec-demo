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


# cp ./scripts/entrypoint.sh ./build/container/packages/oi4-registry-service/scripts/entrypoint.sh
# cp ./packages/oi4-registry-service/package.json ./build/container/packages/oi4-registry-service/package.json
# cp ./packages/oi4-registry-service/.npmrc ./build/container/packages/oi4-registry-service/.npmrc
# cp -r ./packages/oi4-registry-service/dist/ ./build/container/packages/oi4-registry-service/src
# cp -r ./packages/oi4-registry-service/public/ ./build/container/packages/oi4-registry-service/public
# cd ./build/container/packages/oi4-registry-service || exit
# yarn install --production
# echo "Prepared oi4-registry-service for container creation"
# cd ../../../..

# To avoid loadig all the dependencies of the ui, execute this as last
# echo "****************************"
# echo "**  Install oi4-local-ui  **"
# echo "****************************"
# cp ./packages/oi4-local-ui/package.json ./build/container/packages/oi4-local-ui/package.json
# cp -r ./packages/oi4-local-ui/build ./build/container/packages/oi4-local-ui/
# echo "Prepared oi4-local-ui for container creation"
