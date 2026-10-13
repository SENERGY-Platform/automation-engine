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

// Package fogsync synchronises the engine in the cloud with the engines on gateways: the
// cloud side (cloud.go), the gateway side (gateway.go) and the protocol between them
// (protocol.go).
//
// The protocol connects the engine in the cloud and the
// engines on gateways (section 17). Both run the same engine, so the synchronisation replicates
// between equal data models with the same ids.
//
// Transport: MQTT through mgw-device-cloud-connector, which bridges processes/{network}/cmd/#
// of the cloud broker to processes/cmd/# of the gateway broker and processes/state/# of the
// gateway broker to processes/{network}/state/# of the cloud broker. It delivers at most once,
// so reliability is part of the protocol:
//
//   - Down, the cloud sends the desired state of resources: namespaces, deployments with their
//     event descriptions, instances to run, stop or delete. Every resource has a version that
//     grows with each change of its desired state, also after both sides forgot the resource:
//     it starts at the time in milliseconds. The gateway applies a newer version
//     idempotently and acknowledges every resource; the cloud sends again what stays
//     unacknowledged (SYNC-01, SYNC-08). The order of messages does not matter. Applied
//     deletions and the start of an instance the gateway reported as ended are forgotten, in
//     the cloud at once, on the gateway once the instance is gone there.
//   - Up, the gateway sends its change stream: batches of instance states after a position of
//     the stream up to a position. The cloud takes a batch only if it continues the position it
//     acknowledged last, and acknowledges after its commit; the gateway sends again from there
//     (SYNC-02, SYNC-10). Changes of an instance within a batch are folded to its latest state
//     (LONG-09, LONG-10); a gateway that cannot continue sends a snapshot. Retention and
//     storage budget of the gateway keep an instance until the cloud took its end; an ended
//     instance missing from a snapshot stays in the cloud.
//   - The gateway reports its status when it starts and periodically: versions, positions,
//     applied resources and storage (SYNC-11, SYNC-12, SYNC-13, LONG-14).
//
// Every message carries the protocol version (SYNC-11).
package fogsync

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// Version of this protocol; a change of a message is a new version (SYNC-11).
const Version = 1

const (
	root = "processes/"
	path = "engine/v1/"

	// down
	KindDesired = "desired"
	KindUpAck   = "ack"
	KindRequest = "request"
	// up
	KindStatus     = "status"
	KindChanges    = "changes"
	KindDesiredAck = "applied"
	// up, followed by the replica that asked: response/{replica}
	KindResponse = "response"
)

// CloudCommandTopic is where the cloud sends a message of a kind to a network.
func CloudCommandTopic(networkId string, kind string) string {
	return root + networkId + "/cmd/" + path + kind
}

// GatewayCommandFilter subscribes the messages of the cloud on the broker of a gateway.
const GatewayCommandFilter = root + "cmd/" + path + "#"

// GatewayStateTopic is where a gateway sends a message of a kind.
func GatewayStateTopic(kind string) string {
	return root + "state/" + path + kind
}

// CloudStateFilter subscribes the messages of all gateways in the cloud; with a group, replicas
// share them (SYNC-10).
func CloudStateFilter(group string) string {
	filter := root + "+/state/" + path + "#"
	if group == "" {
		return filter
	}
	return "$share/" + group + "/" + filter
}

// GatewayResponseTopic is where a gateway answers a request of a replica.
func GatewayResponseTopic(replica string) string {
	return GatewayStateTopic(KindResponse + "/" + replica)
}

// CloudResponseFilter subscribes the answers to the requests of one replica, of every gateway;
// not shared, the replica waits for them itself.
func CloudResponseFilter(replica string) string {
	return root + "+/state/" + path + KindResponse + "/" + replica
}

// KindOf reads the kind of a message from its topic, on either side; network is empty on a gateway.
func KindOf(topic string) (network string, kind string, ok bool) {
	rest, found := strings.CutPrefix(topic, root)
	if !found {
		return "", "", false
	}
	before, kind, found := strings.Cut(rest, "/"+path)
	if !found {
		if kind, found = strings.CutPrefix(rest, "cmd/"+path); found {
			return "", kind, true
		}
		return "", "", false
	}
	parts := strings.Split(before, "/")
	switch {
	case len(parts) == 2 && (parts[1] == "cmd" || parts[1] == "state"):
		return parts[0], kind, true
	case len(parts) == 1 && (parts[0] == "cmd" || parts[0] == "state"):
		return "", kind, true
	}
	return "", "", false
}

// Envelope is part of every message.
type Envelope struct {
	Protocol int `json:"protocol"`
	// version of the sending engine
	Engine string    `json:"engine,omitempty"`
	SentAt time.Time `json:"sent_at"`
}

// Kinds of desired resources.
const (
	ResourceNamespace  = "namespace"
	ResourceDeployment = "deployment"
	ResourceInstance   = "instance"
)

// Desired carries desired resources from the cloud to a gateway.
type Desired struct {
	Envelope
	Resources []Resource `json:"resources"`
}

