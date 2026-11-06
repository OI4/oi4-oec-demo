// Package demotest is the test harness of the demo services (ADR-0028 §6):
// the in-process TLS test broker of the Rust SDK run as a program
// (go-testbroker, C-076), container storages, the demo binary as a
// process, OEC messages for injection and the D7 runner. Offline and
// without Docker: everything runs on loopback.
//
// Tools come from the environment, with defaults below oec/:
//
//	OEC_TESTBROKER  go-testbroker (sdk/rust/target/debug/examples/go-testbroker)
//	OEC_D7          oec-check (no default: built by oec/demos/check.sh)
//	OEC_D8          oec-infra (no default: built by oec/demos/check.sh)
//
// A missing tool skips the test, unless OEC_DEMOS_REQUIRE=1 (set by
// oec/demos/check.sh), then it fails.
package demotest

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Wait is the upper bound for every wait of a test.
const Wait = 15 * time.Second

// Oec is the directory oec/ of the repository.
func Oec() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

// Tool is the path of a helper program: the environment variable env or
// def (relative to oec/, "" = none). Missing, it skips t – or fails it
// with OEC_DEMOS_REQUIRE=1.
func Tool(t *testing.T, env, def, hint string) string {
	t.Helper()
	path := os.Getenv(env)
	if path == "" && def != "" {
		path = filepath.Join(Oec(), def)
	}
	var err error
	if path == "" {
		err = fmt.Errorf("%s is not set", env)
	} else {
		_, err = os.Stat(path)
	}
	if err != nil {
		if os.Getenv("OEC_DEMOS_REQUIRE") == "1" {
			t.Fatalf("%s: %v (%s)", env, err, hint)
		}
		t.Skipf("%s: %v (%s)", env, err, hint)
	}
	return path
}

// Seen is one line of the test broker: what it saw on a connection.
type Seen struct {
	Seen     string   `json:"seen"`
	Conn     int      `json:"conn"`
	ClientID string   `json:"client_id"`
	Username *string  `json:"username"`
	Will     *string  `json:"will"`
	Topic    string   `json:"topic"`
	Payload  string   `json:"payload"`
	Retain   bool     `json:"retain"`
	Filters  []string `json:"filters"`
}

func (s Seen) String() string {
	switch s.Seen {
	case "publish":
		return fmt.Sprintf("%d publish %s %s", s.Conn, s.Topic, s.Payload)
	case "subscribe", "unsubscribe":
		return fmt.Sprintf("%d %s %v", s.Conn, s.Seen, s.Filters)
	default:
		return fmt.Sprintf("%d %s", s.Conn, s.Seen)
	}
}

// Broker is a running test broker (a routing broker: every publication
// goes to the matching subscribers).
type Broker struct {
	Port int    `json:"port"`
	CA   string `json:"ca"`

	stdin io.WriteCloser
	lines *lines[Seen]
}

// StartBroker starts a test broker for t; it ends with t.
func StartBroker(t *testing.T) *Broker {
	t.Helper()
	path := Tool(t, "OEC_TESTBROKER", "sdk/rust/target/debug/examples/go-testbroker",
		"cargo build -p oec-ffi --example go-testbroker")
	cmd := exec.Command(path, t.TempDir())
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	scan := bufio.NewScanner(stdout)
	scan.Buffer(make([]byte, 1<<20), 1<<24)
	b := &Broker{stdin: stdin}
	if !scan.Scan() {
		t.Fatalf("test broker: no first line: %v", scan.Err())
	}
	if err := json.Unmarshal(scan.Bytes(), b); err != nil {
		t.Fatalf("test broker: %v: %s", err, scan.Text())
	}
	b.lines = readLines(scan, func(line []byte) Seen {
		var s Seen
		if err := json.Unmarshal(line, &s); err != nil {
			panic(fmt.Sprintf("test broker: %v: %s", err, line))
		}
		return s
	})
	t.Cleanup(func() {
		stdin.Close()
		<-b.lines.done // Wait closes stdout
		_ = cmd.Wait()
	})
	return b
}

