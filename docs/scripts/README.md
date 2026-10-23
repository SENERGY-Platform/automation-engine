<!-- generated with: go generate ./pkg/scriptdoc - do not edit by hand -->

# Script environments

The engine runs JavaScript with [goja](https://github.com/dop251/goja) (ECMAScript 5.1 with parts of ES6), in two places with different globals. This reference and the files next to it are generated from the Go source: methods and descriptions from `pkg/modules/compat`, types and field names by reflection with the name mapping of the runtime (JSON tags), deprecations from the comments of the models. A test fails when the files do not match the code.

| File | Content |
|---|---|
| `engine/script-env.d.ts` | TypeScript declarations of the BPMN scripts and expressions |
| `modules/script-env.d.ts` | TypeScript declarations of the prescripts and postscripts of module tasks, for editors with type checking (Monaco, VS Code) |
| `modules/jsdoc.js` | the same API as JSDoc |
| `modules/ace-code-completer.ts` | completion for the Ace editor of the web-ui, as before from smart-service-module-worker-lib |

## BPMN scripts and expressions

Applies to script tasks, conditions on sequence flows, input and output mappings, timer values, `senergy:messageName` and the conditions of device and import events (`senergy:script`).

- The variables of the instance and the local variables of the element are globals. In event conditions, they are the value under `senergy:value_variable_name` and the variables from `senergy:variables`.
- The result is the value of the last expression. Conditions must return a boolean.
- Values are JSON: numbers are doubles, missing values `null`.
- `JSON.parse` returns a value that is no string as it is, in module scripts as well. Camunda handed some values over as JSON text that the engine keeps as objects, e.g. the value of a device event, and models parse them.
- Limits per evaluation: run time `SCRIPT_TIMEOUT` (default 2 s, fixed 2 s for event conditions), result up to `MAX_VARIABLE_BYTES` (default 1 MiB), 100 log entries, call depth 1000. No access to the network, the file system or engine internals.

### `log(...args: any[]): void`

Writes the arguments, separated by spaces, to the history of the instance at the element that runs the script (DBG-11). Strings are written as they are, other values as JSON. At most 100 entries per evaluation are kept.

### `console.log(...args: any[]): void`

console.log is the same function as log.

### `setVariable(name: string, value: any): void`

Sets a variable: the local variable of the element if it has one of this name, otherwise the variable of the instance. The value has to be representable as JSON; it is also visible to the rest of the script under its name.

## Prescripts and postscripts of module tasks

Applies to the inputs `prescript*` and `postscript*` of the task types `info`, `import`, `export`, `analytics`, `device_repository`, `process_deployment`, `process_deployment_start` and `watcher`. Parts with the same prefix are joined in the order of their names.

- Order: prescripts with the raw inputs, then the string inputs are rendered as Go templates against the variables (`{{.name}}`), then the task runs, then postscripts with all outputs.
- Variables are those of the instance, for maintenance procedures those of the parent instance. Written variables apply after the script.
- `deviceRepo` asks device-repository with the token of the owner.
- Run time at most 2 s per script.
- `console` writes to the log of the engine, not to the history of the instance.

### `console`

- `dump(): void`<br>Dump writes all smart-service instance variables, the variables changed by this script, process worker inputs and process worker outputs known at the time of the call to the worker log
- `log(...args: any[]): void`<br>Log writes the arguments, separated by spaces, as info message to the worker log; strings are written as they are, everything else as json

### `deviceRepo`

- `getAspect(id: string): Aspect`
- `getAspectNode(id: string): AspectNode`
- `getAspectNodes(): AspectNode[]`
- `getAspectNodesByIdList(ids: string[]): AspectNode[]`
- `getAspectNodesMeasuringFunctions(id: string, ancestors: boolean, descendants: boolean): FunctionType[]`
- `getAspectNodesWithMeasuringFunction(ancestors: boolean, descendants: boolean): AspectNode[]`
- `getAspects(): Aspect[]`
- `getAspectsWithMeasuringFunction(ancestors: boolean, descendants: boolean): Aspect[]`
- `getCharacteristic(id: string): Characteristic`
- `getConceptWithCharacteristics(id: string): ConceptWithCharacteristics`
- `getConceptWithoutCharacteristics(id: string): Concept`
- `getDeviceClass(id: string): DeviceClass`
- `getDeviceClasses(): DeviceClass[]`
- `getDeviceClassesControllingFunctions(id: string): FunctionType[]`
- `getDeviceClassesFunctions(id: string): FunctionType[]`
- `getDeviceClassesWithControllingFunctions(): DeviceClass[]`
- `getDeviceTypeSelectables(query: FilterCriteria[], pathPrefix: string, includeModified: boolean, servicesMustMatchAllCriteria: boolean): DeviceTypeSelectable[]`
- `getFunction(id: string): FunctionType`
- `getFunctionsByType(rdfType: string): FunctionType[]`
- `getLeafCharacteristics(): Characteristic[]`
- `getLocation(id: string): Location`
- `getService(id: string): Service`
- `listDeviceTypes(limit: number, offset: number, sort: string, filter: FilterCriteria[], includeModified: boolean, includeUnmodified: boolean): DeviceType[]`
- `listHubDeviceIds(id: string, asLocalId: boolean): string[]`
- `readDevice(id: string): Device`
- `readDeviceByLocalId(localId: string): Device`
- `readDeviceGroup(id: string): DeviceGroup`
- `readDeviceType(id: string): DeviceType`
- `readHub(id: string): Hub`

### `inputs`

- `exists(name: string): boolean`<br>Exists checks if a process worker input or a variable of the instance exists
- `get(name: string): any`<br>Get value of a process worker input; a name that is no input of the task finds the variable of the instance, e.g. a start parameter
- `list(): any[]`<br>List input values sorted by their names
- `listNames(): string[]`<br>ListNames lists sorted input names

### `outputs`

- `get(name: string): any`<br>Get a process worker output
- `set(name: string, value: any): void`<br>Set a process worker output
- `setJson(name: string, value: any): void`<br>SetJson marshals the given value to json and sets it as a process worker output

### `util`

- `getDevicesWithServiceFromEntityString(entityStr: string, criteria: FilterCriteria[]): IotOption[]`<br>GetDevicesWithServiceFromEntityString finds a list of iot-options where the entity is the same the input, but the Service field is set with those that match the input criteria
- `getDevicesWithServiceFromIotOption(entity: IotOption, criteria: FilterCriteria[]): IotOption[]`<br>GetDevicesWithServiceFromIotOption finds a list of iot-options where the entity is the same the input, but the Service field is set with those that match the input criteria
- `getUserId(): string`<br>GetUserId returns the user-id of the executing user
- `getUserToken(): string`<br>GetUserToken returns a jwt-token for the executing user
- `groupIotOptionsByDevice(entities: IotOption[]): Record<string, IotOption[]>`<br>GroupIotOptionsByDevice groups a list of IotOption by their device id; options that are not devices will be grouped under ""
- `groupIotOptionsByService(entities: IotOption[]): Record<string, IotOption[]>`<br>GroupIotOptionsByService groups a list of IotOption by their service id; options that are not devices or dont hav a service-id will be grouped under ""
- `isDeviceGroupIotOption(entity: IotOption): boolean`<br>IsDeviceGroupIotOption checks if the input is a device-group
- `isDeviceGroupIotOptionStr(entityStr: string): boolean`<br>IsDeviceGroupIotOptionStr checks if the input is a device-group
- `isDeviceIotOption(entity: IotOption): boolean`<br>IsDeviceIotOption checks if the input is a device
- `isDeviceIotOptionStr(entityStr: string): boolean`<br>IsDeviceIotOptionStr checks if the input is a device
- `isImportIotOption(entity: IotOption): boolean`<br>IsImportIotOption checks if the input is a import
- `isImportIotOptionStr(entityStr: string): boolean`<br>IsImportIotOptionStr checks if the input is a import

### `variables`

- `derefName(ref: string): string`<br>DerefName returns the name of a smart-service instance variable referenced in parameter ref
- `derefTemplate(templ: string): string`<br>DerefTemplate replaces variable references in the input string with the corresponding variable values
- `derefValue(ref: string): any`<br>DerefValue returns the value of a smart-service instance variable referenced in parameter ref
- `exists(name: string): boolean`<br>Exists checks if a smart-service instance variable exists
- `read(name: string): any`<br>Read value of a smart-service instance variable
- `ref(name: string): string`<br>Ref creates a reference to a variable (e.g. "my_var_name" --> "{{.my_var_name}}") throws exception if variable is unknown
- `write(name: string, value: any): void`<br>Write value as smart-service instance variable

### Types

#### `Aspect`

- `id: string`
- `name: string`
- `aspect_class_id: string`
- `sub_aspects: Aspect[]`

#### `AspectNode`

- `id: string`
- `name: string`
- `aspect_class_id: string`
- `root_id: string`
- `parent_id: string`
- `child_ids: string[]`
- `ancestor_ids: string[]`
- `descendent_ids: string[]`

#### `Attribute`

- `key: string`
- `value: string`
- `origin: string`

#### `Characteristic`

- `id: string`
- `name: string`
- `display_unit: string`
- `type: string`
- `min_value: any`
- `max_value: any`
- `allowed_values: any[]`
- `value: any`
- `sub_characteristics: Characteristic[]`

#### `Concept`

- `id: string`
- `name: string`
- `characteristic_ids: string[]`
- `base_characteristic_id: string`
- `conversions: ConverterExtension[]`

#### `ConceptWithCharacteristics`

- `id: string`
- `name: string`
- `base_characteristic_id: string`
- `characteristics: Characteristic[]`
- `conversions: ConverterExtension[]`

#### `Configurable`

- `path: string`
- `characteristic_id: string`
- `aspect_node: AspectNode` (deprecated: alias for a single element AspectNodes; holds the node with the alphabetically first id)
- `aspect_nodes: AspectNode[]`
- `function_id: string`
- `value: any`
- `type: string`

#### `Content`

- `id: string`
- `content_variable: ContentVariable`
- `serialization: string`
- `protocol_segment_id: string`

#### `ContentVariable`

- `id: string`
- `name: string`
- `is_void: boolean`
- `omit_empty: boolean`
- `type: string`
- `sub_content_variables: ContentVariable[]`
- `characteristic_id: string`
- `value: any`
- `serialization_options: string[]`
- `unit_reference: string`
- `function_id: string`
- `aspect_id: string` (deprecated: please use AspectIds)
- `aspect_ids: string[]`

#### `ConverterExtension`

- `from: string`
- `to: string`
- `distance: number`
- `formula: string`
- `placeholder_name: string`

#### `Device`

- `id: string`
- `local_id: string`
- `name: string`
- `attributes: Attribute[]`
- `device_type_id: string`
- `owner_id: string`

#### `DeviceClass`

- `id: string`
- `image: string`
- `name: string`

#### `DeviceGroup`

- `id: string`
- `name: string`
- `image: string`
- `criteria: DeviceGroupFilterCriteria[]`
- `device_ids: string[]`
- `criteria_short: string[]`
- `attributes: Attribute[]`
- `auto_generated_by_device: string`

#### `DeviceGroupFilterCriteria`

- `interaction: string`
- `function_id: string`
- `aspect_id: string` (deprecated: please use AspectIds)
- `aspect_ids: string[]`
- `device_class_id: string`

#### `DeviceGroupSelection`

- `id: string`

#### `DeviceSelection`

- `device_id: string`
- `service_id: string | null`
- `path: string | null`
- `characteristic_id: string | null`

#### `DeviceType`

- `id: string`
- `name: string`
- `description: string`
- `service_groups: ServiceGroup[]`
- `services: Service[]`
- `device_class_id: string`
- `attributes: Attribute[]`

#### `DeviceTypeSelectable`

- `device_type_id: string`
- `services: Service[]`
- `service_path_options: Record<string, ServicePathOption[]>`

#### `FilterCriteria`

- `interaction: string`
- `function_id: string`
- `device_class_id: string`
- `aspect_id: string` (deprecated: alias for a single element AspectIds; normalized into AspectIds at the controller boundary)
- `aspect_ids: string[]`

#### `FunctionType`

- `id: string`
- `name: string`
- `display_name: string`
- `description: string`
- `concept_id: string`
- `rdf_type: string`

#### `GenericEventSource`

- `filter_type: string`
- `filter_ids: string`
- `topic: string`
- `path: string`
- `characteristic_id: string | null`

#### `Hub`

- `id: string`
- `name: string`
- `hash: string`
- `device_local_ids: string[]`
- `device_ids: string[]`
- `owner_id: string`
- `attributes: Attribute[]`

#### `ImportSelection`

- `id: string`
- `path: string | null`
- `characteristic_id: string | null`

#### `IotOption`

- `device_selection: DeviceSelection | null`
- `device_group_selection: DeviceGroupSelection | null`
- `import_selection: ImportSelection | null`
- `generic_event_source: GenericEventSource | null`

#### `Location`

- `id: string`
- `name: string`
- `description: string`
- `image: string`
- `device_ids: string[]`
- `device_group_ids: string[]`

#### `Service`

- `id: string`
- `local_id: string`
- `name: string`
- `description: string`
- `interaction: string`
- `protocol_id: string`
- `inputs: Content[]`
- `outputs: Content[]`
- `attributes: Attribute[]`
- `service_group_key: string`

#### `ServiceGroup`

- `key: string`
- `name: string`
- `description: string`

#### `ServicePathOption`

- `service_id: string`
- `path: string`
- `characteristic_id: string`
- `aspect_node: AspectNode` (deprecated: alias for a single element AspectNodes; holds the node with the alphabetically first id)
- `aspect_nodes: AspectNode[]`
- `function_id: string`
- `is_void: boolean`
- `value: any`
- `is_controlling_function: boolean`
- `configurables: Configurable[]`
- `type: string`
- `interaction: string`

