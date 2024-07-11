# Open Industry 4.0 Alliance - Open Edge Computing Demos

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
Just run `./.mosquitto/start.sh`

## oi4-oec-oocc-demo

The oi4-oec-oocc-demo service is a demo consumer to explain and understand the usage ot the [OI4 OEC service](https://github.com/OI4/oi4-service).
It converts 1.1 guideline conform messages from the message bus to 0.12 and publishes it to the corresponding endpoint.

What should be done in the future?
- Query the registry for all available mams
- The service library should automatically create a publication list
- The service library should automatically create a subscription list
- Add license profile
- Events are not supported fully yet

[***CLICK***](packages/oi4-oec-oocc-demo/README.md) for a detailed description of the service.