// Expect returns the first event from index from on that matches, with
// its index; it fails t after Wait.
func (b *Broker) Expect(t *testing.T, from int, what string, match func(Seen) bool) (Seen, int) {
	t.Helper()
	return b.lines.expect(t, from, "test broker: "+what, match)
}

// Publish waits for a PUBLISH on topic.
func (b *Broker) Publish(t *testing.T, from int, topic string) (Seen, int) {
	t.Helper()
	return b.Expect(t, from, "PUBLISH on "+topic, func(s Seen) bool {
		return s.Seen == "publish" && s.Topic == topic
	})
}

// Len is the number of events so far.
func (b *Broker) Len() int { return b.lines.len() }

// Events is a copy of the events so far.
func (b *Broker) Events() []Seen { return b.lines.all() }

// Send sends a PUBLISH to the client of connection conn.
func (b *Broker) Send(t *testing.T, conn int, topic, payload string) {
	t.Helper()
	line, _ := json.Marshal(map[string]any{"cmd": "send", "conn": conn, "topic": topic, "payload": payload})
	if _, err := b.stdin.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
}

// Container writes a container storage (REQ-CNT-004) for the broker below
// a new temporary directory: the hostname (the MQTT client id and the
// SerialNumber), broker.json, the CA and the credentials user/pa:ss.
func (b *Broker) Container(t *testing.T, hostname string) string {
	t.Helper()
	root := t.TempDir()
	ca, err := os.ReadFile(b.CA)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"etc/hostname":                hostname + "\n",
		"etc/oi4/mqtt/broker.json":    fmt.Sprintf(`{"Address": "localhost", "SecurePort": %d, "MaxPacketSize": 262144}`, b.Port),
		"etc/oi4/certs/ca.pem":        string(ca),
		"run/secrets/oi4/credentials": "dXNlcjpwYTpzcw==\n", // base64("user:pa:ss")
	} {
		Write(t, filepath.Join(root, name), content)
	}
	if err := os.MkdirAll(filepath.Join(root, "etc", "oi4", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// Write writes a file and its directories.
func Write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Eventually polls cond until it holds; it fails t after Wait.
func Eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(Wait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("not within %v: %s", Wait, what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// lines collects the lines of a program's output for waiting.
type lines[T any] struct {
	mu    sync.Mutex
	cond  *sync.Cond
	items []T
	ended bool
	done  chan struct{}
}

func readLines[T any](scan *bufio.Scanner, parse func([]byte) T) *lines[T] {
	l := &lines[T]{done: make(chan struct{})}
	l.cond = sync.NewCond(&l.mu)
	go func() {
		defer close(l.done)
		for scan.Scan() {
			item := parse(scan.Bytes())
			l.mu.Lock()
			l.items = append(l.items, item)
			l.cond.Broadcast()
			l.mu.Unlock()
		}
		l.mu.Lock()
		l.ended = true
		l.cond.Broadcast()
		l.mu.Unlock()
	}()
	return l
}

func (l *lines[T]) expect(t *testing.T, from int, what string, match func(T) bool) (T, int) {
	t.Helper()
	deadline := time.Now().Add(Wait)
	timer := time.AfterFunc(Wait, func() {
		l.mu.Lock()
		l.cond.Broadcast()
		l.mu.Unlock()
	})
	defer timer.Stop()
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := from; ; {
		for ; i < len(l.items); i++ {
			if match(l.items[i]) {
				return l.items[i], i
			}
		}
		if l.ended || time.Now().After(deadline) {
			var all []string
			for _, e := range l.items {
				all = append(all, fmt.Sprint(e))
			}
			t.Fatalf("no %s within %v; seen:\n%s", what, Wait, strings.Join(all, "\n"))
		}
		l.cond.Wait()
	}
}

func (l *lines[T]) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.items)
}

func (l *lines[T]) all() []T {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]T(nil), l.items...)
}

// Topic is the topic level of an Identification Link (base64url without
// padding, REQ-TOP-019).
func Topic(il string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(il))
}

// DataSetClassIds of the resources (MG 9.1).
var classID = map[string]string{
	"Health": "d8e7b6df-42ba-448a-975a-199f59e8ffeb",
	"Data":   "1badfd0b-5a3c-4e8c-9fed-ea47fe25ce67",
	"Event":  "543ae05e-b6d9-4161-a0a3-350a0fac5976",
}

