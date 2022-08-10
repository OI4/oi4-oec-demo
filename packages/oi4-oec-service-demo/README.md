# OI4 OEC OT Demo Connector oi4-oec-service-demo
___
The oi4-oec-service-demo service is demo connector to explain and understand the usage ot the [OI4 OEC service](https://github.com/OI4/oi4-service). 
It simulates ambient sensors by querying the API of [OpenWeather](https://openweathermap.org).

You can run the service locally or as docker container.

## Installation and build
___
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
___

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

The usage of the MQTT settings is explained in the next section.

## General configuration
___
The configuration of the connector follows the definitions of the Open Edge Computing Guideline. It is seperated into the following sections:
- Message Bus storage
- OI4 certificate storage
- Secret storage
- Application specific storages
- MAM setting

It depends on the usage of the connector how the configuration is used and provided. In cast the service is used as docker container, the files and folders must be mapped as volumes to the container.
If the connector is run locally you can either provide the base path to the configuration folders with the environment variable `BASE_PATH` or you can overwrite the configuration that is stored in the docker_configs directory directly (please do not check in any of the files in case you changed them).

By default, the [app.ts](src/app.ts) will use a passphrase file and ignore the client certificate if provided. Just uncomment the lines in the app.ts file to use the client certificate.

### Message Bus storage
The message bus storage contains all the non-sensitive data to connect to the broker, like the CA certificates (not the private key), addresses, ports, etc. and consists of the following elements:
- Message bus CA certificate is stored in the `certs/ca.pem` file.
- Broker settings are stored in the `config/broker.json` file.

### OI4 certificate storage
The certificate storage contains all certificates used in the OEC but not their private keys.
- Message bus client certificate is stored in the `certs/oi4-oec-service-demo.pem` file, in case client certificate based authentication is used.

### Secret storage
Sensitive data like private keys and passphrases are stored in the secret storage.
- Username and password is stored in the `secrets/mqtt_passphrase` in case it is used. The file contains username and password separated by a colon BASE64 encoded.
- Client certificate is stored in the `secrets/mqtt_private_key.pem` in case client certificate based authentication is used.

### Application specific storages
The service will use the following application specific storages:
- The API key of your OpenWeather account is stored as `appid` in the `app/app.json` file.
- Every asset is stored in the `app/assets/` folder following the definition of the [Asset Object](src/application/AssetModel.ts). It contains a MAM with an additional location information (longitude and latitude).

### MAM setting
The MAM setting used by the service is defined in the `config/mam.json` file.

### Environment variables
There are two environment variables that can be used to overwrite the log and event notification level:
- OI4_EDGE_EVENT_LEVEL
- OI4_EDGE_LOG_LEVEL
The values are Syslog levels according to the [RFC 3164](https://tools.ietf.org/html/rfc3164) and can be one of `emergency`,`alert`,`critical`,`error`,`warning`,`notice`,`info`,`debug`
The default value for event notification and logging is `warning`.

## Run the service
___
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
