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

package devicecommand

// Device commands on a gateway (FOG-01), following mgw-external-task-worker v1.2.2 and the mgw
// implementation of device-command: the command goes to command/{local device id}/{local
// service id} on the broker of the gateway as {"command_id","data"}, the connector answers on
// response/{device}/{service} with the same envelope, or with the error text on
// error/command/{command id}. Only the "data" segment travels.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

const (
	LocalResponseTopic = "response/#"
	LocalErrorTopic    = "error/command/#"
	localErrorPrefix   = "error/command/"
	localSegment       = "data"
)

type localEnvelope struct {
	CommandId string `json:"command_id"`
	Data      string `json:"data"`
}

// LocalTransport sends protocol messages to the device connectors of a gateway.
type LocalTransport struct {
	broker mqtt.Broker
	prefix string
}

func NewLocalTransport(broker mqtt.Broker, prefix string) *LocalTransport {
	return &LocalTransport{broker: broker, prefix: prefix}
}

// Produce publishes the data segment of a protocol message; the task id becomes the command id
func (this *LocalTransport) Produce(ctx context.Context, _ string, _ string, payload []byte) error {
	var msg ProtocolMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		return err
	}
	device, service := msg.Metadata.Device.LocalId, msg.Metadata.Service.LocalId
	if device == "" || service == "" {
		return fmt.Errorf("device %v or service %v has no local id", msg.Metadata.Device.Id, msg.Metadata.Service.Id)
	}
	envelope, err := json.Marshal(localEnvelope{CommandId: this.prefix + msg.TaskInfo.TaskId, Data: msg.Request.Input[localSegment]})
	if err != nil {
		return err
	}
	return this.broker.Publish(ctx, "command/"+device+"/"+service, 2, envelope)
}

// HandleLocalResponse completes the sub-task of an answer of a device connector. The protocol
// message is formed again from the stored sub-task, so that a restart loses no correlation.
func (this *Type) HandleLocalResponse(ctx context.Context, _ string, payload []byte) error {
	var envelope localEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil
	}
	subId, ok := strings.CutPrefix(envelope.CommandId, this.config.LocalCommandPrefix)
	if !ok || this.config.LocalCommandPrefix == "" {
		// an answer to device-command or to the cloud
		return nil
	}
	msg, found, err := this.localMessage(ctx, subId)
	if err != nil || !found {
		return err
	}
	msg.Response.Output = map[string]string{localSegment: envelope.Data}
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return this.HandleResponse(ctx, raw)
}

// HandleLocalError fails the task of a command the connector could not deliver.
func (this *Type) HandleLocalError(ctx context.Context, topic string, payload []byte) error {
	commandId := strings.TrimPrefix(topic, localErrorPrefix)
	subId, ok := strings.CutPrefix(commandId, this.config.LocalCommandPrefix)
	if !ok || this.config.LocalCommandPrefix == "" {
		return nil
	}
	_, err := this.completer.FailTask(ctx, jobIdOf(subId), "device command failed: "+strings.TrimSpace(string(payload)))
	return err
}

func (this *Type) localMessage(ctx context.Context, subId string) (msg ProtocolMsg, found bool, err error) {
	var subtask *store.Subtask
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		subtasks, err := tx.ListSubtasks(jobIdOf(subId))
		for _, s := range subtasks {
			if s.SubId == subId {
				subtask = &s
			}
		}
		return err
	})
	if err != nil || subtask == nil || subtask.HasResult {
		return msg, false, err
	}
	var command Command
	var info TaskInfo
	if err = json.Unmarshal(subtask.Command, &command); err != nil {
		return msg, false, err
	}
	if err = json.Unmarshal(subtask.TaskInfo, &info); err != nil {
		return msg, false, err
	}
	token, err := this.repo.GetToken(info.TenantId)
	if err != nil {
		return msg, false, err
	}
	built, _, err := this.message(command, info, token)
	if err != nil || built == nil {
		return msg, false, err
	}
	return *built, true, nil
}

// LastValues reads the last values of event services from mgw-last-value on a gateway, by
// local ids; the column is the path below the root content variable. A value never received
// is an error, as mgw-external-task-worker treats it.
type LastValues struct {
	url string
}

func NewLastValues(url string) *LastValues {
	return &LastValues{url: url}
}

type lastValueRequest struct {
	DeviceId   string
	ServiceId  string
	ColumnName string
}

func (this *LastValues) Query(token string, request []TimescaleRequest, timeout time.Duration) ([]TimescaleResponse, error) {
	body := []lastValueRequest{}
	for _, r := range request {
		_, column, _ := strings.Cut(r.ColumnName, ".")
		body = append(body, lastValueRequest{DeviceId: r.Device.LocalId, ServiceId: r.Service.LocalId, ColumnName: column})
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, this.url+"/last-values", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		temp, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, errors.New(strings.TrimSpace(string(temp)))
	}
	var result []TimescaleResponse
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	for i, r := range result {
		if r.Time == nil && i < len(request) {
			return nil, fmt.Errorf("no value of device %v service %v received yet", request[i].Device.LocalId, request[i].Service.LocalId)
		}
	}
	return result, nil
}