var pubSeq atomic.Int64

// Pub is a self-initiated publication of the application serviceType/app
// on resource Health, Data or Event of source (filter "" = none) with
// payload: its topic and NetworkMessage.
func Pub(serviceType, app, resource, source, filter string, payload any) (string, string) {
	seq := pubSeq.Add(1)
	topic := "Oi4v2/" + serviceType + "/" + Topic(app) + "/Pub/" + resource + "/" + Topic(source)
	if filter != "" {
		topic += "/" + filter
	}
	publisher := serviceType + "/" + app
	msg, _ := json.Marshal(map[string]any{
		"MessageId":      fmt.Sprintf("%d-%s", time.Now().UnixMilli(), publisher),
		"MessageType":    "ua-data",
		"PublisherId":    publisher,
		"DataSetClassId": classID[resource],
		"ReplyTo":        "",
		"Messages": []any{map[string]any{
			"DataSetWriterId":   100 + seq%1000,
			"WriterGroupName":   filter,
			"DataSetWriterName": source,
			"SequenceNumber":    seq,
			"Timestamp":         time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
			"Payload":           payload,
		}},
	})
	return topic, string(msg)
}

// AsJSON decodes a payload.
func AsJSON(t *testing.T, payload string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(payload), &v); err != nil {
		t.Fatalf("%v: %s", err, payload)
	}
	return v
}

var requestSeq atomic.Int64

// Request sends a Set request answering the publication pub with payload
// to connection conn: its first DataSetMessage (WriterGroupName,
// DataSetWriterId of the provider) with the new Payload; it returns the
// MessageId.
func (b *Broker) Request(t *testing.T, conn int, topic string, pub map[string]any, payload any) string {
	t.Helper()
	id := fmt.Sprintf("%d-Aggregation/vendor.com/Client/C-1/1", 1700000000000+requestSeq.Add(1))
	dsm := pub["Messages"].([]any)[0].(map[string]any)
	msg := map[string]any{}
	for k, v := range dsm {
		msg[k] = v
	}
	msg["Payload"] = payload
	req, _ := json.Marshal(map[string]any{
		"MessageId":      id,
		"MessageType":    "ua-data",
		"PublisherId":    "Aggregation/vendor.com/Client/C-1/1",
		"DataSetClassId": pub["DataSetClassId"],
		"Messages":       []any{msg},
	})
	b.Send(t, conn, topic, string(req))
	return id
}

// Get sends a Get of the resource without helper DataSets (MG B1.1.2) to
// connection conn and returns the MessageId.
func (b *Broker) Get(t *testing.T, conn int, topic, resource string) string {
	t.Helper()
	id := fmt.Sprintf("%d-Aggregation/vendor.com/Client/C-1/1", 1700000000000+requestSeq.Add(1))
	req, _ := json.Marshal(map[string]any{
		"MessageId":      id,
		"MessageType":    "ua-data",
		"PublisherId":    "Aggregation/vendor.com/Client/C-1/1",
		"DataSetClassId": classID[resource],
		"ReplyTo":        "",
		"Messages":       []any{},
	})
	b.Send(t, conn, topic, string(req))
	return id
}

// Answer waits for the publication on topic answering the request id.
func (b *Broker) Answer(t *testing.T, from int, topic, id string) (map[string]any, int) {
	t.Helper()
	s, i := b.Expect(t, from, "answer to "+id+" on "+topic, func(s Seen) bool {
		return s.Seen == "publish" && s.Topic == topic && strings.Contains(s.Payload, `"ReplyTo":"`+id+`"`)
	})
	return AsJSON(t, s.Payload), i
}

// EventNumber is the Number of the first DataSetMessage of the Event ev.
func EventNumber(t *testing.T, ev map[string]any) uint32 {
	t.Helper()
	n, ok := ev["Messages"].([]any)[0].(map[string]any)["Payload"].(map[string]any)["Number"].(float64)
	if !ok {
		t.Fatalf("no Number in %v", ev)
	}
	return uint32(n)
}
