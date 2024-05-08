import {defaultSettingsPaths, ISettingsPaths, OI4ApplicationFactory,} from '@oi4/oi4-oec-service-node';
import {ServiceDemoOI4ApplicationResources} from './application/ServiceDemoOI4ApplicationResources';
import {ServiceDemoOI4ApplicationBuilder} from "./application/ServiceDemoOI4ApplicationBuilder";

export {WeatherService} from './weather/WeatherService';
export * from './weather/WeatherServiceModel';

const basePath =  process.env.BASE_PATH || './docker_configs';

const LocalTestPaths: ISettingsPaths = {
    mqttSettings: {
        brokerConfig: `${basePath}/mqtt/broker.json`,
        caCertificate: `${basePath}/certs/ca.pem`,
        // privateKey: `${basePath}/secrets/mqtt_private_key.pem`,
        privateKey: undefined,
        // clientCertificate: `${basePath}/certs/oi4-oec-service-demo.pem`,
        clientCertificate: undefined,
        // passphrase: `${basePath}/secrets/mqtt_passphrase`,
        passphrase: undefined,
        credentials: `${basePath}/secrets/mqtt_credentials`
    },
    certificateStorage: `${basePath}/certs/`,
    secretStorage: `${basePath}/secrets`,
    applicationSpecificStorages: {
        configuration: `${basePath}/app`,
        data: `${basePath}/app`
    }
}

export const isLocal = process.argv.length > 2 && process.argv[2] === 'local';

const paths: ISettingsPaths = isLocal ? LocalTestPaths : defaultSettingsPaths;
const applicationResources = new ServiceDemoOI4ApplicationResources(isLocal, paths);
const builder = new ServiceDemoOI4ApplicationBuilder().withAppid(paths);
const applicationFactory = new OI4ApplicationFactory(applicationResources, paths).initialize(builder);

applicationFactory.createOI4Application();

console.log('|=========== FINISHED initiating Service Demo ============|');
