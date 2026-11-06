'use strict';
// Config node oec-broker: the MQTT broker of the OEC message bus, from the
// container storage (RIG 4.1.3) or set by hand.

const broker = require('../lib/broker');

module.exports = function (RED) {
  function OecBrokerNode(config) {
    RED.nodes.createNode(this, config);
    this.name = config.name;
    this.mode = config.mode === 'manual' ? 'manual' : 'container';
    this.root = config.root || '/';
    this.host = config.host;
    this.port = Number(config.port) || 8883;
    this.tls = config.tls !== false && config.tls !== 'false';
    this.tlsConfig = config.tlsConfig;
    this.clientId = config.clientId || '';
    this.maxPacketSize = Number(config.maxPacketSize) || broker.DEFAULT_MAX_PACKET_SIZE;
  }

  /**
   * {options, maxPacketSize, auth, where} for mqtt.connect() of the
   * application with the Identification Link `il`; throws.
   */
  OecBrokerNode.prototype.describe = function (il) {
    if (this.mode === 'container') {
      const b = broker.fromContainer(this.root);
      return {
        options: broker.connectOptions(b, il),
        maxPacketSize: b.maxPacketSize,
        auth: b.auth,
        where: `${b.host}:${b.port}`,
      };
    }
    if (!this.host) throw new broker.BrokerError('the broker host is not set');
    const b = {
      host: this.host,
      port: this.port,
      tls: this.tls,
      clientId: this.clientId,
      username: this.credentials && this.credentials.username,
      password: this.credentials && this.credentials.password,
    };
    const options = broker.connectOptions(b, il);
    if (this.tls && this.tlsConfig) {
      const tlsNode = RED.nodes.getNode(this.tlsConfig);
      if (tlsNode) tlsNode.addTLSOptions(options);
    }
    return {
      options,
      maxPacketSize: this.maxPacketSize,
      auth: b.username ? 'credentials' : options.cert ? 'client certificate' : 'none',
      where: `${b.host}:${options.port}`,
    };
  };

  RED.nodes.registerType('oec-broker', OecBrokerNode, {
    credentials: { username: { type: 'text' }, password: { type: 'password' } },
  });
};