// Resource is the complete desired state of one resource; Deleted removes it.
type Resource struct {
	Kind    string `json:"kind"`
	Id      string `json:"id"`
	Version int64  `json:"version"`
	Deleted bool   `json:"deleted,omitempty"`

	Namespace  *model.Namespace `json:"namespace,omitempty"`
	Deployment *DeploymentSpec  `json:"deployment,omitempty"`
	Instance   *InstanceSpec    `json:"instance,omitempty"`
}

// DeploymentSpec is a deployment with its current version, as in the cloud; the event
// descriptions are derived in the cloud and read the topics of the gateway (SYNC-06).
type DeploymentSpec struct {
	Deployment   model.Deployment         `json:"deployment"`
	Version      model.DeploymentVersion  `json:"version"`
	Descriptions []model.EventDescription `json:"descriptions,omitempty"`
	Groups       []string                 `json:"groups,omitempty"`
}

// InstanceSpec is an instance the gateway runs; a start is idempotent by the id (RUN-01).
// Stopped stops it, restarts after incidents are the gateway's own (FOG-07).
type InstanceSpec struct {
	DeploymentId string         `json:"deployment_id"`
	Owner        string         `json:"owner"`
	Variables    map[string]any `json:"variables,omitempty"`
	Stopped      bool           `json:"stopped,omitempty"`
}

// Applied acknowledges desired resources; Error says why one could not be applied (SYNC-13).
type Applied struct {
	Envelope
	Results []Result `json:"results"`
}

type Result struct {
	Kind    string    `json:"kind"`
	Id      string    `json:"id"`
	Version int64     `json:"version"`
	Error   string    `json:"error,omitempty"`
	At      time.Time `json:"at"`
}

// Changes is a batch of the change stream of a gateway. The cloud takes it only if After is the
// position it acknowledged last; a snapshot replaces everything the cloud holds of the network.
type Changes struct {
	Envelope
	After            int64           `json:"after"`
	Upto             int64           `json:"upto"`
	Snapshot         bool            `json:"snapshot,omitempty"`
	Instances        []InstanceState `json:"instances,omitempty"`
	DeletedInstances []string        `json:"deleted_instances,omitempty"`
}

// InstanceState is the latest state of an instance with the incidents of its current run.
type InstanceState struct {
	Instance  model.Instance   `json:"instance"`
	Incidents []model.Incident `json:"incidents,omitempty"`
}

// UpAck tells a gateway the position of its change stream the cloud has taken.
type UpAck struct {
	Envelope
	Acked int64 `json:"acked"`
}

// Status of a gateway, at its start and periodically; it serves as sign of life (SYNC-12).
type Status struct {
	Envelope
	StartedAt time.Time `json:"started_at"`
	// the latest position of the change stream and the one the cloud acknowledged
	Latest int64 `json:"latest"`
	Acked  int64 `json:"acked"`
	// every resource the gateway applied, so that the cloud resends what is missing
	Applied []Result `json:"applied,omitempty"`
	Storage Storage  `json:"storage"`
}

// Storage of a gateway (LONG-14).
type Storage struct {
	UsedBytes   int64            `json:"used_bytes"`
	BudgetBytes int64            `json:"budget_bytes"`
	Rows        map[string]int64 `json:"rows,omitempty"`
}

// Compatible tells whether the cloud serves a gateway of a protocol version; the cloud supports
// its own version and window versions before it (SYNC-11).
func Compatible(gatewayProtocol int, window int) bool {
	return gatewayProtocol <= Version && gatewayProtocol >= Version-window
}

// Requests read the history and the state of an instance on its gateway, where it stays
// (DBG-15), or watch it live while the cloud renews the request (DBG-12).
const (
	MethodDebugState       = "debug_state"
	MethodHistoryState     = "history_state"
	MethodListRuns         = "list_runs"
	MethodListActivities   = "list_activities"
	MethodListElementStats = "list_element_stats"
	MethodGetVariables     = "get_variables"
	MethodGetVariable      = "get_variable"
	MethodHistoryVariable  = "history_variable"
	// a stream of debug messages; the gateway ends it without renewal within WatchLease
	MethodWatch   = "watch"
	MethodUnwatch = "unwatch"
)

// WatchLease is how long a gateway streams after the last watch request.
const WatchLease = time.Minute

// Request asks a gateway; the answer goes to GatewayResponseTopic(ReplyTo).
type Request struct {
	Envelope
	Id        string `json:"id"`
	ReplyTo   string `json:"reply_to"`
	Method    string `json:"method"`
	Namespace string `json:"namespace"`
	Instance  string `json:"instance"`
	// method parameters: options of the method, a variable name, element id or run
	Args RequestArgs `json:"args"`
}

type RequestArgs struct {
	Name      string          `json:"name,omitempty"`
	ElementId string          `json:"element_id,omitempty"`
	Run       int             `json:"run,omitempty"`
	Options   json.RawMessage `json:"options,omitempty"`
}

// Response answers a request; a watch answers with one response per debug message and a final
// one when it ends.
type Response struct {
	Envelope
	RequestId string          `json:"request_id"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *model.Error    `json:"error,omitempty"`
	Final     bool            `json:"final,omitempty"`
}
