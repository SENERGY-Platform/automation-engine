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

// Command devicesim stands in for a protocol handler and its devices in the local Docker Compose
// setup. It reads the device commands the engine sends to the Kafka topic of a protocol handler
// and answers every one on the topic the command names (metadata.response_to), as
// platform-connector does once the device answered. At a fixed interval it also sends an event
// of every device in device-repository for each of its services with the interaction event or
// event+request, to the device data topic of the service, as platform-connector does when a
// device reports a value.
//
// Answer and event follow the outputs of the service, the answer by protocol segment, the event
// by the name of the output variable. Values are the value of a variable if the device type
// fixes one, the current number for numbers, true for booleans and "simulated" for strings. The
// number swings around NUMBER by AMPLITUDE over PERIOD, so that event conditions turn true and
// false again. A service without outputs, a setter, gets an empty answer. Inputs are logged,
// not stored.
//
// Environment: KAFKA_URL (default kafka:9092), TOPICS (comma separated, default connector),
// GROUP (default devicesim), NUMBER (default 21.5), AMPLITUDE (default 3), PERIOD (default 2m),
// EVENT_INTERVAL (default 10s, 0 sends no events), DEVICE_REPOSITORY_URL (default
// http://device-repository:8080), AUTH_URL (devauth, default http://devauth:8080).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/devicecommand"
	"github.com/SENERGY-Platform/models/go/models"
	"github.com/segmentio/kafka-go"
)

func env(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func main() {
	broker := env("KAFKA_URL", "kafka:9092")
	base, err := strconv.ParseFloat(env("NUMBER", "21.5"), 64)
	if err != nil {
		log.Fatal("NUMBER: ", err)
	}
	amplitude, err := strconv.ParseFloat(env("AMPLITUDE", "3"), 64)
	if err != nil {
		log.Fatal("AMPLITUDE: ", err)
	}
	period, err := time.ParseDuration(env("PERIOD", "2m"))
	if err != nil || period <= 0 {
		log.Fatal("PERIOD: ", env("PERIOD", "2m"))
	}
	interval, err := time.ParseDuration(env("EVENT_INTERVAL", "10s"))
	if err != nil {
		log.Fatal("EVENT_INTERVAL: ", err)
	}
	// the current number of all simulated devices
	number := func() float64 {
		phase := 2 * math.Pi * float64(time.Now().UnixNano()%int64(period)) / float64(period)
		return math.Round((base+amplitude*math.Sin(phase))*100) / 100
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	writer := &kafka.Writer{Addr: kafka.TCP(broker), RequiredAcks: kafka.RequireOne, AllowAutoTopicCreation: true}
	defer writer.Close()
	done := make(chan struct{})
	topics := strings.Split(env("TOPICS", "connector"), ",")
	for _, topic := range topics {
		reader := kafka.NewReader(kafka.ReaderConfig{Brokers: []string{broker}, GroupID: env("GROUP", "devicesim"), Topic: topic, StartOffset: kafka.FirstOffset})
		go func() {
			defer func() { done <- struct{}{} }()
			defer reader.Close()
			for {
				message, err := reader.ReadMessage(ctx)
				if err != nil {
					if ctx.Err() == nil {
						log.Println("read", topic, err)
					}
					return
				}
				if err = answer(ctx, writer, message.Value, number()); err != nil {
					log.Println("answer:", err)
				}
			}
		}()
	}
	log.Println("devicesim answers commands on", topics, "at", broker)
	if interval > 0 {
		source := eventSource{repository: env("DEVICE_REPOSITORY_URL", "http://device-repository:8080"), auth: env("AUTH_URL", "http://devauth:8080")}
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := source.send(ctx, writer, number()); err != nil {
						log.Println("events:", err)
					}
				}
			}
		}()
		log.Println("devicesim sends events every", interval)
	}
	for range topics {
		<-done
	}
}

