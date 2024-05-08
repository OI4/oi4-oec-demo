import {TopicInfo} from '@oi4/oi4-oec-service-node';

import {ESyslogEventFilter, IOPCUANetworkMessage} from '@oi4/oi4-oec-service-model';

import {logger} from '@oi4/oi4-oec-service-logger';

export const listener = (topicInfo: TopicInfo, parsedMessage: IOPCUANetworkMessage): void => {
    logger.log(`Message received from ${topicInfo.toString()} with values: ${JSON.stringify(parsedMessage?.Messages[0]?.Payload)}`, ESyslogEventFilter.informational)
};
