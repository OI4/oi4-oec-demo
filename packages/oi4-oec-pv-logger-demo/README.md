# OI4 OEC data consumer demo oi4-oec-pv-logger-demo

The oi4-oec-pv-logger-demo service is a demo service to explain and understand the usage ot the [oi4-oec-service](https://github.com/OI4/oi4-service).
It simulates a consumer of OI4 OEC data from the bus.

You can run the service locally or as docker container.

## Installation and build

Make sure you provided a PAT as described in the root [README.md](../../README.md) file.
```
yarn install
yarn build
docker:build:local
```

In case you need changes from the oi4-service that are not yet available as a package, you can copy the service code to the oi4-oec-pv-logger-demo project with the following two commands:
```
copy:dependencies
docker:build:snapshot
```
The copy command needs the information where to find the oi4-service code on your machine. This information is provided in the .env file with the environment variable `OI4_SERVICE_PACKAGES_ROOT`.

## Prerequisites

### GitHub package repository access
Apply the configuration as described in the root [README.md](../../README.md) file.

### MQTT settings
The service uses the MQTT connection of the OI4 OEC service. It requires a secure MQTT over TLS connection and either a client certificate or a username and password based authentication.
You will need the Certificate Authority (CA) certificate that singed the certificate of the MQTT broker. For the authentication you need the username and password or the client certificate and its according private key.
In case the private key is protected with a passphrase, you will need to provide the passphrase as well.

## Configuration
The general configuration of the service is described in the [wiki](https://github.com/OI4/oi4-oec-service/wiki/Configuration-of-OEC-services) of the oi4-oec-service.
The demo service will handle all common configuration as described there. In addition there are the following configuration locations used.

### MAM setting
The MAM setting used by the service is defined in the `config/mam.json` file.

## Building a docker image

How to build the service as a docker image can be found [HERE](../oi4-oec-service-demo/README.md

## General configuration
The configuration of the connector follows the definitions of the Open Edge Computing Guideline.

It depends on the usage of the connector how the configuration is used and provided. In cast the service is used as docker container, the files and folders must be mapped as volumes to the container.
If the connector is run locally you can either provide the base path to the configuration folders with the environment variable `BASE_PATH` or you can overwrite the configuration that is stored in the docker_configs directory directly (please do not check in any of the files in case you changed them).

By default, the [app.ts](src/app.ts) will use a passphrase file and ignore the client certificate if provided. Just uncomment the lines in the app.ts file to use the client certificate.

## Run the service
To run the service make sure you processed the steps above to build and configure the service.

### Run the service locally
The oi4-oec-pv-logger-demo is basically a nodejs application. You can run it with the following command:
```
cd packages/oi4-oec-pv-logger-demo
node dist/app.js local
```
It will start the service in the local mode where the configuration is taken from the docker_configs directory by default.
There is also yarn script doing the same thing. Just run `yarn run testLocal`

### Run the service as docker container
The main intention of the service is to run as docker container, of course. As every OI4 OEC service this service need a couple of mounted files and directories.
The easiest way to run te container is with the help of the prepared docker configuration.
```shell
yarn run unZip
cd docker/oi4-oec-pv-logger-demo
docker compose up -d
```
