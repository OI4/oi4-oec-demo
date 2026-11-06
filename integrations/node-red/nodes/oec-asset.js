'use strict';
// Config node oec-asset: an asset an OEC application represents (a Source
// with its own MAM, Health, Config, Data, Event, ...; MG 5, 7.5).

const { mamOf } = require('./oec-app');

module.exports = function (RED) {
  function OecAssetNode(config) {
    RED.nodes.createNode(this, config);
    this.name = config.name;
    this.mam = mamOf(config);
  }
  RED.nodes.registerType('oec-asset', OecAssetNode);
};
