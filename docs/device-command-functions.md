# Device commands with several functions

How a `device_command` task sets several values of one device in one message (SNRGY-5157, since 2026-11-17).

## Scope

Holds for device commands whose payload has `additional_functions`, in the cloud and on a gateway (`MODE=fog`), with every completion strategy and group scheduler. A payload without `additional_functions` is not affected. **Not this if** the values are meant for different services or devices: each command still goes to one service per device.

Some devices read a value only together with the other values of the same message. zigbee2mqtt applies `transition` to the `brightness` or `color_temp` of the same set message; sent alone, it has no effect. A device command with one function per task sends one message per task and loses that.

## The terms

- **Primary function**: `function`, `characteristic_id`, `aspects` and `input` of the payload, as in every device command. The device selection, `selection.path` and the prepared deployment refer to it.
- **Additional function**: an entry of `additional_functions`, with a name, a function, a characteristic, aspects and a default value.
- **Serves**: a service serves a function if one of its input content variables carries the function and every aspect of the entry, by the aspect itself or a descendant (`commons.ContentsMatch`), as for the primary function.

## The payload

```json
{
  "version": 3,
  "function": {"id": "urn:infai:ses:controlling-function:<brightness>"},
  "characteristic_id": "urn:infai:ses:characteristic:<percent>",
  "aspects": [{"id": "urn:infai:ses:aspect:<light>"}],
  "input": 0,
  "additional_functions": [
    {"name": "transition",
     "function": {"id": "urn:infai:ses:controlling-function:<transition time>"},
     "characteristic_id": "urn:infai:ses:characteristic:<seconds>",
     "aspects": [{"id": "urn:infai:ses:aspect:<light>"}],
     "input": 0}
  ]
}
```

- **Name**: not empty, unique within the task, without `.` and `:`.
- **Functions**: all controlling, the primary one included. A measuring function cannot be combined with others.
- **Version**: a command with additional functions is marshalled as version 3 or later, whatever the payload says. The `configurables` of a payload before version 3 go only through the marshalling of version 2, so a payload with them cannot carry additional functions; the deploy refuses it, a payload computed by a script becomes an incident.

## The values

The task inputs set the values when the task runs, with the conversion to the type of the default value that `inputs` has always had:

| Input | Sets |
|---|---|
| `inputs` | the value of the primary function |
| `inputs.<path>` | a field inside the value of the primary function |
| `inputs:<name>` | the value of the additional function `<name>` |
| `inputs:<name>.<path>` | a field inside that value |

At runtime an input that names no additional function is ignored with a warning, as an input whose path does not fit has always been.

## The message

Each sub-task, one per device and service, makes one call to the marshaller (`MarshalV2`) with one entry per function: the primary function first, then the additional functions in their order, then the configurables of `selection.path`. An additional function without a value whose function has no concept is left out, as the primary function is. The marshaller writes all values into the one message of the service. The protocol message (`ProtocolMsg`) is unchanged; connectors see one command with one input, as before. On a gateway the marshaller runs in the process (`localmarshaller.go`) and receives the same list.

## The service

A command with additional functions goes only to a service that serves all of its functions:

- **Device and service selected**: the service has to serve all of them. If it does not, the task becomes an incident without retry, `service <id> of device <id> does not serve <functions>`.
- **Device without service**: every service of the device that serves all of them gets the command, as every matching service does for one function.
- **Device group**: per member, every service that serves all of them. A member without such a service gets no command; the order and concurrency of the group are those of `docs/device-command-groups.md`.

## Deploy

Checked when a deployment is created or updated, in this order:

1. **The payload**, without other services: names, functions, controlling functions, and every task input `inputs:<name>` against the names of the payload. A payload computed by a script is checked only when the task runs.
2. **The selection**, with the token of the user and device-repository, for admins as well: a selected service serves all functions; a selected device has a service that does; a selected group has at least one member with such a service. A failed check names the element and the functions, `no service of device <id> serves all of <functions>`, `no device of group <id> has a service that serves all of <functions>`. A selected device or group the user cannot read, and an aspect device-repository does not know, are reported at the element as well. A device named only in the payload is checked when the task runs.

The prepared deployment asks device-selection with all functions of the element and offers only services that serve all of them; a device without such a service is not offered. Its `criteria` stay those of the primary function, in the native and in the old format.

## Answer and result

As for every controlling command: the task completes with the answer of the device under `pessimistic` and on sending under `optimistic`, the result is `null`.

## Gateways with an older engine

The payload goes to the gateway with the deployment. An engine before this change does not know `additional_functions`, ignores them and sends the primary function alone, without an error. A process with additional functions belongs on a gateway whose process module carries an engine that knows them.

## Designers

The process designer of the web-ui writes and keeps `additional_functions` and the inputs `inputs:<name>`. The smart service designer generates the iot form field of such a task with one criterion per function, so that the form offers only devices with all of them.
