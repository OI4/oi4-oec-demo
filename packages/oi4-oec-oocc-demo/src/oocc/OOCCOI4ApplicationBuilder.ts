import { OI4ApplicationBuilder } from '@oi4/oi4-oec-service-node/dist/application/OI4Application';

export class OOCCOI4ApplicationBuilder extends OI4ApplicationBuilder {

    build() {
        this.mqttSettings.clientId =  `${this.mqttSettings.clientId}-oocc`;
        return super.build();
    }

}
