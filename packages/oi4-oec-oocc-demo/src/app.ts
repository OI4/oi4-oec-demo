import {
    defaultMAMFile,
    defaultSettingsPaths,
    ISettingsPaths,
    OI4ApplicationFactory,
    OI4ApplicationResources
} from '@oi4/oi4-oec-service-node';
import {OECMqttMessageProcessor} from './oocc/OECMqttMessageProcessor';
import {OOCCOI4ApplicationBuilder} from './oocc/OOCCOI4ApplicationBuilder';
import {SubscriptionListConfig} from "@oi4/oi4-oec-service-model";
const basePath =  process.env.BASE_PATH || './docker_configs';

const localTestPaths: ISettingsPaths = {
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

export const isLocal = process.argv.length > 2 && process.argv[2] === 'local';

const healthTopic = 'oi4/+/+/+/+/+/pub/health/+/+/+/+';
const healthSummaryTopic = 'oi4/+/+/+/+/+/pub/health';
const mamTopic = 'oi4/+/+/+/+/+/pub/mam/+/+/+/+';
const mamSummaryTopic = 'oi4/+/+/+/+/+/pub/mam';
const paths: ISettingsPaths = isLocal ? localTestPaths : defaultSettingsPaths;

const getMamFileLocation = (isLocal: boolean) => isLocal ? `${basePath}/config/mam.json` : defaultMAMFile;
const applicationResources = new OI4ApplicationResources(getMamFileLocation(isLocal));
const processor = new OECMqttMessageProcessor(applicationResources.oi4Id.toString());
const appFactory = new OI4ApplicationFactory(applicationResources, paths);
appFactory.mqttMessageProcessor = processor;
appFactory.initialize(new OOCCOI4ApplicationBuilder());
const oi4App = appFactory.createOI4Application();
oi4App.addSubscription(healthTopic, SubscriptionListConfig.NONE_0, 0).then();
oi4App.addSubscription(healthSummaryTopic, SubscriptionListConfig.NONE_0, 0).then();
oi4App.addSubscription(mamTopic, SubscriptionListConfig.NONE_0, 0).then();
oi4App.addSubscription(mamSummaryTopic, SubscriptionListConfig.NONE_0, 0).then();
console.log('|=========== FINISHED initiating OOCC demo ============|');
