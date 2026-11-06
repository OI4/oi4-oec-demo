"""Tests of the protocol core oec.py (standard library unittest)."""

from __future__ import annotations

import json
import unittest

from _load import load

oec = load("oec")

CLOCK = lambda: 1793982000.5  # noqa: E731
IDENTITY = oec.Identity(serial_number="0123456789abcdef0123456789abcdef", software_revision="0.1.0")
IL = "oi4.org/OEC,20Home,20Assistant/OEC-HOME-ASSISTANT/0123456789abcdef0123456789abcdef"
REQUESTER = "ITConnector/oi4.org/OEC,20Check/OEC-CHECK/d7"


def get(resource: str, source: str | None = None, messages: list | None = None, method: str = "Get",
        filter_: str | None = None, **extra) -> tuple:
    topic = oec.topic("ITConnector", IL, method, resource, source, filter_)
    body = {
        "MessageId": "1793982000000-" + REQUESTER,
        "MessageType": "ua-data",
        "PublisherId": REQUESTER,
        "ReplyTo": "",
        "Messages": messages or [],
        **extra,
    }
    return oec.parse_topic(topic), oec.parse_message(json.dumps(body))


class EncodingTest(unittest.TestCase):
    def test_dnp_encoding(self) -> None:
        # MG A3 examples: space, comma, slash and non-ASCII octets.
        self.assertEqual(oec.dnp_encode("OEC Home Assistant"), "OEC,20Home,20Assistant")
        self.assertEqual(oec.dnp_encode("a,b/c"), "a,2Cb,2Fc")
        self.assertEqual(oec.dnp_encode("Ü"), ",C3,9C")
        self.assertEqual(oec.dnp_encode("A-1._~"), "A-1._~")
        for text in ("OEC Home Assistant", "a,b/c", "Ü", "A-1._~"):
            self.assertEqual(oec.dnp_decode(oec.dnp_encode(text)), text)
        self.assertEqual(oec.dnp_decode("a,zz,4"), "a,zz,4")

    def test_identity(self) -> None:
        self.assertEqual(IDENTITY.il, IL)
        self.assertEqual(IDENTITY.publisher_id, "ITConnector/" + IL)
        self.assertEqual(oec.decode_id(IDENTITY.app_id), IL)
        self.assertNotIn("=", IDENTITY.app_id)
        self.assertEqual(IDENTITY.client_id, "oec-ha-0123456789abcdef")

    def test_base64url_without_padding(self) -> None:
        # MG 7.3 example of the guideline vectors.
        il = "provider.com/FieldDataService/FDS-001/1200-0345"
        app_id = "cHJvdmlkZXIuY29tL0ZpZWxkRGF0YVNlcnZpY2UvRkRTLTAwMS8xMjAwLTAzNDU"
        self.assertEqual(oec.encode_id(il), app_id)
        self.assertEqual(oec.decode_id(app_id), il)
        with self.assertRaises(oec.OecError):
            oec.decode_id(app_id + "=")

    def test_topics(self) -> None:
        il = "provider.com/FieldDataService/FDS-001/1200-0345"
        built = oec.topic("OTConnector", il, "Pub", "Data", il, "a/b+c")
        self.assertTrue(built.endswith("/Pub/Data/" + oec.encode_id(il) + "/a%2Fb%2Bc"))
        parsed = oec.parse_topic(built)
        self.assertEqual((parsed.service_type, parsed.method, parsed.resource), ("OTConnector", "Pub", "Data"))
        self.assertEqual((parsed.app_il, parsed.source_il, parsed.filter), (il, il, "a%2Fb%2Bc"))
        for bad in ("Oi4v1/OTConnector/x/Pub/MAM", "Oi4v2/Robot/" + oec.encode_id(il) + "/Pub/MAM",
                    "Oi4v2/OTConnector/" + oec.encode_id(il) + "/Push/MAM", "foo"):
            with self.assertRaises(oec.OecError):
                oec.parse_topic(bad)

    def test_message_ids_strictly_increase(self) -> None:
        ids = oec.MessageIds("ITConnector/x", CLOCK)
        first, second = ids.next(), ids.next()
        self.assertEqual(first, "1793982000500-ITConnector/x")
        self.assertEqual(second, "1793982000501-ITConnector/x")

    def test_parse_ignores_unknown_keys(self) -> None:
        message = oec.parse_message(json.dumps({
            "MessageId": "1-OTConnector/a/b/c/d", "MessageType": "ua-data",
            "PublisherId": "OTConnector/a/b/c/d", "ReplyTo": "", "Unknown": 1,
            "Messages": [{"DataSetWriterId": 10, "DataSetWriterName": "a/b/c/d",
                          "WriterGroupName": "", "Payload": {"X": 1}, "Extra": True},
                         "garbage", {"DataSetWriterId": True}],
        }))
        self.assertEqual(len(message.messages), 1)
        self.assertEqual(message.publisher_il, "a/b/c/d")
        for bad in (b"{", b"[]", b'{"MessageType":"ua-data"}'):
            with self.assertRaises(oec.OecError):
                oec.parse_message(bad)


