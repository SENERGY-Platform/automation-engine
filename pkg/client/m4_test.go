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
	"context"
	"errors"
	"io"
	"log/slog"
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

func TestBrowsersSendTheTokenAsFirstMessageOnlyIfTheEngineVerifiesIt(t *testing.T) {
	streamCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	config := configuration.Default()
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
	dial := func(settings api.Settings) *websocket.Conn {
		httpServer := httptest.NewServer(api.New(srv.Controller, srv.Store, slog.New(slog.NewTextHandler(io.Discard, nil)), settings))
		t.Cleanup(httpServer.Close)
		conn, _, err := websocket.Dial(streamCtx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/namespaces/"+ns+"/instances/"+inst.Id+"/live", nil)
		if err != nil {
			t.Fatal(err)
		}
		conn.SetReadLimit(1 << 24)
		return conn
	}

	unverified := dial(api.Settings{})
	defer unverified.CloseNow()
	var message model.DebugMessage
	if err = wsjson.Read(streamCtx, unverified, &message); websocket.CloseStatus(err) != 4401 {
		t.Fatalf("without own signature checks the token must come through Kong in the header (DBG-08), got %v", err)
	}

	verified := dial(api.Settings{VerifiesTokens: true})
	defer verified.CloseNow()
	if err = wsjson.Write(streamCtx, verified, api.AuthMessage{Type: "auth", Token: strings.TrimPrefix(alice, "Bearer ")}); err != nil {
		t.Fatal(err)
	}
	if err = wsjson.Read(streamCtx, verified, &message); err != nil || message.Type != model.DebugSnapshot {
		t.Fatalf("the first message authenticates the stream: %+v %v", message, err)
	}
}
