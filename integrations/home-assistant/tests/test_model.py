"""Tests of the bus state model.py (standard library unittest)."""

from __future__ import annotations

import json
import unittest

from _load import load

oec = load("oec")
model = load("model")

APP = oec.Application(oec.Identity("ha-test", "0.1.0"), lambda: 1793982000.0)
WEATHER = "oi4.org/OEC,20Weather/OEC-WEATHER/w1"
STATION = "oi4.org/OEC,20Weather,20Station/OEC-WEATHER-STATION/w1-station-1"
PUBLISHER = "OTConnector/" + WEATHER


def nm(messages: list[dict], reply_to: str = "", class_id: str | None = None, **extra) -> str:
    body = {"MessageId": "1-" + PUBLISHER, "MessageType": "ua-data", "PublisherId": PUBLISHER,
            "ReplyTo": reply_to, "Messages": messages, **extra}
    if class_id:
        body["DataSetClassId"] = class_id
    return json.dumps(body)


def dsm(writer_id: int, il: str, payload: dict, group: str = "", **extra) -> dict:
    return {"DataSetWriterId": writer_id, "WriterGroupName": group, "DataSetWriterName": il,
            "Payload": payload, **extra}


def mam(il: str, model_text: str, serial: str) -> dict:
    return {"Manufacturer": {"Locale": "en-US", "Text": "Open Industry 4.0 Alliance"},
            "ManufacturerUri": "oi4.org", "Model": {"Locale": "en-US", "Text": model_text},
            "ProductCode": "P", "HardwareRevision": "undefined", "SoftwareRevision": "0.1.0",
            "SerialNumber": serial, "ProductInstanceUri": il, "RevisionCounter": 1}


def topic(method: str, resource: str, source: str | None = None, filter_: str | None = None) -> str:
    return oec.topic("OTConnector", WEATHER, method, resource, source, filter_)


METADATA = {
    "MessageId": "2-" + PUBLISHER, "MessageType": "ua-metadata", "PublisherId": PUBLISHER,
    "ReplyTo": "x", "DataSetWriterId": 36, "WriterGroupName": "Oi4Pv", "DataSetWriterName": STATION,
    "MetaData": {"Name": "Oi4Pv", "Fields": [
        {"Name": "Pv", "BuiltInType": 11, "ValueRank": -1, "Description": {"Locale": "en-US", "Text": "Air temperature"},
         "Properties": [{"Key": "EngineeringUnits", "Value": {"UaType": 22, "Value": {
             "UaTypeId": "i=887", "UnitId": -1, "DisplayName": {"Text": "°C"}}}}]},
        {"Name": "Sv4", "BuiltInType": 11, "ValueRank": -1,
         "Description": {"Locale": "en-US", "Text": "Wind direction (from, clockwise from north)"}},
        {"Name": "Samples", "BuiltInType": 11, "ValueRank": 1, "ArrayDimensions": [3]},
    ]},
}


