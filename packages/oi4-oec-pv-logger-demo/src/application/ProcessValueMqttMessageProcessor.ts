import { MqttMessageProcessor, TopicInfo } from '@oi4/oi4-oec-service-node';
import {LOGGER} from '@oi4/oi4-oec-service-logger';
import { ESyslogEventFilter } from '@oi4/oi4-oec-service-model';
import { IOPCUANetworkMessage } from '@oi4/oi4-oec-service-opcua-model';

export class ProcessValueMqttMessageProcessor extends MqttMessageProcessor {

    async handleForeignMessage(topicInfo: TopicInfo, parsedMessage: IOPCUANetworkMessage): Promise<void> {
        LOGGER.log(`Message received from ${topicInfo.topic} with values: ${JSON.stringify(parsedMessage?.Messages[0]?.Payload)}`, ESyslogEventFilter.informational);
    }
}
