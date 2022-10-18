pwd

rm -rf node_modules/@oi4/oi4-oec-json-schemas
rm -rf node_modules/@oi4/oi4-oec-service-conformity-validator
rm -rf node_modules/@oi4/oi4-oec-service-logger
rm -rf node_modules/@oi4/oi4-oec-service-model
rm -rf node_modules/@oi4/oi4-oec-service-node
rm -rf node_modules/@oi4/oi4-oec-service-opcua-model

mkdir node_modules/@oi4
mkdir node_modules/@oi4/oi4-oec-json-schemas
mkdir ./node_modules/@oi4/oi4-oec-service-conformity-validator
mkdir ./node_modules/@oi4/oi4-oec-service-logger
mkdir ./node_modules/@oi4/oi4-oec-service-model
mkdir ./node_modules/@oi4/oi4-oec-service-node
mkdir ./node_modules/@oi4/oi4-oec-service-opcua-model

cp -R $OI4_SERVICE_PACKAGES_ROOT/oi4-oec-json-schemas/package.json ./node_modules/@oi4/oi4-oec-json-schemas/package.json
cp -R $OI4_SERVICE_PACKAGES_ROOT/oi4-oec-service-conformity-validator/package.json ./node_modules/@oi4/oi4-oec-service-conformity-validator/package.json
cp -R $OI4_SERVICE_PACKAGES_ROOT/oi4-oec-service-logger/package.json ./node_modules/@oi4/oi4-oec-service-logger/package.json
cp -R $OI4_SERVICE_PACKAGES_ROOT/oi4-oec-service-model/package.json ./node_modules/@oi4/oi4-oec-service-model/package.json
cp -R $OI4_SERVICE_PACKAGES_ROOT/oi4-oec-service-node/package.json ./node_modules/@oi4/oi4-oec-service-node/package.json
cp -R $OI4_SERVICE_PACKAGES_ROOT/oi4-oec-service-opcua-model/package.json ./node_modules/@oi4/oi4-oec-service-opcua-model/package.json

cp -R $OI4_SERVICE_PACKAGES_ROOT/oi4-oec-json-schemas/dist ./node_modules/@oi4/oi4-oec-json-schemas/dist
cp -R $OI4_SERVICE_PACKAGES_ROOT/oi4-oec-service-conformity-validator/dist ./node_modules/@oi4/oi4-oec-service-conformity-validator/dist
cp -R $OI4_SERVICE_PACKAGES_ROOT/oi4-oec-service-logger/dist ./node_modules/@oi4/oi4-oec-service-logger/dist
cp -R $OI4_SERVICE_PACKAGES_ROOT/oi4-oec-service-model/dist ./node_modules/@oi4/oi4-oec-service-model/dist
cp -R $OI4_SERVICE_PACKAGES_ROOT/oi4-oec-service-node/dist ./node_modules/@oi4/oi4-oec-service-node/dist
cp -R $OI4_SERVICE_PACKAGES_ROOT/oi4-oec-service-opcua-model/dist ./node_modules/@oi4/oi4-oec-service-opcua-model/dist