class DirectoryTest(unittest.TestCase):
    def setUp(self) -> None:
        self.directory = model.Directory(APP)

    def ingest(self, topic_: str, payload: str | dict, now: float = 100.0):
        if isinstance(payload, dict):
            payload = json.dumps(payload)
        return self.directory.ingest(topic_, payload.encode(), now)

    def test_discovery_on_first_message(self) -> None:
        changes = self.ingest(topic("Pub", "Health", WEATHER),
                              nm([dsm(11, WEATHER, {"Health": "NORMAL_0", "HealthScore": 100})]))
        self.assertEqual(changes.new_apps, [WEATHER])
        self.assertEqual(changes.health, [WEATHER])
        self.assertEqual([r.topic for r in changes.requests],
                         [topic("Get", "MAM"), topic("Get", "Health"), topic("Get", "Data")])
        self.assertTrue(self.directory.app_online(WEATHER))
        again = self.ingest(topic("Pub", "Health", WEATHER),
                            nm([dsm(11, WEATHER, {"Health": "OFF_SPEC_3", "HealthScore": 50})]))
        self.assertEqual((again.new_apps, again.health, again.requests), ([], [], []))
        self.assertEqual(self.directory.assets[WEATHER].health, "OFF_SPEC_3")

    def test_mam_subordinate_assets_and_names(self) -> None:
        changes = self.ingest(topic("Pub", "MAM"), nm(
            [dsm(10, WEATHER, mam(WEATHER, "OEC Weather", "w1")),
             dsm(28, STATION, mam(STATION, "OEC Weather Station", "w1-station-1-with-a-long-serial"))],
            reply_to="1-x", class_id=oec.CLASS_MAM))
        self.assertEqual(changes.new_assets, [WEATHER, STATION])
        self.assertEqual(changes.devices, {WEATHER, STATION})
        station = self.directory.assets[STATION]
        self.assertFalse(station.is_app)
        self.assertEqual(station.app_il, WEATHER)
        self.assertEqual(self.directory.assets[WEATHER].name, "OEC Weather w1")
        self.assertEqual(station.name, "OEC Weather Station …-long-serial")
        self.assertIsNone(self.directory.app_online(WEATHER))  # an answer is no Birth

    def test_lifecycle(self) -> None:
        health = topic("Pub", "Health", WEATHER)
        self.ingest(health, nm([dsm(11, WEATHER, {"Health": "NORMAL_0", "HealthScore": 100})]))
        self.ingest(health, nm([dsm(11, WEATHER, {"Health": "NORMAL_0", "HealthScore": 0})]))  # Close
        self.assertIs(self.directory.app_online(WEATHER), False)
        self.ingest(health, nm([dsm(11, WEATHER, {"Health": "NORMAL_0", "HealthScore": 100})]))
        self.assertIs(self.directory.app_online(WEATHER), False)  # REQ-LIFE-013: only a Birth
        birth = self.ingest(topic("Pub", "MAM", WEATHER), nm([dsm(10, WEATHER, mam(WEATHER, "OEC Weather", "w1"))]),
                            now=200.0)
        self.assertIs(self.directory.app_online(WEATHER), True)
        self.assertEqual(len(birth.requests), 3)  # rediscovery after the Birth
        self.ingest(health, nm([dsm(11, WEATHER, {"Health": "FAILURE_1", "HealthScore": 0})]))  # Will
        self.assertIs(self.directory.app_online(WEATHER), False)

    def test_data_metadata_and_fields(self) -> None:
        data = nm([dsm(36, STATION, {"Pv": 6.2, "Sv4": 73.0, "Samples": [1, 2, 3], "On": True, "Label": "x",
                                     "Nested": {"A": 1}}, group="Oi4Pv", MessageType="ua-keyframe"),
                   dsm(2, STATION, {"TotalCount": 1}, group="Pagination")])
        changes = self.ingest(topic("Pub", "Data"), data)
        self.assertIn(STATION, changes.new_assets)
        [request] = [r for r in changes.requests if "/Get/Metadata/" in r.topic]
        self.assertEqual(request.topic, topic("Get", "Metadata", STATION, "Oi4Pv"))
        self.assertEqual(changes.fields, [])  # waits for the Metadata
        dataset = self.directory.datasets[(STATION, "Oi4Pv")]
        self.assertEqual(dataset.writer_id, 36)
        meta = self.ingest(topic("Pub", "Metadata", STATION, "Oi4Pv"), METADATA)
        [(key, names)] = meta.fields
        self.assertEqual(key, (STATION, "Oi4Pv"))
        self.assertEqual(names, ["Pv", "Sv4", "On", "Label"])
        self.assertEqual(dataset.fields["Pv"].unit, "°C")
        self.assertEqual(dataset.fields["Pv"].label, "Air temperature")
        self.assertEqual(dataset.fields["Sv4"].label, "Wind direction")
        self.assertEqual(dataset.kind("On"), model.KIND_BOOLEAN)
        self.assertEqual(dataset.kind("Label"), model.KIND_TEXT)
        self.assertIsNone(dataset.kind("Samples"))
        self.assertIsNone(dataset.kind("Nested"))
        delta = self.ingest(topic("Pub", "Data", STATION, "Oi4Pv"),
                            nm([dsm(36, STATION, {"Pv": 7.0, "New": 1}, group="Oi4Pv", MessageType="ua-deltaframe")]))
        self.assertEqual(delta.fields, [((STATION, "Oi4Pv"), ["New"])])
        self.assertEqual(dataset.values["Pv"], 7.0)
        self.assertEqual(dataset.values["Sv4"], 73.0)

    def test_metadata_timeout(self) -> None:
        self.ingest(topic("Pub", "Data", STATION, "Climate"),
                    nm([dsm(19, STATION, {"Temperature": 21.8}, group="Climate")]), now=100.0)
        self.assertEqual(self.directory.tick(105.0).fields, [])
        self.assertEqual(self.directory.tick(100.0 + model.METADATA_WAIT).fields,
                         [((STATION, "Climate"), ["Temperature"])])
        self.assertEqual(self.directory.tick(200.0).fields, [])

    def test_own_messages_and_requests(self) -> None:
        own = APP.identity.il
        self.assertFalse(self.directory.ingest(str(APP.birth().topic), APP.birth().payload, 1.0))
        get_topic = oec.topic("ITConnector", own, "Get", "MAM")
        changes = self.directory.ingest(get_topic, nm([]), 1.0)
        self.assertIsNotNone(changes.request)
        set_topic = oec.topic("ITConnector", own, "Set", "Data", own, "Tag")
        self.assertIsNotNone(self.directory.ingest(set_topic, nm([]), 1.0).request)
        self.assertEqual(self.directory.apps, {})
        for method in ("Get", "Set", "Del"):
            self.assertIn(f"Oi4v2/ITConnector/{APP.identity.app_id}/{method}/#", self.directory.subscriptions())

    def test_name_from_identifier_until_mam(self) -> None:
        self.ingest(topic("Pub", "Health", WEATHER), nm([dsm(11, WEATHER, {"Health": "NORMAL_0", "HealthScore": 100})]))
        self.assertEqual(self.directory.assets[WEATHER].name, "OEC Weather w1")
        long_serial = "oi4.org/OEC,20Config/OEC-CONFIG/oec-config-2df334f0-48eb-4582-8493-6bdac7a97961"
        self.assertEqual(model.Asset(long_serial, long_serial).name, "OEC Config …6bdac7a97961")
        self.assertEqual(model.Asset("odd", "odd").name, "odd")

    def test_registry_mirror_and_own_application(self) -> None:
        registry = "oi4.org/OEC,20Registry/OEC-REGISTRY/r1"
        own = APP.identity.il

        def rtopic(resource: str) -> str:
            return oec.topic("Registry", registry, "Pub", resource)

        # The Registry answers Get MAM/Health with the entries of the whole bus.
        changes = self.ingest(rtopic("MAM"), nm([dsm(10, registry, mam(registry, "OEC Registry", "r1")),
                                                 dsm(28, WEATHER, mam(WEATHER, "OEC Weather", "w1")),
                                                 dsm(86, own, mam(own, "OEC Home Assistant", "ha-test"))], "1-x"))
        self.assertEqual(changes.new_assets, [registry])
        self.ingest(rtopic("Health"), nm([dsm(11, registry, {"Health": "NORMAL_0", "HealthScore": 100}),
                                          dsm(87, own, {"Health": "NORMAL_0", "HealthScore": 0})], "1-x"))
        self.assertEqual(set(self.directory.assets), {registry})
        self.assertTrue(self.directory.app_online(registry))

        # Another application lists an asset before its application is seen: it moves to its application.
        gateway = "oi4.org/Gateway/GW/g1"
        self.ingest(oec.topic("Aggregation", gateway, "Pub", "MAM"),
                    nm([dsm(10, gateway, mam(gateway, "Gateway", "g1")), dsm(28, WEATHER, mam(WEATHER, "OEC Weather", "w1"))], "1-x"))
        self.assertEqual(self.directory.assets[WEATHER].app_il, gateway)
        changes = self.ingest(topic("Pub", "Health", WEATHER), nm([dsm(11, WEATHER, {"Health": "NORMAL_0", "HealthScore": 100})]))
        self.assertEqual(self.directory.assets[WEATHER].app_il, WEATHER)
        self.assertIn(WEATHER, changes.devices)
        # From then on the application is the authority for itself.
        self.ingest(oec.topic("Aggregation", gateway, "Pub", "Health"),
                    nm([dsm(29, WEATHER, {"Health": "FAILURE_1", "HealthScore": 0})], "1-x"))
        self.assertEqual(self.directory.assets[WEATHER].health, "NORMAL_0")
        self.assertTrue(self.directory.app_online(WEATHER))

    def test_garbage_is_ignored(self) -> None:
        for topic_, payload in (("Oi4v2/OTConnector/!!/Pub/MAM", "{}"), (topic("Pub", "MAM"), "no json"),
                                ("other/topic", "{}"), (topic("Get", "MAM"), nm([]))):
            self.assertFalse(self.directory.ingest(topic_, payload.encode(), 1.0))


if __name__ == "__main__":
    unittest.main()
