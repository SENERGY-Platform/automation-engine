#!/bin/sh
# Copyright 2026 InfAI (CC SES)
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Creates a thermostat of alice in the device-repository of docker-compose.platform.yml, with a
# service that reads the room temperature and also reports it as event, and one that sets the
# target temperature, on a protocol whose handler topic devicesim answers. devicesim sends the
# events of the first service. Safe to run again. Needs curl and jq.
#
#   ./examples/seed-devices.sh

set -eu

REPO=${REPO:-http://localhost:8084}
AUTH=${AUTH:-http://localhost:8090}
ADMIN=$(curl -sf "$AUTH/token/admin?roles=admin")
ALICE=$(curl -sf "$AUTH/token/alice")

CELSIUS=urn:infai:ses:characteristic:local-celsius
CONCEPT=urn:infai:ses:concept:local-temperature
GET_FUNCTION=urn:infai:ses:measuring-function:local-get-temperature
SET_FUNCTION=urn:infai:ses:controlling-function:local-set-temperature
ASPECT=urn:infai:ses:aspect:local-room
DEVICE_CLASS=urn:infai:ses:device-class:local-thermostat
PROTOCOL=urn:infai:ses:protocol:local
SEGMENT=urn:infai:ses:protocol-segment:local-data
DEVICE_TYPE=urn:infai:ses:device-type:local-thermostat
GET_SERVICE=urn:infai:ses:service:local-get-temperature
SET_SERVICE=urn:infai:ses:service:local-set-temperature

# send METHOD PATH TOKEN BODY: fails with the answer of device-repository
send() {
	answer=$(curl -s -w '\n%{http_code}' -X "$1" -H "Authorization: $3" -H 'Content-Type: application/json' -d "$4" "$REPO$2")
	code=$(printf '%s' "$answer" | tail -n 1)
	case $code in
	2*) ;;
	*)
		printf '%s %s: %s\n' "$1" "$2" "$answer" >&2
		exit 1
		;;
	esac
}

send POST /characteristics "$ADMIN" "{\"id\": \"$CELSIUS\", \"name\": \"Celsius\", \"display_unit\": \"°C\", \"type\": \"https://schema.org/Float\"}"
send POST /concepts "$ADMIN" "{\"id\": \"$CONCEPT\", \"name\": \"Temperature\", \"characteristic_ids\": [\"$CELSIUS\"], \"base_characteristic_id\": \"$CELSIUS\"}"
send POST /functions "$ADMIN" "{\"id\": \"$GET_FUNCTION\", \"name\": \"Get Temperature\", \"concept_id\": \"$CONCEPT\", \"rdf_type\": \"https://senergy.infai.org/ontology/MeasuringFunction\"}"
send POST /functions "$ADMIN" "{\"id\": \"$SET_FUNCTION\", \"name\": \"Set Target Temperature\", \"concept_id\": \"$CONCEPT\", \"rdf_type\": \"https://senergy.infai.org/ontology/ControllingFunction\"}"
send POST /aspects "$ADMIN" "{\"id\": \"$ASPECT\", \"name\": \"Room\", \"sub_aspects\": []}"
send POST /device-classes "$ADMIN" "{\"id\": \"$DEVICE_CLASS\", \"name\": \"Thermostat\", \"image\": \"\"}"
send POST /protocols "$ADMIN" "{\"id\": \"$PROTOCOL\", \"name\": \"Local\", \"handler\": \"connector\", \"protocol_segments\": [{\"id\": \"$SEGMENT\", \"name\": \"data\"}]}"
send POST /device-types "$ADMIN" "$(jq -n \
	--arg id "$DEVICE_TYPE" --arg class "$DEVICE_CLASS" --arg protocol "$PROTOCOL" --arg segment "$SEGMENT" \
	--arg get "$GET_SERVICE" --arg set "$SET_SERVICE" --arg getf "$GET_FUNCTION" --arg setf "$SET_FUNCTION" \
	--arg celsius "$CELSIUS" --arg aspect "$ASPECT" '{
	id: $id, name: "Local Thermostat", description: "", device_class_id: $class, attributes: [],
	services: [
		{id: $get, local_id: "get-temperature", name: "Get Temperature", description: "", interaction: "event+request",
		 protocol_id: $protocol, attributes: [], inputs: [], outputs: [
			{id: ($get + ":out"), protocol_segment_id: $segment, serialization: "json", content_variable:
				{id: ($get + ":reading"), name: "reading", type: "https://schema.org/StructuredValue", sub_content_variables: [
					{id: ($get + ":temperature"), name: "temperature", type: "https://schema.org/Float",
					 characteristic_id: $celsius, function_id: $getf, aspect_id: $aspect}]}}]},
		{id: $set, local_id: "set-temperature", name: "Set Target Temperature", description: "", interaction: "request",
		 protocol_id: $protocol, attributes: [], outputs: [], inputs: [
			{id: ($set + ":in"), protocol_segment_id: $segment, serialization: "json", content_variable:
				{id: ($set + ":target"), name: "target", type: "https://schema.org/StructuredValue", sub_content_variables: [
					{id: ($set + ":temperature"), name: "temperature", type: "https://schema.org/Float",
					 characteristic_id: $celsius, function_id: $setf, aspect_id: $aspect}]}}]}
	]}')"
# a user creates a device with POST, which generates its id; found again by its local id
DEVICE=$(curl -sf -H "Authorization: $ALICE" "$REPO/devices/thermostat-1?as=local_id&owner_id=alice" | jq -r .id || true)
if [ -z "$DEVICE" ]; then
	send POST /devices "$ALICE" "{\"local_id\": \"thermostat-1\", \"name\": \"Thermostat 1\", \"device_type_id\": \"$DEVICE_TYPE\", \"attributes\": []}"
	DEVICE=$(printf '%s' "$answer" | sed '$d' | jq -r .id)
fi

echo "device $DEVICE of alice"
echo "  get: $GET_SERVICE ($GET_FUNCTION)"
echo "  set: $SET_SERVICE ($SET_FUNCTION)"
