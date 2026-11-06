import {OI4ApplicationBuilder} from '@oi4/oi4-oec-service-node/dist/application/OI4Application';
import {ServiceDemoOI4Application} from './ServiceDemoOI4Application';
import fs from 'fs';
import {ISettingsPaths} from '@oi4/oi4-oec-service-node';

export class ServiceDemoOI4ApplicationBuilder extends OI4ApplicationBuilder {

    appid: string;

    withAppid(paths: ISettingsPaths) {
        const appId = `${paths.secretStorage}/weather_app_id`;
        if (!fs.existsSync(appId)) {
            throw new Error(`Application configuration ${appId} does not exist`);
        }

        this.appid = fs.readFileSync(appId,'utf8');
        return this;
    }

    protected newOI4Application() {
        return new ServiceDemoOI4Application(this.applicationResources, this.messageBus, this.mqttSettings, this.opcUaBuilder, this.clientPayloadHelper, this.clientCallbacksHelper, this.mqttMessageProcessor, this.appid);
    }
}