class ApplicationTest(unittest.TestCase):
    def setUp(self) -> None:
        self.app = oec.Application(IDENTITY, CLOCK)

    def assert_own(self, out, resource: str, with_source: bool = True) -> dict:
        self.assertEqual(out.topic, oec.topic("ITConnector", IL, "Pub", resource, IL if with_source else None))
        body = json.loads(out.payload)
        self.assertEqual(body["PublisherId"], "ITConnector/" + IL)
        self.assertTrue(body["MessageId"].endswith("-ITConnector/" + IL))
        self.assertEqual(body["MessageType"], "ua-data")
        self.assertIn("ReplyTo", body)
        self.assertIsInstance(body["Messages"], list)
        return body

    def test_birth_will_close_heartbeat(self) -> None:
        birth = self.assert_own(self.app.birth(), "MAM")
        self.assertEqual(birth["DataSetClassId"], oec.CLASS_MAM)
        mam = birth["Messages"][0]
        self.assertEqual(mam["DataSetWriterName"], IL)
        self.assertNotIn("Timestamp", mam)
        self.assertEqual(mam["Payload"]["ProductInstanceUri"], IL)
        self.assertEqual(mam["Payload"]["DeviceClass"], "Oi4.ITConnector")
        self.assertEqual(mam["Payload"]["HardwareRevision"], "")
        for message, expected in ((self.app.will(), ("FAILURE_1", 0)), (self.app.close(), ("NORMAL_0", 0)),
                                  (self.app.heartbeat(), ("NORMAL_0", 100))):
            body = self.assert_own(message, "Health")
            payload = body["Messages"][0]["Payload"]
            self.assertEqual((payload["Health"], payload["HealthScore"]), expected)
            self.assertEqual(body["Messages"][0]["DataSetWriterId"], oec.DSW_HEALTH)
        self.assertIn("Timestamp", json.loads(self.app.heartbeat().payload)["Messages"][0])

    def test_get_with_and_without_source(self) -> None:
        for resource in oec.RESOURCES:
            for source in (None, IL):
                [answer] = self.app.handle_request(*get(resource, source))
                body = self.assert_own(answer, resource, source is not None)
                self.assertEqual(body["ReplyTo"], "1793982000000-" + REQUESTER)
                self.assertEqual(len(body["Messages"]), 1)
        [profile] = self.app.handle_request(*get("Profile"))
        self.assertEqual(json.loads(profile.payload)["Messages"][0]["Payload"],
                         {"Resources": ["MAM", "Health", "Profile", "Event"]})

    def assert_status(self, answers: list, symbol: str) -> None:
        [answer] = answers
        self.assertEqual(answer.topic, oec.topic("ITConnector", IL, "Pub", "Event", IL) + "/Status/Bad")
        body = json.loads(answer.payload)
        self.assertEqual(body["DataSetClassId"], oec.CLASS_EVENT)
        self.assertEqual(body["ReplyTo"], "1793982000000-" + REQUESTER)
        [event] = body["Messages"]
        self.assertEqual((event["DataSetWriterId"], event["WriterGroupName"], event["DataSetWriterName"]),
                         (oec.DSW_STATUS_BAD, "Status/Bad", IL))
        self.assertIn("Timestamp", event)
        self.assertEqual(event["Payload"]["Category"], "CAT_STATUS_1")
        self.assertEqual(event["Payload"]["Details"], {"SymbolicId": symbol})
        self.assertGreaterEqual(event["Payload"]["Number"], 0x80000000)

    def test_unknown_source_and_writes(self) -> None:
        other = "provider.com/FieldDataService/FDS-001/1200-0345"
        # REQ-CMP-047: no Pub, Status event BadNotFound under the AppId.
        self.assert_status(self.app.handle_request(*get("MAM", other)), "BadNotFound")
        # REQ-CMP-043, REQ-RES-EVENT-005: nothing is offered for modification.
        self.assert_status(self.app.handle_request(*get("Data", IL, method="Set", filter_="Tag")), "BadNotSupported")
        self.assert_status(self.app.handle_request(*get("SubscriptionList", IL, method="Del", filter_="Data/x")),
                           "BadNotSupported")
        self.assert_status(self.app.handle_request(*get("Data", other, method="Set", filter_="Tag")), "BadNotFound")

    def test_silence(self) -> None:
        self.assertEqual(self.app.handle_request(*get("Config")), [])  # not supported
        self.assertEqual(self.app.handle_request(*get("MAM", IL, filter_="x")), [])
        parsed, message = get("MAM")
        foreign = oec.parse_topic(str(parsed).replace("/ITConnector/", "/OTConnector/"))
        self.assertEqual(self.app.handle_request(foreign, message), [])

    def test_unknown_keys_and_locale(self) -> None:
        locale = {"DataSetWriterId": 3, "WriterGroupName": "Locale", "DataSetWriterName": "x",
                  "Payload": {"Locale": "de-DE"}}
        [answer] = self.app.handle_request(*get("MAM", None, [locale], Foo={"Bar": 1}))
        mam = json.loads(answer.payload)["Messages"][0]["Payload"]
        self.assertEqual(mam["Model"]["Locale"], "en-US")

    def test_pagination(self) -> None:
        def request(per_page: int, page: int) -> list[dict]:
            paging = {"DataSetWriterId": 1, "WriterGroupName": "PaginationRequest",
                      "DataSetWriterName": "x", "Payload": {"PerPage": per_page, "Page": page}}
            return [json.loads(o.payload) for o in self.app.handle_request(*get("Health", None, [paging]))]

        [page] = request(1, 0)
        health, pagination = page["Messages"]
        self.assertEqual(health["DataSetWriterId"], oec.DSW_HEALTH)
        self.assertEqual(pagination["DataSetWriterId"], oec.DSW_PAGINATION)
        self.assertEqual(pagination["WriterGroupName"], "Pagination")
        self.assertEqual(pagination["DataSetWriterName"], IL)
        self.assertEqual(pagination["Payload"], {"TotalCount": 1, "PerPage": 1, "Page": 1, "HasNext": False,
                                                 "PaginationId": page["MessageId"]})
        [beyond] = request(5, 3)
        [only] = beyond["Messages"]
        self.assertEqual(only["Payload"]["Page"], 3)
        self.assertFalse(only["Payload"]["HasNext"])
        self.assertEqual(only["Payload"]["PaginationId"], beyond["MessageId"])
        # Invalid PaginationRequest (PerPage 0) is answered unpaginated.
        [plain] = request(0, 1)
        self.assertEqual(len(plain["Messages"]), 1)

    def test_get_metadata_request(self) -> None:
        il = "oi4.org/OEC,20Micro,20ESP32,20DHT22/OECM-ESP32-DHT22/3C6105316C8C"
        out = self.app.get_metadata("OTConnector", il, il, "Climate", 19)
        self.assertEqual(out.topic, oec.topic("OTConnector", il, "Get", "Metadata", il, "Climate"))
        body = json.loads(out.payload)
        self.assertEqual(body["MessageType"], "ua-metadata")
        self.assertEqual((body["DataSetWriterId"], body["WriterGroupName"], body["DataSetWriterName"]),
                         (19, "Climate", il))
        self.assertEqual(body["MetaData"], {})


if __name__ == "__main__":
    unittest.main()
