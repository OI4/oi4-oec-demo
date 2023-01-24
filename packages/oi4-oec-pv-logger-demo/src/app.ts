import {SubscriptionListConfig} from '@oi4/oi4-oec-service-model';
import {
    defaultMAMFile,
    defaultSettingsPaths,
    foreignMessage,
    ISettingsPaths,
    OI4ApplicationFactory,
    OI4ApplicationResources
} from '@oi4/oi4-oec-service-node';
import {listener} from './application/ProcessValueMessageListener';
import {ProcessValueOI4ApplicationBuilder} from './application/ProcessValueOI4ApplicationBuilder';

const basePath = process.env.BASE_PATH || './docker_configs';

const LocalTestPaths: ISettingsPaths = {
    mqttSettings: {
        brokerConfig: `${basePath}/mqtt/broker.json`,
        caCertificate: `${basePath}/docker_configs/certs/ca.pem`,
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

export const IS_LOCAL = process.argv.length > 2 && process.argv[2] === 'local';

const PV_TOPIC = 'oi4/+/+/+/+/+/pub/data/+/+/+/+/oi4_pv';

const paths: ISettingsPaths = IS_LOCAL ? LocalTestPaths : defaultSettingsPaths;

const getMamFileLocation = (isLocal: boolean) => isLocal ? `${basePath}/config/mam.json` : defaultMAMFile;
const applicationResources = new OI4ApplicationResources(getMamFileLocation(IS_LOCAL));
const oi4App = new OI4ApplicationFactory(applicationResources, paths).initialize(new ProcessValueOI4ApplicationBuilder()).createOI4Application();;
oi4App.addListener(foreignMessage, listener);
oi4App.addSubscription(PV_TOPIC, SubscriptionListConfig.NONE_0, 0).then();

console.log('|=========== FINISHED initiating Service Demo ============|');
