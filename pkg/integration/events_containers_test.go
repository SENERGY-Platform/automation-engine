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

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// newContainerEventEnv creates the thermometers and the weather import of a scenario in the
// real services, with ids of their own, and starts both roles against Kafka
func newContainerEventEnv(t *testing.T, platform *realPlatform) *eventEnv {
	t.Helper()
	f := realFixture(t, platform)
	engineGroup, eventGroup := "engine-"+uuid.NewString(), "events-"+uuid.NewString()
	e := startRoles(t, func(c *configuration.Config, deps *server.Dependencies) {
		usePlatform(t, platform, c, deps)
		c.DeviceRepositoryUrl, c.ImportDeployUrl, c.ImportRepositoryUrl = platform.deviceRepository, platform.importDeploy, platform.importRepository
		c.AuthEndpoint, c.AuthClientId = platform.keycloak, "automation-engine"
		c.KafkaUrl, c.KafkaConsumerGroup, c.EventConsumerGroup = platform.kafka, engineGroup, eventGroup
	}, nil)
	return &eventEnv{env: e, fixture: f, bus: &realKafka{url: platform.kafka, group: eventGroup}}
}

func realFixture(t *testing.T, platform *realPlatform) fixture {
	t.Helper()
	admin := auth.UnsignedToken("test-admin", "admin")
	repo := devicerepository.NewClient(platform.deviceRepository, nil)
	check := func(what string, err error, code int) {
		t.Helper()
		if err != nil || code >= 300 {
			t.Fatalf("device-repository, %v: %v %v", what, code, err)
		}
	}

	// the meaning of a temperature; the same ids in every scenario, so written again each time
	characteristics, concept, function := temperatureModels()
	for _, c := range characteristics {
		_, err, code := repo.SetCharacteristic(admin, c)
		check("characteristic", err, code)
	}
	_, err, code := repo.SetConcept(admin, concept)
	check("concept", err, code)
	_, err, code = repo.SetFunction(admin, function)
	check("function", err, code)
	_, err, code = repo.SetAspect(admin, models.Aspect{Id: airAspect, Name: "air"})
	check("aspect", err, code)

	// thermometers of this scenario
	suffix := uuid.NewString()
	protocol := models.Protocol{Id: "urn:infai:ses:protocol:" + suffix, Name: "test", Handler: "connector",
		ProtocolSegments: []models.ProtocolSegment{{Id: "urn:infai:ses:protocol-segment:" + suffix, Name: "data"}}}
	_, err, code = repo.SetProtocol(admin, protocol)
	check("protocol", err, code)
	f := fixture{service: "urn:infai:ses:service:" + suffix}
	deviceType := models.DeviceType{Id: "urn:infai:ses:device-type:" + suffix, Name: "Thermometer " + suffix,
		Services: []models.Service{{Id: f.service, LocalId: "temperature", Name: "temperature", Interaction: models.EVENT, ProtocolId: protocol.Id,
			Outputs: []models.Content{{Id: "urn:infai:ses:content:" + suffix, ContentVariable: withIds(reading(), suffix), Serialization: models.JSON, ProtocolSegmentId: protocol.ProtocolSegments[0].Id}}}}}
	_, err, code = repo.SetDeviceType(admin, deviceType, devicerepomodel.DeviceTypeUpdateOptions{})
	check("device type", err, code)
	// with an id of its own: with DISABLE_STRICT_VALIDATION_FOR_TESTING, device-repository creates
	// devices without generating one
	device := func(token string, name string) string {
		t.Helper()
		id := "urn:infai:ses:device:" + uuid.NewString()
		_, err, code := repo.SetDevice(token, models.Device{Id: id, LocalId: id, Name: name, DeviceTypeId: deviceType.Id}, devicerepomodel.DeviceUpdateOptions{})
		check("device "+name, err, code)
		return id
	}
	f.d1, f.d2, f.foreign = device(alice, "thermometer 1"), device(alice, "thermometer 2"), device(bob, "thermometer of bob")
	group := models.DeviceGroup{Id: "urn:infai:ses:device-group:" + suffix, Name: "Thermometers " + suffix, DeviceIds: []string{f.d1, f.d2}}
	_, err, code = repo.SetDeviceGroup(alice, group)
	check("device group", err, code)
	f.group = group.Id
	// the engine reads them with the token of alice, once device-repository has them and their rights
	waitUntil(t, "the thermometers in device-repository", func() error {
		for _, id := range []string{f.d1, f.d2} {
			if _, err, _ := repo.ReadDevice(id, alice, models.UnsetPermissionFlag); err != nil {
				return fmt.Errorf("device %v: %w", id, err)
			}
		}
		if _, err, _ := repo.ReadDeviceGroup(f.group, alice, false); err != nil {
			return fmt.Errorf("device group %v: %w", f.group, err)
		}
		if _, err, _ := repo.GetAspectNode(airAspect); err != nil {
			return fmt.Errorf("aspect node %v: %w", airAspect, err)
		}
		return nil
	})
	if err := createTopic(platform.kafka, f.serviceTopic()); err != nil {
		t.Fatal(err)
	}

	// the weather import of this scenario
	var importType models.ImportType
	waitUntil(t, "the import type in import-repository", func() error {
		return call(alice, http.MethodPost, platform.importRepository+"/import-types", models.ImportType{Name: "Weather " + suffix,
			Image: "ghcr.io/senergy-platform/import-weather:latest", Configs: []models.ImportTypeConfig{}, Output: weather()}, &importType)
	})
	var instance models.Import
	waitUntil(t, "the import in import-deploy", func() error {
		return call(alice, http.MethodPost, platform.importDeploy+"/instances", map[string]any{"name": "Weather " + suffix,
			"import_type_id": importType.Id, "configs": []any{}, "restart": false}, &instance)
	})
	f.importId, f.importTopic = instance.Id, instance.KafkaTopic
	return f
}

