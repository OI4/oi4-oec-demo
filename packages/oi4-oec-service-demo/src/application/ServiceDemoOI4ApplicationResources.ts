import {OI4ApplicationResources, defaultMAMFile, ISettingsPaths} from '@oi4/oi4-oec-service-node';
import fs from 'fs';
import {Asset} from './AssetModel';
import {initializeLogger, logger} from '@oi4/oi4-oec-service-logger';
import {getServiceType, ESyslogEventFilter} from '@oi4/oi4-oec-service-model';

const basePath =  process.env.BASE_PATH || './docker_configs';
const getMamFileLocation = (isLocal: boolean) => isLocal ? `${basePath}/config/mam.json` : defaultMAMFile;

export class ServiceDemoOI4ApplicationResources extends OI4ApplicationResources {

    readonly assets: Asset[];

    constructor(isLocal: boolean, paths: ISettingsPaths) {
        super(getMamFileLocation(isLocal));

        this.initializeLogger();

        const assetFolder = `${paths.applicationSpecificStorages.configuration}/assets`;
        if (!fs.existsSync(assetFolder)) {
            throw new Error(`Asset folder ${assetFolder} does not exist`);
        }

        const files = fs.readdirSync(assetFolder);
        // files object contains all files names
        // log them on console
        this.assets = files.map(file => {
            try {
                return Asset.clone(JSON.parse(fs.readFileSync(`${assetFolder}/${file}`, 'utf-8')) as Asset);
            } catch (error) {
                logger.log(`File ${file} is not a valid asset file and is skipped`);
                return undefined;
            }
        }).filter(asset => asset !== undefined);

        this.assets.map(asset => {
            const masterAssetModel = asset.toMasterAssetModel();
            this.addSource(masterAssetModel);
        });
    }

    initializeLogger(): void {
        if(logger === undefined) {
            const publishingLevel: ESyslogEventFilter = process.env.OI4_EDGE_EVENT_LEVEL as ESyslogEventFilter | ESyslogEventFilter.warning;
            const logLevel = process.env.OI4_EDGE_LOG_LEVEL ? process.env.OI4_EDGE_LOG_LEVEL as ESyslogEventFilter : publishingLevel;
            initializeLogger(true, undefined, logLevel, publishingLevel, this.oi4Id, getServiceType(this.mam.DeviceClass));
        }
    }
}
