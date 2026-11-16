# Connection state of a device

How a process waits for the connection state of a device to change, and how it reads the state at one moment (SNRGY-5151, since 2026-11-16).

## Scope

Holds for the catch event with a connection selection and for the task type `device_connection`, in the cloud and on a gateway (`MODE=fog`). Not this: the data of a device (`event/...`, the service topics), which events and commands handle as before, and the connection history, which the engine does not keep.

## The terms

- **Connection state**: `online` or `offline`. A device the source does not know, or a state other than these two, is `unknown`; only the task reports `unknown`, an event never has that value.
- **Device log**: the Kafka topic `device_log` (`DEVICE_LOG_TOPIC`), compacted, key the platform device id, message `{"id", "connected", "time"}`. platform-connector writes it, the cloud source of the engine reads it.
- **device-manager**: on a gateway the service the device connectors report to over MQTT and that answers `GET /devices` and `GET /devices/{local id}` over REST. The engine finds it at `DEVICE_MANAGER_URL`.
- **Local id**: the id of a device at its connector, `local_id` in device-repository. A gateway knows its devices only by it.

## The model

- **Event**: an intermediate or boundary catch event without `messageRef`, like an event on device data, with a `script` condition (`value_variable_name`, `variables`, `qos` as for devices). It fires when a matching message arrives while the token waits; it does not look at the state the device had before.
- **Task**: a service task with the task type `device_connection`. It reads the state when it runs and completes with the output `result`, `"online"`, `"offline"` or `"unknown"`.

An event and a task together make the usual pattern: read the state, decide, then wait for the change. A change between reading and waiting is lost, see the limits.

## The selection

The device alone, without service, path or group. It is chosen at deploy, like the device of a device event.

- **Native** (`POST /deployments`, `selections` of the element, for the event and the task): `{"connection_device_id": "<platform device id>"}`. Exactly one source per element; a service, a path, a group or a second source is rejected. A `device_connection` task without it is rejected.
- **Smart-service module** `process_deployment` (input `deploy.<element>.selection`): `{"connection_device_id": "<id>"}`, or `{"device_connection": <iot option>}` with the option of an iot form field, which names the device in `device_selection.device_id`. The service, path and characteristic of the option are ignored, because the connection belongs to the device: a form field whose user picked a value of a service still names the device. A selection that names the connection of a device keeps the deployment on a gateway that carries the device, as one with a device does.
- **Old deploy format** (`/deprecated/deployment`, as the deploy dialog of the web-ui sends it): for an event `selected_generic_event_source: {"filter_type": "connectionState", "filter_ids": "<device id>"}`; the topic is ignored on input and empty on output, the filter type is compared without regard to case, `filter_ids` holds exactly one id. For a `device_connection` task `selected_device_id` without service. The prepared deployment lists the task with the devices the owner may read as options, only the devices of the gateway when it is prepared for one; the options of events stay those of device-selection, the dialog adds the connection itself.

At deploy the owner needs the right to read the device (`permissions-v2`, topic `devices`), as for a device command: `Access.Check` rejects a selection of a device the owner may not read.

## Value and variables

The value of the event is the text `online` or `offline`. The condition reads it as `value`, or as the name given by `value_variable_name`, and compares: `value == 'online'`. The local variables are:

| Variable | Content |
|---|---|
| `event` | the value, as for devices |
| `age` | seconds since the time of the message |
| `msg_id` | the identifier of the message |
| `device_id` | the platform device id |

The condition runs once per message. The maximum age of a message and the bound by the creation of the version apply as for devices.

## The task

In the cloud the task reads `connection_state` of the extended device from device-repository with the token of the owner; an empty state is `unknown`. On a gateway it reads the device from the mirror for its local id, then `GET <DEVICE_MANAGER_URL>/devices/<local id>`; `404` is `unknown`. Without `DEVICE_MANAGER_URL` on a gateway the task fails with a message that names the setting. The task is in the allowlist of the namespace `processes` of a new database; existing databases need it added (see `OPEN-ITEMS.md`).

## The sources

**Cloud.** The event worker reads `DEVICE_LOG_TOPIC` in `EVENT_CONSUMER_GROUP`, together with the service topics, as long as a description names a device connection. It correlates by the device id of the message with the description; a device without description is ignored. The device is read with the token of the owner at deploy, which also confirms the right.

**Gateway.** The engine subscribes with QoS 1 to `device-manager/device/+` and `device-manager/device/+/lw` on the broker it uses for `event/#`.

- A message `{"method": "set", "device_id": "<local id>", "data": {"state": "online"|"offline"}}` sets the state of the device and remembers the connector that sent it; `"method": "delete"` makes the engine forget the device.
- The last will, payload `offline`, marks every device of that connector that the engine knows as online as offline.
- A message that arrives with the retain flag is a replay of the broker, not a change: at every subscribe and after every reconnect the broker sends what connectors left retained (`set`, `delete` and `.../lw` alike). Such a message records the connector of the device, sets the state only of a device no live message has named since the start, and triggers nothing. Live messages arrive without the flag, also a forward of a retained publish.
- At start the engine reads `GET <DEVICE_MANAGER_URL>/devices` once, tolerant to both implementations of the device-manager (the connector is `module_id` or `ref`), and takes that as the state each device had. This produces no event. It replaces a state that is known only from retained messages, because a connector that died leaves its retained `online` behind; it does not replace a state a live message has set since the subscription.
- Only a change in a live message is passed on. A connector that reports the same state again triggers nothing.

## Latency

Cloud: the event is as fast as the connector writes the device log. A disconnect is written at once. The device-repository is behind: connection-log-worker holds a change for a window (10 s) before it writes to the repository, so the task can still read the old state shortly after the event has fired. Only the task sees that window, the event does not.

Gateway: the time between the connector's message and the evaluation, usually below a second. The task asks the device-manager, which has the state of its last message.

## Limits

- **Edge, not level.** The event fires on a change that arrives while the token waits. A device that is already online when the token reaches the event does not fire it. The task before the event narrows this, but a change between reading and waiting is lost.
- **Repeated cloud messages.** The device log may repeat a state; each message triggers one evaluation, and the condition decides. A condition on `online` can fire again for a device that stays online and reconnects its channel.
- **Replays are not changes.** A state change during a disconnect of the engine from the MQTT broker is not seen as an event. A replay never replaces what a live message told since the start of the engine, because it can be older: a client that publishes its birth message retained and goes offline through its last will leaves a stale "online" on the broker. A replay sets the state only of a device no live message has named yet, and the snapshot replaces it.
- **Gateway without snapshot.** If `DEVICE_MANAGER_URL` is not set or the device-manager does not answer at start, the engine has no earlier state, and the first live message of each device counts as a change.
- **Gateway with an older engine.** Descriptions reach a gateway through the synchronisation. An engine from before this feature stores them but never evaluates them: the event does not fire, and no error is reported. There is no version gate.
- **Last will.** A connector whose last will is not on `device-manager/device/{connector}/lw` leaves its devices online when it dies; offline arrives only when the connector says so itself.
- **Local ids.** The local id the connector uses must equal `local_id` in device-repository. Topics of device data rely on the same assumption (`LDH_ID_PREFIX` empty).
- **History.** The debug view neither reconstructs these events nor shows a current value: the connection state is not in Timescale.
