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
// setup: it reads the device commands the engine sends to the Kafka topic of a protocol handler
// and answers every one on the topic the command names (metadata.response_to), as
// platform-connector does once the device answered.
//
// The answer follows the outputs of the service: every protocol segment gets the content its
// output describes, with the value of a variable if the device type fixes one, NUMBER for
// numbers, true for booleans and "simulated" for strings, serialised as JSON or plain text.
// A service without outputs, a setter, gets an empty answer. Inputs are logged, not stored.
//
// Environment: KAFKA_URL (default kafka:9092), TOPICS (comma separated, default connector),
// GROUP (default devicesim), NUMBER (default 21.5).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

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
	number, err := strconv.ParseFloat(env("NUMBER", "21.5"), 64)
	if err != nil {
		log.Fatal("NUMBER: ", err)
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
				if err = answer(ctx, writer, message.Value, number); err != nil {
					log.Println("answer:", err)
				}
			}
		}()
	}
	log.Println("devicesim answers commands on", topics, "at", broker)
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
