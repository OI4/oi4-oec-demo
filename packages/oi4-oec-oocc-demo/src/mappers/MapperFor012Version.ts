import {IOPCUANetworkMessage} from '@oi4/oi4-oec-service-model';

// TODO This function is broken at the moment
export function map1To012Version(opcuaMessage:  IOPCUANetworkMessage) {
    // @ts-ignore
    opcuaMessage.Messages.forEach(message => {
        // eslint-disable-next-line @typescript-eslint/ban-ts-ignore
       // @ts-ignore
        message.POI = message.subResource;
        //delete message.subResource;
        //delete message.filter;
   });
    // eslint-disable-next-line @typescript-eslint/ban-ts-ignore
    // @ts-ignore
    opcuaMessage.CorrelationId = opcuaMessage.correlationId;
    //delete opcuaMessage.correlationId;
    return opcuaMessage;
}
