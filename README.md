# Open Industry 4.0 Alliance - Open Edge Computing Demos

## Installation

### GitHub package repository access
Dependencies to the oi4-service are available in the GitHub package repository. As the OI4 repositories are private, you need to authenticate to the GitHub repository using a [personal access token](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/creating-a-personal-access-token) (PAT).
The PAT must have the 'read:packages'.
![](docs/pat_access_rights.png)

The installation and build scripts will use the preconfigured .npmrc file to access the GitHub package repository. The PAT must be stored in the .env file in the project root with the key `PACKAGES_AUTH_TOKEN`. As the .env file is not always applied when using the console, the PAT should also be set as an environment variable.
The bash script `setenv.sh` will process the .env file and set the PAT as an environment variable. On Linux based systems just run `source ./scripts/setenv.sh`.

## oi4-oec-oocc-demo

The oi4-oec-oocc-demo service is a demo consumer to explain and understand the usage ot the [OI4 OEC service](https://github.com/OI4/oi4-service).
It converts 1.0 guideline conform messages from the message bus to 0.12 and publishes it to the corresponding endpoint.

What should be done in the future?
- Query the registry for all available mams
- The service library should automatically create a publication list
- The service library should automatically create a subscription list
- Add license profile
- Events are not supported fully yet

[***CLICK***](packages/oi4-oec-oocc-demo/README.md) for a detailed description of the service.

## oi4-oec-service-demo

The oi4-oec-service-demo service is a demo connector to explain and understand the usage ot the [OI4 OEC service](https://github.com/OI4/oi4-service).
It simulates ambient sensors by querying the API of [OpenWeather](https://openweathermap.org).

[***CLICK***](packages/oi4-oec-service-demo/README.md) for a detailed description of the service.

## oi4-oec-pv-logger-demo

The oi4-oec-pv-logger-demo service is a demo consumer to explain and understand the usage ot the [OI4 OEC service](https://github.com/OI4/oi4-service).
It logs OI4 PV messages send to the broker.

[***CLICK***](packages/oi4-oec-pv-logger-demo/README.md) for a detailed description of the service.

## oi4-oec-node-red-demo

The oi4-oec-node-red-demo contains a Node-RED flow, demonstrating the basics about Message Bus communication compliant to OI4 OEC Guideline. It does so by simulating an application and several devices. 

[***CLICK***](packages/oi4-oec-node-red-demo/README.md) for a detailed description of the demo.

## Demo Mosquitto broker

For an easier testing of the service, there is a test / demo Mosquitto broker available at .mosquitto.
Just run `./.mosquitto/start.sh
`
