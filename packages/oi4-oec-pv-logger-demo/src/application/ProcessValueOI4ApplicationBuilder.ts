import {OI4ApplicationBuilder} from '@oi4/oi4-oec-service-node/dist/application/OI4Application';
import {IOI4Application} from '@oi4/oi4-oec-service-node';

export class ProcessValueOI4ApplicationBuilder extends OI4ApplicationBuilder {

    build(): IOI4Application {
        this.mqttSettings.clientId = `${this.mqttSettings.clientId}-pv-logger`;
        return super.build();
    }

}