// withIds gives the content variables ids of their own, as device-repository expects them
func withIds(v models.ContentVariable, suffix string) models.ContentVariable {
	v.Id = "urn:infai:ses:content-variable:" + v.Name + ":" + suffix
	for i := range v.SubContentVariables {
		v.SubContentVariables[i] = withIds(v.SubContentVariables[i], suffix)
	}
	return v
}

// call sends JSON to a platform service and decodes the answer
func call(token string, method string, url string, body any, result any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%v %v: %v %s", method, url, resp.StatusCode, answer)
	}
	return json.Unmarshal(answer, result)
}

// waitUntil retries fn, which the services answer once their asynchronous work is done
func waitUntil(t *testing.T, what string, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		err := fn()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %v: %v", what, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// createTopic creates a topic before a consumer group asks for it: a group that joins without
// the topic gets no partition until its next check, a minute later
func createTopic(url string, topic string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := (&kafka.Client{Addr: kafka.TCP(url)}).CreateTopics(ctx, &kafka.CreateTopicsRequest{
		Topics: []kafka.TopicConfig{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}},
	})
	if err != nil {
		return err
	}
	if err = result.Errors[topic]; err != nil && err != kafka.TopicAlreadyExists {
		return err
	}
	return nil
}

// realKafka sends to Kafka; the event role reads in the consumer group of the scenario
type realKafka struct {
	url   string
	group string
}

func (this *realKafka) send(topic string, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	writer := &kafka.Writer{Addr: kafka.TCP(this.url), Topic: topic, RequiredAcks: kafka.RequireAll}
	defer writer.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return writer.WriteMessages(ctx, kafka.Message{Key: []byte(key), Value: raw})
}

// ready waits until the group of the event role committed a probe sent after it joined. A group
// that only got its partition may still settle its start offset at the end of the topic and
// skip what was sent before; once a probe is committed, later messages are read.
func (this *realKafka) ready(t *testing.T, topic string, probe any) {
	t.Helper()
	waitUntil(t, "the event role to read "+topic, func() error {
		if err := this.send(topic, "probe", probe); err != nil {
			return err
		}
		return this.committed(topic, 10*time.Second)
	})
}

// drain waits until the group committed everything on the topic; the event role commits a
// message after it handled it
func (this *realKafka) drain(t *testing.T, topic string) {
	t.Helper()
	if err := this.committed(topic, 30*time.Second); err != nil {
		t.Fatal(err)
	}
}

func (this *realKafka) committed(topic string, timeout time.Duration) error {
	client := &kafka.Client{Addr: kafka.TCP(this.url)}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	end, err := client.ListOffsets(ctx, &kafka.ListOffsetsRequest{Topics: map[string][]kafka.OffsetRequest{topic: {kafka.LastOffsetOf(0)}}})
	if err != nil {
		return err
	}
	last := end.Topics[topic][0].LastOffset
	for ctx.Err() == nil {
		committed, err := client.OffsetFetch(ctx, &kafka.OffsetFetchRequest{GroupID: this.group, Topics: map[string][]int{topic: {0}}})
		if err == nil && len(committed.Topics[topic]) == 1 && committed.Topics[topic][0].CommittedOffset >= last {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("the messages up to offset %v of %v are not committed by %v", last, topic, this.group)
}
