# Node-RED example from the community of the Open Industry 4.0 Alliance

> A ready to use Node-RED example to make the first steps with Open Industry 4.0 Alliance its Message Bus communication for the Open Edge Computing layer.

## What does it contain?
The flow contains a few basic components an OI4 compliant OEC layer might exist of:
1. An Edge Device representation with all mandatory resources a device shall provide. This Edge Device representation has its own MQTT client and can be disconnected if not needed.
2. An Application, which represents itself as an application of `serviceType` _OTConnector_. As every application it has its own MQTT client.
3. An additional Device, which gets covered from the _OTConnector_. This device is represented through the application - therefore it has no MQTT client and communicated through the application.

## What does it not contain?
To run a full OI4 compliant OEC system, you need at least a running MQTT broker and a running OEC Registry.

## Usage

1. Import the [flow](./src/flows.json) to a running Node-RED instance. We used Node-RED v2.2.2 at the time.
2. Adopt the MQTT client settings of the configuration nodes `MyApp-MQTT-Client` and `OEC-GW-MQTT-Client` to your MQTT Broker settings.
3. Deploy the flow and check the results in your running [OEC Registry](https://github.com/OI4/oi4-oec-registry).

## Pitfalls 
1. To use this flow in a larger context, e.g. to simulate on-boarding or usage of AAS, please be sure to change at least the serial numbers of each asset (Edge Device, Application and Device) to generate different instances. To do so, just edit the _mam object_ inside the function nodes called `init`. 

2. In case of several Node-RED instances should connect to the same MQTT broker, be aware to change the ClientIds of each MQTT client (hard coded in configuration node).
