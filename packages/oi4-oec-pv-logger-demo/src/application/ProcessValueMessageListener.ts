import {TopicInfo} from '@oi4/oi4-oec-service-node';
import {logger} from '@oi4/oi4-oec-service-logger';
import {ESyslogEventFilter} from '@oi4/oi4-oec-service-model';
import {IOPCUANetworkMessage} from '@oi4/oi4-oec-service-opcua-model';

export const listener = (topicInfo: TopicInfo, parsedMessage: IOPCUANetworkMessage): void => {
    logger.log(`Message received from ${topicInfo.topic} with values: ${JSON.stringify(parsedMessage?.Messages[0]?.Payload)}`, ESyslogEventFilter.informational)
};
