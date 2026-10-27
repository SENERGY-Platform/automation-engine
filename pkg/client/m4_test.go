/*
 * Copyright 2026 InfAI (CC SES)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package client_test

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/api"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestDebugStateShowsWaitsAndCutsLargeValues(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: b.Process(
			b.Message("m", "go"),
			b.Start("start"), b.Script("init", "big", "'x'.repeat(10000)"), b.CatchMessage("wait", "m"), b.End("end"),
			b.Flow("start", "init"), b.Flow("init", "wait"), b.Flow("wait", "end"),
		)})
		if err != nil {
			t.Fatal(err)
		}
		inst, err := c.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id})
		if err != nil {
			t.Fatal(err)
		}
		state, err := c.DebugState(ctx, alice, ns, inst.Id, model.DebugOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(state.Active) != 1 || state.Active[0].ElementId != "wait" || len(state.Waits) != 1 || state.Waits[0].Kind != model.WaitMessage {
			t.Fatalf("the instance waits for the message (DBG-02): %+v %+v", state.Active, state.Waits)
		}
		big := state.Variables["big"]
		if !big.Truncated || big.Size != 10002 || len(big.Value.(string)) >= 10000 {
			t.Fatalf("large values are cut for transfer (DBG-04): truncated %v size %v", big.Truncated, big.Size)
		}
		full, err := c.GetVariable(ctx, alice, ns, inst.Id, "big", "")
		if err != nil || len(full.Value.(string)) != 10000 {
			t.Fatalf("a cut value loads on its own: %v", err)
		}
		if _, err = c.DebugState(ctx, bob, ns, inst.Id, model.DebugOptions{}); !errors.Is(err, model.ErrNotFound) && !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("debugging needs the read right (DBG-08), got %v", err)
		}
		waitingSeq := state.Seq

		if _, err = c.CorrelateMessage(ctx, alice, model.MessageInput{Name: "go", All: true}); err != nil {
			t.Fatal(err)
		}
		finished(t, c, ns, inst.Id)
		latest, err := c.HistoryState(ctx, alice, ns, inst.Id, model.HistoryOptions{})
		if err != nil || latest.Instance.State != model.StateCompleted || len(latest.Active) != 0 || !latest.Reconstructed {
			t.Fatalf("latest step: %+v %v", latest, err)
		}
		step, err := c.HistoryState(ctx, alice, ns, inst.Id, model.HistoryOptions{Seq: waitingSeq})
		if err != nil {
			t.Fatal(err)
		}
		if step.Seq != waitingSeq || len(step.Active) != 1 || step.Active[0].ElementId != "wait" || step.Instance.State != model.StateRunning || step.FirstSeq != 1 {
			t.Fatalf("the waiting step is reconstructed (DBG-13): %+v", step)
		}
		value, err := c.HistoryVariable(ctx, alice, ns, inst.Id, "big", "", model.HistoryOptions{Seq: waitingSeq})
		if err != nil || !value.Available || !strings.HasPrefix(value.Value.(string), "xxx") || len(value.Value.(string)) != 10000 {
			t.Fatalf("the snapshot holds the complete value (DBG-14): available %v, %v", value.Available, err)
		}
		if _, err = c.HistoryState(ctx, alice, ns, inst.Id, model.HistoryOptions{Seq: 0, Time: &inst.CreatedAt}); err != nil && !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("a time selects the step at or before it: %v", err)
		}
	})
}

func waitingDeployment(t *testing.T, c client.Client) model.Deployment {
	t.Helper()
	d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: b.Process(
		b.Message("m", "go"),
		b.Start("start"), b.Script("init", "n", "1"), b.CatchMessage("wait", "m", b.OutScript("received", "payload")), b.End("end"),
		b.Flow("start", "init"), b.Flow("init", "wait"), b.Flow("wait", "end"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func aliceToken() (string, error) {
	return alice, nil
}

func TestLiveStreamSendsSnapshotDeltasAndEndState(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		streamCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		d := waitingDeployment(t, c)
		inst, err := c.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.Watch(streamCtx, bob, ns, inst.Id, model.WatchOptions{}); !errors.Is(err, model.ErrNotFound) && !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("live debugging needs the read right (DBG-08), got %v", err)
		}
		f, err := client.Follow(streamCtx, c, aliceToken, ns, inst.Id, model.WatchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		first, err := f.Next(streamCtx)
		if err != nil || first.Type != model.DebugSnapshot || len(f.State().Active) != 1 || f.State().Active[0].ElementId != "wait" {
			t.Fatalf("a stream starts with the snapshot (DBG-01): %+v %v", first, err)
		}
		waitingSeq := first.Seq
		if _, err = c.CorrelateMessage(ctx, alice, model.MessageInput{Name: "go", All: true, LocalVariables: map[string]any{"payload": "hello"}}); err != nil {
			t.Fatal(err)
		}
		var kinds []model.ActivityKind
		for {
			message, err := f.Next(streamCtx)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if message.Type == model.DebugDelta && message.Seq <= waitingSeq {
				t.Fatalf("sequence numbers rise: %v after %v", message.Seq, waitingSeq)
			}
			for _, a := range message.Activities {
				kinds = append(kinds, a.Kind)
			}
		}
		state := f.State()
		if state.Instance.State != model.StateCompleted || len(state.Active) != 0 || state.Variables["received"].Value != "hello" {
			t.Fatalf("the end state closes the stream: %+v", state)
		}
		if !slices.Contains(kinds, model.ActivityMessageReceived) || !slices.Contains(kinds, model.ActivityRunEnded) {
			t.Fatalf("deltas carry the activity entries (DBG-03): %v", kinds)
		}

		resumed, err := c.Watch(streamCtx, alice, ns, inst.Id, model.WatchOptions{After: waitingSeq})
		if err != nil {
			t.Fatal(err)
		}
		message := <-resumed
		if message.Type != model.DebugDelta || len(message.Activities) == 0 || message.Activities[0].Seq != waitingSeq+1 {
			t.Fatalf("a resume continues after the given sequence instead of a snapshot: %+v", message)
		}
		if end := <-resumed; end.Type != model.DebugEnd {
			t.Fatalf("a finished instance ends the stream: %+v", end)
		}
	})
}

func TestLiveStreamsArePerUserLimited(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		streamCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		d := waitingDeployment(t, c)
		inst, err := c.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id})
		if err != nil {
			t.Fatal(err)
		}
		for i := range 5 {
			if _, err = c.Watch(streamCtx, alice, ns, inst.Id, model.WatchOptions{}); err != nil {
				t.Fatalf("stream %v: %v", i, err)
			}
		}
		if _, err = c.Watch(streamCtx, alice, ns, inst.Id, model.WatchOptions{}); !errors.Is(err, model.ErrTooMany) {
			t.Fatalf("connections per user are limited (DBG-10), got %v", err)
		}
	})
}

func TestBrowsersSendTheTokenPastKongOnlyIfTheEngineVerifiesIt(t *testing.T) {
	streamCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	config := configuration.Default()
	config.MinTimerDuration = "0s"
	config.SqlitePath = storetest.MemoryDsn()
	config.PollInterval = "50ms"
	srv, err := server.New(streamCtx, config, slog.New(slog.NewTextHandler(io.Discard, nil)), server.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	srv.Start(streamCtx)
	d := waitingDeployment(t, srv.Controller)
	inst, err := srv.Controller.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	dial := func(settings api.Settings, options *websocket.DialOptions) *websocket.Conn {
		httpServer := httptest.NewServer(api.New(srv.Controller, srv.Store, slog.New(slog.NewTextHandler(io.Discard, nil)), settings))
		t.Cleanup(httpServer.Close)
		conn, _, err := websocket.Dial(streamCtx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/namespaces/"+ns+"/instances/"+inst.Id+"/live", options)
		if err != nil {
			t.Fatal(err)
		}
		conn.SetReadLimit(1 << 24)
		return conn
	}

	unverified := dial(api.Settings{}, nil)
	defer unverified.CloseNow()
	var message model.DebugMessage
	if err = wsjson.Read(streamCtx, unverified, &message); websocket.CloseStatus(err) != 4401 {
		t.Fatalf("without own signature checks the token must come through Kong in the header (DBG-08), got %v", err)
	}

	// stands in for the signature check against Keycloak: only the token of alice is signed
	verify := func(token string) error {
		if token != alice {
			return errors.New("signature is invalid")
		}
		return nil
	}
	verified := dial(api.Settings{VerifyToken: verify}, nil)
	defer verified.CloseNow()
	if err = wsjson.Write(streamCtx, verified, api.AuthMessage{Type: "auth", Token: strings.TrimPrefix(alice, "Bearer ")}); err != nil {
		t.Fatal(err)
	}
	if err = wsjson.Read(streamCtx, verified, &message); err != nil || message.Type != model.DebugSnapshot {
		t.Fatalf("the first message authenticates the stream: %+v %v", message, err)
	}

	forged := dial(api.Settings{VerifyToken: verify}, nil)
	defer forged.CloseNow()
	if err = wsjson.Write(streamCtx, forged, api.AuthMessage{Type: "auth", Token: client.TestToken("mallory")}); err != nil {
		t.Fatal(err)
	}
	if err = wsjson.Read(streamCtx, forged, &message); websocket.CloseStatus(err) != 4401 {
		t.Fatalf("an unsigned first-message token is refused, got %+v %v", message, err)
	}

	// the stream verifies a token in the header as well, also when the other requests trust Kong
	header := dial(api.Settings{VerifyToken: verify}, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {client.TestToken("mallory")}}})
	defer header.CloseNow()
	if err = wsjson.Read(streamCtx, header, &message); websocket.CloseStatus(err) != 4401 {
		t.Fatalf("an unsigned header token is refused by the live stream, got %+v %v", message, err)
	}

	// browsers offer the token as subprotocol next to "bearer"; the answer names only "bearer"
	subprotocol := dial(api.Settings{VerifyToken: verify}, &websocket.DialOptions{Subprotocols: []string{api.BearerProtocol, strings.TrimPrefix(alice, "Bearer ")}})
	defer subprotocol.CloseNow()
	if subprotocol.Subprotocol() != api.BearerProtocol {
		t.Fatalf("the engine answers with the bearer subprotocol, got %q", subprotocol.Subprotocol())
	}
	if err = wsjson.Read(streamCtx, subprotocol, &message); err != nil || message.Type != model.DebugSnapshot {
		t.Fatalf("the subprotocol token authenticates the stream: %+v %v", message, err)
	}

	forgedProtocol := dial(api.Settings{VerifyToken: verify}, &websocket.DialOptions{Subprotocols: []string{api.BearerProtocol, strings.TrimPrefix(client.TestToken("mallory"), "Bearer ")}})
	defer forgedProtocol.CloseNow()
	if err = wsjson.Read(streamCtx, forgedProtocol, &message); websocket.CloseStatus(err) != 4401 {
		t.Fatalf("an unsigned subprotocol token is refused, got %+v %v", message, err)
	}

	// without own signature checks a subprotocol token has not passed Kong and does not count
	unverifiedProtocol := dial(api.Settings{}, &websocket.DialOptions{Subprotocols: []string{api.BearerProtocol, strings.TrimPrefix(alice, "Bearer ")}})
	defer unverifiedProtocol.CloseNow()
	if err = wsjson.Read(streamCtx, unverifiedProtocol, &message); websocket.CloseStatus(err) != 4401 {
		t.Fatalf("without own signature checks the subprotocol token is ignored, got %+v %v", message, err)
	}
}

func TestTheLiveStreamPingsSoThatProxiesKeepAQuietStreamOpen(t *testing.T) {
	streamCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	config := configuration.Default()
	config.MinTimerDuration = "0s"
	config.SqlitePath = storetest.MemoryDsn()
	config.PollInterval = "50ms"
	srv, err := server.New(streamCtx, config, slog.New(slog.NewTextHandler(io.Discard, nil)), server.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	srv.Start(streamCtx)
	d := waitingDeployment(t, srv.Controller)
	inst, err := srv.Controller.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	settings := api.Settings{LivePingPeriod: 50 * time.Millisecond}
	httpServer := httptest.NewServer(api.New(srv.Controller, srv.Store, slog.New(slog.NewTextHandler(io.Discard, nil)), settings))
	defer httpServer.Close()

	// raw frames, because a WebSocket client library answers pings without showing them
	conn, err := net.Dial("tcp", strings.TrimPrefix(httpServer.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	request := "GET /namespaces/" + ns + "/instances/" + inst.Id + "/live HTTP/1.1\r\nHost: engine\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n" +
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nAuthorization: " + alice + "\r\n\r\n"
	if _, err = conn.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil || response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v", response, err)
	}
	// the instance waits, so after the snapshot only pings come (opcode 9)
	for i := 0; i < 3; i++ {
		opcode := readFrame(t, reader)
		if opcode == 9 {
			return
		}
	}
	t.Fatal("no ping on a quiet live stream")
}

// readFrame reads one unmasked server frame and returns its opcode
func readFrame(t *testing.T, reader *bufio.Reader) byte {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		t.Fatal(err)
	}
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(reader, extended); err != nil {
			t.Fatal(err)
		}
		length = uint64(binary.BigEndian.Uint16(extended))
	case 127:
		extended := make([]byte, 8)
		if _, err := io.ReadFull(reader, extended); err != nil {
			t.Fatal(err)
		}
		length = binary.BigEndian.Uint64(extended)
	}
	if _, err := io.CopyN(io.Discard, reader, int64(length)); err != nil {
		t.Fatal(err)
	}
	return header[0] & 0x0f
}
