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
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// When Run of a role returns, its consumers have left their groups. A pod that exits as a member
// keeps the partitions of its group until the session timeout; in a rollout the replacing pods
// then consume nothing for up to a minute.
func TestStoppedRolesLeaveTheirConsumerGroups(t *testing.T) {
	platform := containers(t)
	f, _ := realDeviceFixture(t, platform)
	engineGroup, eventGroup := "engine-"+uuid.NewString(), "events-"+uuid.NewString()

	config := configuration.Default()
	config.PollInterval = "20ms"
	deps, err := client.TestDependencies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	usePlatform(t, platform, &config, &deps)
	config.DeviceRepositoryUrl, config.ImportDeployUrl, config.ImportRepositoryUrl = platform.deviceRepository, platform.importDeploy, platform.importRepository
	config.AuthEndpoint, config.AuthClientId = platform.keycloak, "automation-engine"
	config.KafkaUrl, config.KafkaConsumerGroup, config.EventConsumerGroup = platform.kafka, engineGroup, eventGroup
	config.ServerPort, config.InternalPort, config.PprofPort = 0, 0, 0
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	roles, stop := context.WithCancel(ctx)
	defer stop()
	engineRole, err := server.New(roles, config, logger, deps)
	if err != nil {
		t.Fatal(err)
	}
	defer engineRole.Close()
	eventRole, err := server.NewEventServer(roles, config, logger, server.EventDependencies{})
	if err != nil {
		t.Fatal(err)
	}
	defer eventRole.Close()
	stopped := make(chan error, 2)
	go func() { stopped <- engineRole.Run(roles) }()
	go func() { stopped <- eventRole.Run(roles) }()

	// the event role joins its group only once a description names a topic
	httpServer := httptest.NewServer(engineRole.Handler)
	defer httpServer.Close()
	xml := b.Process(b.Start("start"), warmEvent("warm", "intermediateCatchEvent"), b.End("end"),
		b.Flow("start", "warm"), b.Flow("warm", "end"))
	_, err = client.New(httpServer.URL).CreateDeployment(ctx, alice, processes, model.DeploymentInput{Name: "Heating", Xml: xml,
		Selections: map[string]model.Selection{"warm": {DeviceId: f.d1, ServiceId: f.service}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []string{engineGroup, eventGroup} {
		waitUntil(t, group+" to have a member", func() error {
			members, err := groupMembers(platform.kafka, group)
			if err == nil && members == 0 {
				err = fmt.Errorf("no member in %v", group)
			}
			return err
		})
	}

	stop()
	for range 2 {
		select {
		case err := <-stopped:
			if err != nil {
				t.Fatal("a role stopped with an error:", err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("a role did not stop")
		}
	}
	for _, group := range []string{engineGroup, eventGroup} {
		if members, err := groupMembers(platform.kafka, group); err != nil || members != 0 {
			t.Errorf("%v keeps %v members after its role stopped (%v)", group, members, err)
		}
	}
}

func groupMembers(url string, group string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := (&kafka.Client{Addr: kafka.TCP(url)}).DescribeGroups(ctx, &kafka.DescribeGroupsRequest{GroupIDs: []string{group}})
	if err != nil {
		return 0, err
	}
	if len(result.Groups) != 1 {
		return 0, fmt.Errorf("described %v groups", len(result.Groups))
	}
	if result.Groups[0].Error != nil {
		return 0, result.Groups[0].Error
	}
	return len(result.Groups[0].Members), nil
}
