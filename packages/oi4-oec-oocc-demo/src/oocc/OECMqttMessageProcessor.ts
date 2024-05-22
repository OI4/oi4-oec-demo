import {MqttMessageProcessor, TopicInfo} from '@oi4/oi4-oec-service-node';
import { IOPCUANetworkMessage, Methods} from '@oi4/oi4-oec-service-model';
import {map1To012Version} from '../mappers/MapperFor012Version';
import axios from 'axios';
import {AASCredentialsHelper} from '../helpers/AASCredentialsHelper';
import {readFileSync} from 'fs';

export class OECMqttMessageProcessor extends MqttMessageProcessor {
    constructor(private oi4Id: string) {
        super();
    }
    private assCredentialsPath = './docker_configs/secrets/aas_credentials';
    private assConfigPath = './docker_configs/app/aas_endpoint.json';

    private getBaseAuth() {
        const credentials = new AASCredentialsHelper({credentials: this.assCredentialsPath}).loadUserCredentials();
        return {username: credentials.username, password: credentials.password  };
    }
    private readAppConfig(): {resourceUrl: string} {
        return JSON.parse(readFileSync(this.assConfigPath, {encoding: 'utf-8'}));
    }
    async handleForeignMessage(topicInfo: TopicInfo, parsedMessage: IOPCUANetworkMessage): Promise<void> {

        const convertedMessage = map1To012Version(parsedMessage);
        console.log(`Message received from ${topicInfo.toString()} with values: ${JSON.stringify(parsedMessage?.Messages[0]?.Payload)}`);
        const auths = this.getBaseAuth();
        await axios.post(`${this.readAppConfig().resourceUrl}/${Methods.PUB}/${topicInfo.resource}`, convertedMessage, {
                auth: auths,
                params: {appId: this.oi4Id}
            }).then((res) => console.log('Message successfully posted', res.data))
            .catch(console.log)
    }
}
