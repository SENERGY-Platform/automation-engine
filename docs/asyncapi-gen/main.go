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

// Command asyncapi-gen writes docs/asyncapi.json, the AsyncAPI 3.0.0 description of the Kafka
// topics and MQTT topics the engine reads and writes. The payload schemas are reflected from the
// types the engine uses, so a changed message changes the document; CI regenerates it and fails
// on a difference, as for the OpenAPI document.
//
// Unlike the generators of other platform services, it writes 3.0.0 directly and runs with
// `go generate ./...` from the repository root: there is no conversion by hand.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"log"
	"os"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/permissions"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/devicecommand"
	"github.com/SENERGY-Platform/service-commons/pkg/cache/invalidator"
	asyncapi "github.com/swaggest/go-asyncapi/reflector/asyncapi-3.0.0"
	spec "github.com/swaggest/go-asyncapi/spec-3.0.0"
)

// ImportMessage is a message of an import on its Kafka topic.
type ImportMessage struct {
	ImportId string `json:"import_id" description:"id of the import instance; the further fields are the output of its import type"`
}

const (
	kafka       = "kafka"
	gatewayMqtt = "gateway-mqtt"
	syncMqtt    = "sync-mqtt"
)

type generator struct {
	reflector asyncapi.Reflector
	err       error
}

// channel adds a channel under id with the address; parameters name the {placeholders} of the
// address with their description
func (this *generator) channel(id string, address string, description string, server string, parameters ...string) spec.Reference {
	return this.reflector.AddChannel(id, func(ch *spec.Channel) {
		ch.WithAddress(address)
		ch.Description = description
		ch.Servers = []spec.Reference{{Ref: "#/servers/" + server}}
		for i := 0; i+1 < len(parameters); i += 2 {
			if ch.Parameters == nil {
				ch.Parameters = map[string]spec.ParameterOrRef{}
			}
			ch.Parameters[parameters[i]] = spec.ParameterOrRef{Parameter: &spec.Parameter{Description: parameters[i+1]}}
		}
	})
}

// operation adds what the engine does on a channel: it sends or receives the sample
func (this *generator) operation(action spec.OperationAction, channel spec.Reference, sample any, name string, summary string) {
	if this.err != nil {
		return
	}
	this.err = this.reflector.AddOperation(channel, action, func(m *asyncapi.MessageSample) {
		m.Sample = sample
		m.Name = name
		m.Title = name
	}, func(op *spec.Operation) {
		op.Summary = summary
	})
}

