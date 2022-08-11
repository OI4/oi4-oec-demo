# OI4 OEC OT Demo Connector oi4-oec-service-demo

The oi4-oec-service-demo service is a demo connector to explain and understand the usage ot the [oi4-oec-service](https://github.com/OI4/oi4-service). 
It simulates ambient sensors by querying the API of [OpenWeather](https://openweathermap.org).

You can run the service locally or as docker container.

## Installation and build

Make sure you provided a PAT as described in the root [README.md](../../README.md) file.
```
yarn install
yarn build
docker:build:local
```

In case you need changes from the oi4-service that are not yet available as a package, you can copy the service code to the oi4-oec-service-demo project with the following two commands:
```
copy:dependencies
docker:build:snapshot
```
The copy command needs the information where to find the oi4-service code on your machine. This information is provided in the .env file with the environment variable `OI4_SERVICE_PACKAGES_ROOT`.

## Prerequisites

### GitHub package repository access
Apply the configuration as described in the root [README.md](../../README.md) file.

### Setup OpenWeather account 
The OpenWeather API requires an account to retrieve the weather data. OpenWeather provides a free account - [SIGN UP](https://home.openweathermap.org/users/sign_up). 
Once you created an account switch to your API keys and note it down. The service uses the API key provided in the app.json as appid.

The usage of the app.json and other configuration files is explained in the next section.

### MQTT settings
The OT Demo Connector uses the MQTT connection of the OI4 OEC service. It requires a secure MQTT over TLS connection and either a client certificate or a username and password based authentication.
You will need the Certificate Authority (CA) certificate that singed the certificate of the MQTT broker. For the authentication you need the username and password or the client certificate and its according private key.
In case the private key is protected with a passphrase, you will need to provide the passphrase as well.

## Configuration
The general configuration of the service is described in the [wiki](https://github.com/OI4/oi4-oec-service/wiki/Configuration-of-OEC-services) of the oi4-oec-service.
The demo service will handle all common configuration as described there. In addition there are the following configuration locations used.

### Application specific storages
The service will use the following application specific storages:
- The API key of your OpenWeather account is stored as `appid` in the `app/app.json` file.
- Every asset is stored in the `app/assets/` folder following the definition of the [Asset Object](src/application/AssetModel.ts). It contains a MAM with an additional location information (longitude and latitude).

### MAM setting
The MAM setting used by the service is defined in the `config/mam.json` file.

## Building a docker image

### Modules in OI4 GitHub package repo
The oi4-service Node.js modules are published to a private repository on GitHub. To access the modules the repository must be registered and a PAT (personal access token) is needed for the authentication.
This is typically done by putting a .npmrc file into to current working directory.
As credentials shall not be shared in a GitHub repository the file uses an environment variable `PACKAGES_AUTH_TOKEN` to retrieve the PAT.
If the node module dependencies are installed in the dockerfile during the build process (e.g. with yarn install), the build process must also use the .npmrc file and authenticate against the repository.

In the example Docker image build process this is done by copying the .npmrc file and the PAT is provided as build argument.

### Using unpublished versions of oi4-service in docker builds
Current, up-to-date versions of the oi4-service are published as node modules to the GitHub package repository.
It is always recommended to use these packages for any service. In cases where unreleased and unpublished versions of the oi4-service should be used (e.g. when working on the oi4-service itself) a `yarn install` or similar will not work.
The demo image uses a build flag BUILD_ENV to switch to a snapshot build, which will copy the content of the `node_module/oi4` folder to the image. This can be used to link the latest versions of the oi4-service to the node_modules.

BUT symlinks will not work with docker build. Therefore, a `yarn link` will not work. Best known solution so far is, to physically copy the folder to the node_modules folder.

## General configuration
The configuration of the connector follows the definitions of the Open Edge Computing Guideline. 

It depends on the usage of the connector how the configuration is used and provided. In cast the service is used as docker container, the files and folders must be mapped as volumes to the container.
If the connector is run locally you can either provide the base path to the configuration folders with the environment variable `BASE_PATH` or you can overwrite the configuration that is stored in the docker_configs directory directly (please do not check in any of the files in case you changed them).

By default, the [app.ts](src/app.ts) will use a passphrase file and ignore the client certificate if provided. Just uncomment the lines in the app.ts file to use the client certificate.


## Run the service
To run the service make sure you processed the steps above to build and configure the service.

### Run the service locally
The oi4-oec-service-demo is basically a nodejs application. You can run it with the following command:
```
cd packages/oi4-oec-service-demo
node dist/app.js local
```
It will start the service in the local mode where the configuration is taken from the docker_configs directory by default.
There is also yarn script doing the same thing. Just run `yarn run testLocal`

### Run the service as docker container
The main intention of the service is to run as docker container, of course. As every OI4 OEC service this service need a couple of mounted files and directories.
The easiest way to run the container is with the help of the [docker-compose file](docker_configs/docker-compose.yml).
All you need to do is to adjust the host location of the volumes and run `docker compose up -d`.
