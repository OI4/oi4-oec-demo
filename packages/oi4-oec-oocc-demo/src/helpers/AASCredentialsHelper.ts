import {BaseCredentialsHelper} from '@oi4/oi4-oec-service-node/dist/Utilities/Helpers/BaseCredentialsHelper';
import {IMqttSettingsPaths} from '@oi4/oi4-oec-service-node';

export class AASCredentialsHelper extends BaseCredentialsHelper {
    constructor(settings: {credentials: string}) {
        super(settings as IMqttSettingsPaths);
    }
}