func main() {
	output := flag.String("o", "docs/asyncapi.json", "file to write")
	flag.Parse()
	config := configuration.Default()

	g := &generator{}
	doc := &spec.AsyncAPI{}
	doc.UpdateInfo(func(i *spec.Info) {
		i.Title = "Automation-Engine"
		i.Version = "0.1"
		i.Description = "Kafka topics and MQTT topics of the engine. Topic names are the defaults of the configuration; " +
			"the environment variable that changes one is named in its channel. {placeholders} in addresses are channel parameters."
	})
	doc.AddServer(kafka, func(s *spec.Server) {
		s.Host, s.Protocol = "kafka:9092", "kafka"
		s.Description = "KAFKA_URL; the cloud."
	})
	doc.AddServer(gatewayMqtt, func(s *spec.Server) {
		s.Host, s.Protocol = "mqtt:1883", "mqtt"
		s.Description = "MQTT_BROKER; the broker of a gateway, in fog mode, shared with the device connectors."
	})
	doc.AddServer(syncMqtt, func(s *spec.Server) {
		s.Host, s.Protocol = "mqtt:1883", "mqtt"
		s.Description = "SYNC_MQTT_BROKER; the cloud broker that mgw-device-cloud-connector bridges to the brokers of the gateways."
	})
	g.reflector.Schema = doc
	send, receive := spec.OperationActionSend, spec.OperationActionReceive

	// device commands in the cloud (TASK-04, TASK-05)
	g.operation(send, g.channel("protocolCommand", "{protocolHandler}",
		"Device commands to platform-connector. The topic is the handler of the protocol of the device; a handler that is an "+
			"http(s) URL gets the message as POST instead. The key is the device id.", kafka,
		"protocolHandler", "handler of the protocol of the device, e.g. connector"),
		new(devicecommand.ProtocolMsg), "ProtocolMsg", "send a device command")
	g.operation(receive, g.channel("commandResponse", config.ResponseTopic,
		"RESPONSE_TOPIC; answers of protocol handlers, read in the consumer group KAFKA_CONSUMER_GROUP. "+
			"Protocol handlers may also POST them to /responses of the internal port.", kafka),
		new(devicecommand.ProtocolMsg), "ProtocolMsg", "complete a device command")
	g.operation(receive, g.channel("commandError", config.ErrorTopic,
		"ERROR_TOPIC; failed commands, read in the consumer group KAFKA_CONSUMER_GROUP. "+
			"Protocol handlers may also POST them to /errors of the internal port.", kafka),
		new(devicecommand.ProtocolMsg), "ProtocolMsg", "fail a device command")

	// events in the cloud, read by the event role (EVT-01)
	g.operation(receive, g.channel("deviceData", "{serviceTopic}",
		"Device data of the services that event descriptions of deployments name, read in the consumer group "+
			"EVENT_CONSUMER_GROUP. The topic is the service id with ':' and '#' replaced by '_'.", kafka,
		"serviceTopic", "service id with ':' and '#' replaced by '_'"),
		new(events.DeviceMessage), "DeviceMessage", "evaluate device events")
	g.operation(receive, g.channel("importData", "{importTopic}",
		"Data of the imports that event descriptions of deployments name, read in the consumer group EVENT_CONSUMER_GROUP.", kafka,
		"importTopic", "kafka_topic of the import instance in import-deploy"),
		new(ImportMessage), "ImportMessage", "evaluate import events")

	// platform signals
	g.operation(receive, g.channel("deviceGroups", config.DeviceGroupTopic,
		"DEVICE_GROUP_TOPIC; a changed device group changes the event descriptions of deployments that select it (DEP-08) "+
			"and clears its cache entry in every replica.", kafka),
		new(invalidator.Command), "DeviceGroupCommand", "recompute event descriptions")
	g.operation(receive, g.channel("devices", config.DeviceTopic,
		"DEVICE_TOPIC; clears the cache entry of the device in every replica.", kafka),
		new(invalidator.Command), "DeviceCommand", "clear the device cache")
	for _, topic := range config.CacheInvalidationAllTopics {
		g.operation(receive, g.channel(camel(topic), topic,
			"CACHE_INVALIDATION_ALL_TOPICS; any message clears the device cache of every replica.", kafka),
			new(invalidator.Command), "CacheInvalidation", "clear the device cache")
	}
	g.operation(receive, g.channel("users", config.UserTopic,
		"USER_TOPIC; a DELETE removes the data of a user deleted outside of user-management (SEC-14).", kafka),
		new(invalidator.Command), "UserCommand", "delete user data")
	g.operation(receive, g.channel("permissions", "{permissionsTopic}",
		"PERMISSIONS_KAFKA_TOPIC, not set by default; a RIGHTS message without the administrate right of the owner "+
			"restores it (SEC-10).", kafka,
		"permissionsTopic", "PERMISSIONS_KAFKA_TOPIC"),
		new(permissions.RightsMessage), "RightsMessage", "restore owner rights")

	// device commands and events on a gateway (FOG-01)
	g.operation(send, g.channel("localCommand", "command/{localDeviceId}/{localServiceId}",
		"Device commands to the device connectors of a gateway; command_id starts with LOCAL_COMMAND_PREFIX.", gatewayMqtt,
		"localDeviceId", "local id of the device", "localServiceId", "local id of the service"),
		new(devicecommand.LocalEnvelope), "LocalEnvelope", "send a device command")
	g.operation(receive, g.channel("localResponse", "response/{localDeviceId}/{localServiceId}",
		"Answers of device connectors; those whose command_id lacks LOCAL_COMMAND_PREFIX belong to another service and are ignored.", gatewayMqtt,
		"localDeviceId", "local id of the device", "localServiceId", "local id of the service"),
		new(devicecommand.LocalEnvelope), "LocalEnvelope", "complete a device command")
	g.operation(receive, g.channel("localError", "error/command/{commandId}",
		"A command a device connector could not deliver; the payload is the error text.", gatewayMqtt,
		"commandId", "command_id of the command"),
		new(string), "LocalError", "fail a device command")
	g.operation(receive, g.channel("localEvent", "event/{localDeviceId}/{localServiceId}",
		"Device data on a gateway; the payload is the data segment as the service describes it.", gatewayMqtt,
		"localDeviceId", "local id of the device", "localServiceId", "local id of the service"),
		new(string), "LocalEvent", "evaluate device events")

	// synchronisation between the cloud and the gateways (section 17), see pkg/fogsync/protocol.go
	type kind struct {
		name, id, summary string
		sample            any
	}
	down := []kind{
		{fogsync.KindDesired, "Desired", "desired state of resources", new(fogsync.Desired)},
		{fogsync.KindUpAck, "UpAck", "acknowledge a batch of the change stream", new(fogsync.UpAck)},
		{fogsync.KindRequest, "Request", "ask the gateway, e.g. for the debug state of an instance", new(fogsync.Request)},
	}
	up := []kind{
		{fogsync.KindStatus, "Status", "status of the gateway", new(fogsync.Status)},
		{fogsync.KindChanges, "Changes", "batch of the change stream", new(fogsync.Changes)},
		{fogsync.KindDesiredAck, "Applied", "results of applied resources", new(fogsync.Applied)},
	}
	network := []string{"networkId", "id of the hub of the gateway"}
	for _, k := range down {
		g.operation(send, g.channel("cloud"+k.id, fogsync.CloudCommandTopic("{networkId}", k.name),
			"Cloud to gateway.", syncMqtt, network...), k.sample, k.id, k.summary)
		g.operation(receive, g.channel("gateway"+k.id, filterTopic(fogsync.GatewayCommandFilter, "", k.name),
			"Cloud to gateway, on the broker of the gateway.", gatewayMqtt), k.sample, k.id, k.summary)
	}
	for _, k := range up {
		g.operation(receive, g.channel("cloud"+k.id, filterTopic(fogsync.CloudStateFilter(""), "{networkId}", k.name),
			"Gateway to cloud; the replicas share the subscription SYNC_SHARE_GROUP.", syncMqtt, network...), k.sample, k.id, k.summary)
		g.operation(send, g.channel("gateway"+k.id, fogsync.GatewayStateTopic(k.name),
			"Gateway to cloud, on the broker of the gateway.", gatewayMqtt), k.sample, k.id, k.summary)
	}
	g.operation(receive, g.channel("cloudResponse", filterTopic(fogsync.CloudStateFilter(""), "{networkId}", fogsync.KindResponse+"/{replicaId}"),
		"Answer of a gateway to a request; only the replica that asked subscribes.", syncMqtt,
		"networkId", "id of the hub of the gateway", "replicaId", "id of the replica that asked"),
		new(fogsync.Response), "Response", "answer to a request")
	g.operation(send, g.channel("gatewayResponse", fogsync.GatewayResponseTopic("{replicaId}"),
		"Answer of a gateway to a request, on the broker of the gateway.", gatewayMqtt,
		"replicaId", "reply_to of the request"),
		new(fogsync.Response), "Response", "answer to a request")
	if g.err != nil {
		log.Fatal(g.err)
	}

	// operation ids in the style of the channel ids, sendLocalCommand instead of the
	// send:localCommand of the reflector
	operations := map[string]spec.OperationOrRef{}
	for id, op := range doc.Operations {
		action, channel, _ := strings.Cut(id, ":")
		operations[action+capitalize(channel)] = op
	}
	doc.Operations = operations

	raw, err := doc.MarshalJSON()
	if err != nil {
		log.Fatal(err)
	}
	var formatted bytes.Buffer
	if err = json.Indent(&formatted, raw, "", "    "); err != nil {
		log.Fatal(err)
	}
	formatted.WriteString("\n")
	if err = os.WriteFile(*output, formatted.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}

// filterTopic turns an MQTT filter that ends in # into the topic of a kind; + becomes the
// placeholder
func filterTopic(filter string, placeholder string, kind string) string {
	return strings.Replace(strings.TrimSuffix(filter, "#"), "+", placeholder, 1) + kind
}

func camel(topic string) string {
	parts := strings.FieldsFunc(topic, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	for i := 1; i < len(parts); i++ {
		parts[i] = capitalize(parts[i])
	}
	return strings.Join(parts, "")
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
