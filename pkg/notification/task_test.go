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

package notification_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/notification"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// a notifier behind the cloud-proxy of a gateway: while the cloud is away, the connection closes
// without an answer
type cloudNotifier struct {
	offline  atomic.Bool
	mu       sync.Mutex
	received []map[string]string
}

func (this *cloudNotifier) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if this.offline.Load() {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	this.mu.Lock()
	this.received = append(this.received, body)
	this.mu.Unlock()
}

func (this *cloudNotifier) messages() []map[string]string {
	this.mu.Lock()
	defer this.mu.Unlock()
	return append([]map[string]string{}, this.received...)
}

func TestAProcessNotificationOnAGatewayWithoutCloudDoesNotFailTheProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	notifier := &cloudNotifier{}
	notifier.offline.Store(true)
	notifierServer := httptest.NewServer(notifier)
	t.Cleanup(notifierServer.Close)
	var developerCopies atomic.Int32
	developers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { developerCopies.Add(1) }))
	t.Cleanup(developers.Close)

	config := configuration.Default()
	config.SqlitePath = storetest.MemoryDsn()
	config.Mode, config.MqttBroker = "fog", "memory"
	config.NotifierUrl, config.DeveloperNotificationsUrl = notifierServer.URL, developers.URL
	srv, err := server.New(ctx, config, slog.New(slog.NewTextHandler(io.Discard, nil)), server.Dependencies{Broker: mqtt.NewMemory()})
	if err != nil {
		t.Fatal(err)
	}
	srv.Start(ctx)
	c := srv.Controller
	alice := client.TestToken("alice")

	xml := b.Process(
		b.Start("start"),
		b.Service("notify", "notification", b.InText("title", "hot"), b.InText("message", "it is hot")),
		b.End("end"), b.Flow("start", "notify"), b.Flow("notify", "end"),
	)
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: xml})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for inst.State == model.StateRunning || inst.State == model.StatePending {
		if time.Now().After(deadline) {
			t.Fatalf("still %v", inst.State)
		}
		time.Sleep(50 * time.Millisecond)
		if inst, err = c.GetInstance(ctx, alice, "processes", inst.Id); err != nil {
			t.Fatal(err)
		}
	}
	if inst.State != model.StateCompleted {
		t.Fatalf("a notifier without answer does not stop the process (FOG-05): %v %q", inst.State, inst.EndReason)
	}

	notifier.offline.Store(false)
	deadline = time.Now().Add(15 * time.Second)
	for len(notifier.messages()) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	// a second delivery would show up within the next pass
	time.Sleep(2 * time.Second)
	sent := notifier.messages()
	if len(sent) != 1 || sent[0]["userId"] != "alice" || sent[0]["title"] != "hot" || sent[0]["message"] != "it is hot" || sent[0]["topic"] != notification.Topic {
		t.Fatalf("the notification is delivered once when the cloud is back: %v", sent)
	}
	if n := developerCopies.Load(); n != 0 {
		t.Errorf("a notification of a process gets no copy to developer-notifications, as before: %v", n)
	}
}

func TestANotificationTaskQueuesOncePerJob(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		task := notification.NewTask(s)
		job := engine.Task{JobId: "job-1", Owner: "alice", Namespace: "processes", Input: map[string]any{"title": "hot", "message": 21}}
		for range 2 {
			// the second execution is the one after a completion that did not reach the database
			result, err := task.Execute(ctx, job)
			if err != nil || result.Output == nil || result.Async {
				t.Fatalf("the task completes once queued: %+v %v", result, err)
			}
		}
		var due []store.Notification
		if err := s.Tx(ctx, func(tx *store.Tx) (err error) {
			due, err = tx.DueNotifications(time.Now().Add(time.Hour), 10)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if len(due) != 1 || due[0].UserId != "alice" || due[0].Title != "hot" || due[0].Message != "21" || due[0].Topic != notification.Topic || due[0].Namespace != "processes" {
			t.Fatalf("one queued notification per job: %+v", due)
		}

		_, err := task.Execute(ctx, engine.Task{JobId: "job-2", Owner: "alice", Input: map[string]any{}})
		var permanent *engine.PermanentError
		if !errors.As(err, &permanent) {
			t.Fatalf("a notification without title and message is a permanent error: %v", err)
		}
	})
}
