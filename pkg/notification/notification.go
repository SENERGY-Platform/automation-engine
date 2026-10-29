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

// Package notification sends user notifications through the notifier and copies them to
// developer-notifications, in the format process-incident-api uses today (INC-02), and is the
// task type "notification" of processes (Task, BPMN-09).
package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	devnotifications "github.com/SENERGY-Platform/developer-notifications/pkg/client"
)

type Notifier struct {
	notifierUrl string
	developer   devnotifications.Client
	http        *http.Client
	logger      *slog.Logger
}

// New creates a notifier; an empty URL disables the respective target.
func New(notifierUrl string, developerNotificationsUrl string, logger *slog.Logger) *Notifier {
	result := &Notifier{notifierUrl: notifierUrl, http: &http.Client{Timeout: 10 * time.Second}, logger: logger}
	if developerNotificationsUrl != "" {
		result.developer = devnotifications.New(developerNotificationsUrl)
	}
	return result
}

type message struct {
	UserId  string `json:"userId"`
	Title   string `json:"title"`
	Message string `json:"message"`
	Topic   string `json:"topic"`
}

// Notify returns an error if the notifier did not accept the message, so that it is retried.
// developer-notifications gets a copy, best effort.
func (this *Notifier) Notify(ctx context.Context, n store.Notification) error {
	if err := this.Send(ctx, n); err != nil {
		return err
	}
	return this.copyToDevelopers(n)
}

// Send delivers to the notifier only, e.g. for notifications a process sends (BPMN-09).
func (this *Notifier) Send(ctx context.Context, n store.Notification) error {
	if this.notifierUrl != "" {
		body, err := json.Marshal(message{UserId: n.UserId, Title: n.Title, Message: n.Message, Topic: n.Topic})
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, this.notifierUrl+"/notifications?ignore_duplicates_within_seconds=3600", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if err = tracing.Inject(ctx, req); err != nil {
			return err
		}
		resp, err := this.http.Do(req)
		if err != nil {
			// no answer at all: the engine postpones the other due notifications as well
			return &engine.NotifierUnreachableError{Err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			text, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			return fmt.Errorf("notifier answered %v: %s", resp.Status, text)
		}
	}
	return nil
}

// copyToDevelopers names the namespace as a tag and in the body, so that a process and a smart
// service of the same name can be told apart
func (this *Notifier) copyToDevelopers(n store.Notification) error {
	if this.developer != nil {
		tags := []string{"process-incident", "user-notification", n.UserId}
		body := fmt.Sprintf("Notification For %v\n", n.UserId)
		if n.Namespace != "" {
			tags = append(tags, n.Namespace)
			body += fmt.Sprintf("Namespace: %v\n", n.Namespace)
		}
		err := this.developer.SendMessage(devnotifications.Message{
			Sender: "github.com/SENERGY-Platform/automation-engine",
			Title:  "Process-Incident-User-Notification",
			Tags:   tags,
			Body:   body + fmt.Sprintf("Title: %v\nMessage: %v\n", n.Title, n.Message),
		})
		if err != nil {
			this.logger.Warn("unable to send developer notification", "error", err)
		}
	}
	return nil
}