func answer(ctx context.Context, writer *kafka.Writer, raw []byte, number float64) error {
	var msg devicecommand.ProtocolMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		return fmt.Errorf("unreadable command: %w", err)
	}
	segments := map[string]string{}
	for _, s := range msg.Metadata.Protocol.ProtocolSegments {
		segments[s.Id] = s.Name
	}
	output := map[string]string{}
	for _, content := range msg.Metadata.Service.Outputs {
		payload, err := serialise(content, number)
		if err != nil {
			return err
		}
		output[segments[content.ProtocolSegmentId]] = payload
	}
	msg.Response.Output = output
	msg.Trace = append(msg.Trace, devicecommand.Trace{TimeUnit: "unix_nano", Timestamp: time.Now().UnixNano(), Location: "devicesim"})
	log.Printf("command for device %v service %v, input %v, answer %v", msg.Metadata.Device.Name, msg.Metadata.Service.Name, msg.Request.Input, output)
	response, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	to := msg.Metadata.ResponseTo
	if to == "" {
		to = "response"
	}
	return writer.WriteMessages(ctx, kafka.Message{Topic: to, Key: []byte(msg.Metadata.Device.Id), Value: response})
}

func serialise(content models.Content, number float64) (string, error) {
	value := sample(content.ContentVariable, number)
	if content.Serialization == models.PlainText {
		return fmt.Sprint(value), nil
	}
	raw, err := json.Marshal(value)
	return string(raw), err
}

// sample is a value that fits the variable
func sample(v models.ContentVariable, number float64) any {
	if v.Value != nil {
		return v.Value
	}
	switch v.Type {
	case models.Structure:
		result := map[string]any{}
		for _, sub := range v.SubContentVariables {
			result[sub.Name] = sample(sub, number)
		}
		return result
	case models.List:
		result := []any{}
		for _, sub := range v.SubContentVariables {
			result = append(result, sample(sub, number))
		}
		return result
	case models.Integer:
		return int64(number)
	case models.Float:
		return number
	case models.Boolean:
		return true
	default:
		return "simulated"
	}
}

// eventSource finds the devices to send events for in device-repository
type eventSource struct {
	repository string
	auth       string
}

// send sends one event of every device for each of its event services
func (this eventSource) send(ctx context.Context, writer *kafka.Writer, number float64) error {
	messages, err := this.messages(ctx, number)
	if err != nil || len(messages) == 0 {
		return err
	}
	log.Printf("events of %d device services, number %v", len(messages), number)
	return writer.WriteMessages(ctx, messages...)
}

func (this eventSource) messages(ctx context.Context, number float64) ([]kafka.Message, error) {
	// an admin token sees the devices of every user
	token, err := this.get(ctx, this.auth+"/token/admin?roles=admin", "")
	if err != nil {
		return nil, err
	}
	var list []models.Device
	if err = this.getJSON(ctx, this.repository+"/devices?limit=1000", string(token), &list); err != nil {
		return nil, err
	}
	deviceTypes := map[string]models.DeviceType{}
	var messages []kafka.Message
	for _, device := range list {
		deviceType, ok := deviceTypes[device.DeviceTypeId]
		if !ok {
			if err = this.getJSON(ctx, this.repository+"/device-types/"+url.PathEscape(device.DeviceTypeId), string(token), &deviceType); err != nil {
				return nil, err
			}
			deviceTypes[device.DeviceTypeId] = deviceType
		}
		for _, service := range deviceType.Services {
			if service.Interaction != models.EVENT && service.Interaction != models.EVENT_AND_REQUEST {
				continue
			}
			value := map[string]any{}
			for _, content := range service.Outputs {
				value[content.ContentVariable.Name] = sample(content.ContentVariable, number)
			}
			raw, err := json.Marshal(events.DeviceMessage{DeviceId: device.Id, ServiceId: service.Id, Value: value})
			if err != nil {
				return nil, err
			}
			messages = append(messages, kafka.Message{Topic: devices.ServiceIdToTopic(service.Id), Key: []byte(device.Id), Value: raw})
		}
	}
	return messages, nil
}

func (this eventSource) getJSON(ctx context.Context, target string, token string, result any) error {
	raw, err := this.get(ctx, target, token)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

func (this eventSource) get(ctx context.Context, target string, token string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", strings.TrimSpace(token))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %v: %v %s", target, resp.StatusCode, body)
	}
	return body, nil
}
