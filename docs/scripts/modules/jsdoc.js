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

/*
 * generated in github.com/SENERGY-Platform/automation-engine with:
 * go generate ./pkg/scriptdoc
 *
 * the script API of pre- and postscripts of module tasks. property names are the names the
 * script runtime exposes, the json tag names of the underlying go structs.
 */


/**
 * @namespace console
 */

/**
 * @namespace deviceRepo
 */

/**
 * @namespace inputs
 */

/**
 * @namespace outputs
 */

/**
 * @namespace util
 */

/**
 * @namespace variables
 */

/**
 * Dump writes all smart-service instance variables, the variables changed by this script, process worker inputs and process worker outputs known at the time of the call to the worker log
 * @function console#dump
 * @example
 * console.dump()
 */

/**
 * Log writes the arguments, separated by spaces, as info message to the worker log; strings are written as they are, everything else as json
 * @function console#log
 * @param { ...Object } args
 * @example
 * console.log(args_as_any)
 */

/**
 * @function deviceRepo#getAspect
 * @param { string } id
 * @returns { Aspect }
 * @example
 * deviceRepo.getAspect(id_as_string)
 */

/**
 * @function deviceRepo#getAspectNode
 * @param { string } id
 * @returns { AspectNode }
 * @example
 * deviceRepo.getAspectNode(id_as_string)
 */

/**
 * @function deviceRepo#getAspectNodes
 * @returns { AspectNode[] }
 * @example
 * deviceRepo.getAspectNodes()
 */

/**
 * @function deviceRepo#getAspectNodesByIdList
 * @param { string[] } ids
 * @returns { AspectNode[] }
 * @example
 * deviceRepo.getAspectNodesByIdList(ids_as_string_list)
 */

/**
 * @function deviceRepo#getAspectNodesMeasuringFunctions
 * @param { string } id
 * @param { boolean } ancestors
 * @param { boolean } descendants
 * @returns { FunctionType[] }
 * @example
 * deviceRepo.getAspectNodesMeasuringFunctions(id_as_string, ancestors_as_boolean, descendants_as_boolean)
 */

/**
 * @function deviceRepo#getAspectNodesWithMeasuringFunction
 * @param { boolean } ancestors
 * @param { boolean } descendants
 * @returns { AspectNode[] }
 * @example
 * deviceRepo.getAspectNodesWithMeasuringFunction(ancestors_as_boolean, descendants_as_boolean)
 */

/**
 * @function deviceRepo#getAspects
 * @returns { Aspect[] }
 * @example
 * deviceRepo.getAspects()
 */

/**
 * @function deviceRepo#getAspectsWithMeasuringFunction
 * @param { boolean } ancestors
 * @param { boolean } descendants
 * @returns { Aspect[] }
 * @example
 * deviceRepo.getAspectsWithMeasuringFunction(ancestors_as_boolean, descendants_as_boolean)
 */

/**
 * @function deviceRepo#getCharacteristic
 * @param { string } id
 * @returns { Characteristic }
 * @example
 * deviceRepo.getCharacteristic(id_as_string)
 */

/**
 * @function deviceRepo#getConceptWithCharacteristics
 * @param { string } id
 * @returns { ConceptWithCharacteristics }
 * @example
 * deviceRepo.getConceptWithCharacteristics(id_as_string)
 */

/**
 * @function deviceRepo#getConceptWithoutCharacteristics
 * @param { string } id
 * @returns { Concept }
 * @example
 * deviceRepo.getConceptWithoutCharacteristics(id_as_string)
 */

/**
 * @function deviceRepo#getDeviceClass
 * @param { string } id
 * @returns { DeviceClass }
 * @example
 * deviceRepo.getDeviceClass(id_as_string)
 */

/**
 * @function deviceRepo#getDeviceClasses
 * @returns { DeviceClass[] }
 * @example
 * deviceRepo.getDeviceClasses()
 */

/**
 * @function deviceRepo#getDeviceClassesControllingFunctions
 * @param { string } id
 * @returns { FunctionType[] }
 * @example
 * deviceRepo.getDeviceClassesControllingFunctions(id_as_string)
 */

/**
 * @function deviceRepo#getDeviceClassesFunctions
 * @param { string } id
 * @returns { FunctionType[] }
 * @example
 * deviceRepo.getDeviceClassesFunctions(id_as_string)
 */

/**
 * @function deviceRepo#getDeviceClassesWithControllingFunctions
 * @returns { DeviceClass[] }
 * @example
 * deviceRepo.getDeviceClassesWithControllingFunctions()
 */

/**
 * @function deviceRepo#getDeviceTypeSelectables
 * @param { FilterCriteria[] } query
 * @param { string } pathPrefix
 * @param { boolean } includeModified
 * @param { boolean } servicesMustMatchAllCriteria
 * @returns { DeviceTypeSelectable[] }
 * @example
 * deviceRepo.getDeviceTypeSelectables(query_as_FilterCriteria_list, pathPrefix_as_string, includeModified_as_boolean, servicesMustMatchAllCriteria_as_boolean)
 */

/**
 * @function deviceRepo#getFunction
 * @param { string } id
 * @returns { FunctionType }
 * @example
 * deviceRepo.getFunction(id_as_string)
 */

/**
 * @function deviceRepo#getFunctionsByType
 * @param { string } rdfType
 * @returns { FunctionType[] }
 * @example
 * deviceRepo.getFunctionsByType(rdfType_as_string)
 */

/**
 * @function deviceRepo#getLeafCharacteristics
 * @returns { Characteristic[] }
 * @example
 * deviceRepo.getLeafCharacteristics()
 */

/**
 * @function deviceRepo#getLocation
 * @param { string } id
 * @returns { Location }
 * @example
 * deviceRepo.getLocation(id_as_string)
 */

/**
 * @function deviceRepo#getService
 * @param { string } id
 * @returns { Service }
 * @example
 * deviceRepo.getService(id_as_string)
 */

/**
 * @function deviceRepo#listDeviceTypes
 * @param { number } limit
 * @param { number } offset
 * @param { string } sort
 * @param { FilterCriteria[] } filter
 * @param { boolean } includeModified
 * @param { boolean } includeUnmodified
 * @returns { DeviceType[] }
 * @example
 * deviceRepo.listDeviceTypes(limit_as_number, offset_as_number, sort_as_string, filter_as_FilterCriteria_list, includeModified_as_boolean, includeUnmodified_as_boolean)
 */

/**
 * @function deviceRepo#listHubDeviceIds
 * @param { string } id
 * @param { boolean } asLocalId
 * @returns { string[] }
 * @example
 * deviceRepo.listHubDeviceIds(id_as_string, asLocalId_as_boolean)
 */

/**
 * @function deviceRepo#readDevice
 * @param { string } id
 * @returns { Device }
 * @example
 * deviceRepo.readDevice(id_as_string)
 */

/**
 * @function deviceRepo#readDeviceByLocalId
 * @param { string } localId
 * @returns { Device }
 * @example
 * deviceRepo.readDeviceByLocalId(localId_as_string)
 */

/**
 * @function deviceRepo#readDeviceGroup
 * @param { string } id
 * @returns { DeviceGroup }
 * @example
 * deviceRepo.readDeviceGroup(id_as_string)
 */

/**
 * @function deviceRepo#readDeviceType
 * @param { string } id
 * @returns { DeviceType }
 * @example
 * deviceRepo.readDeviceType(id_as_string)
 */

/**
 * @function deviceRepo#readHub
 * @param { string } id
 * @returns { Hub }
 * @example
 * deviceRepo.readHub(id_as_string)
 */

/**
 * Exists checks if a process worker input exists
 * @function inputs#exists
 * @param { string } name
 * @returns { boolean }
 * @example
 * inputs.exists(name_as_string)
 */

/**
 * Get value of a process worker input
 * @function inputs#get
 * @param { string } name
 * @returns { Object }
 * @example
 * inputs.get(name_as_string)
 */

/**
 * List input values sorted by their names
 * @function inputs#list
 * @returns { Object[] }
 * @example
 * inputs.list()
 */

/**
 * ListNames lists sorted input names
 * @function inputs#listNames
 * @returns { string[] }
 * @example
 * inputs.listNames()
 */

/**
 * Get a process worker output
 * @function outputs#get
 * @param { string } name
 * @returns { Object }
 * @example
 * outputs.get(name_as_string)
 */

/**
 * Set a process worker output
 * @function outputs#set
 * @param { string } name
 * @param { Object } value
 * @example
 * outputs.set(name_as_string, value_as_any)
 */

/**
 * SetJson marshals the given value to json and sets it as a process worker output
 * @function outputs#setJson
 * @param { string } name
 * @param { Object } value
 * @example
 * outputs.setJson(name_as_string, value_as_any)
 */

/**
 * GetDevicesWithServiceFromEntityString finds a list of iot-options where the entity is the same the input, but the Service field is set with those that match the input criteria
 * @function util#getDevicesWithServiceFromEntityString
 * @param { string } entityStr
 * @param { FilterCriteria[] } criteria
 * @returns { IotOption[] }
 * @example
 * util.getDevicesWithServiceFromEntityString(entityStr_as_string, criteria_as_FilterCriteria_list)
 */

/**
 * GetDevicesWithServiceFromIotOption finds a list of iot-options where the entity is the same the input, but the Service field is set with those that match the input criteria
 * @function util#getDevicesWithServiceFromIotOption
 * @param { IotOption } entity
 * @param { FilterCriteria[] } criteria
 * @returns { IotOption[] }
 * @example
 * util.getDevicesWithServiceFromIotOption(entity_as_IotOption, criteria_as_FilterCriteria_list)
 */

/**
 * GetUserId returns the user-id of the executing user
 * @function util#getUserId
 * @returns { string }
 * @example
 * util.getUserId()
 */

/**
 * GetUserToken returns a jwt-token for the executing user
 * @function util#getUserToken
 * @returns { string }
 * @example
 * util.getUserToken()
 */

/**
 * GroupIotOptionsByDevice groups a list of IotOption by their device id; options that are not devices will be grouped under ""
 * @function util#groupIotOptionsByDevice
 * @param { IotOption[] } entities
 * @returns { Map<string,IotOption[]> }
 * @example
 * util.groupIotOptionsByDevice(entities_as_IotOption_list)
 */

/**
 * GroupIotOptionsByService groups a list of IotOption by their service id; options that are not devices or dont hav a service-id will be grouped under ""
 * @function util#groupIotOptionsByService
 * @param { IotOption[] } entities
 * @returns { Map<string,IotOption[]> }
 * @example
 * util.groupIotOptionsByService(entities_as_IotOption_list)
 */

/**
 * IsDeviceGroupIotOption checks if the input is a device-group
 * @function util#isDeviceGroupIotOption
 * @param { IotOption } entity
 * @returns { boolean }
 * @example
 * util.isDeviceGroupIotOption(entity_as_IotOption)
 */

/**
 * IsDeviceGroupIotOptionStr checks if the input is a device-group
 * @function util#isDeviceGroupIotOptionStr
 * @param { string } entityStr
 * @returns { boolean }
 * @example
 * util.isDeviceGroupIotOptionStr(entityStr_as_string)
 */

/**
 * IsDeviceIotOption checks if the input is a device
 * @function util#isDeviceIotOption
 * @param { IotOption } entity
 * @returns { boolean }
 * @example
 * util.isDeviceIotOption(entity_as_IotOption)
 */

/**
 * IsDeviceIotOptionStr checks if the input is a device
 * @function util#isDeviceIotOptionStr
 * @param { string } entityStr
 * @returns { boolean }
 * @example
 * util.isDeviceIotOptionStr(entityStr_as_string)
 */

/**
 * IsImportIotOption checks if the input is a import
 * @function util#isImportIotOption
 * @param { IotOption } entity
 * @returns { boolean }
 * @example
 * util.isImportIotOption(entity_as_IotOption)
 */

/**
 * IsImportIotOptionStr checks if the input is a import
 * @function util#isImportIotOptionStr
 * @param { string } entityStr
 * @returns { boolean }
 * @example
 * util.isImportIotOptionStr(entityStr_as_string)
 */

/**
 * DerefName returns the name of a smart-service instance variable referenced in parameter ref
 * @function variables#derefName
 * @param { string } ref
 * @returns { string }
 * @example
 * variables.derefName(ref_as_string)
 */

/**
 * DerefTemplate replaces variable references in the input string with the corresponding variable values
 * @function variables#derefTemplate
 * @param { string } templ
 * @returns { string }
 * @example
 * variables.derefTemplate(templ_as_string)
 */

/**
 * DerefValue returns the value of a smart-service instance variable referenced in parameter ref
 * @function variables#derefValue
 * @param { string } ref
 * @returns { Object }
 * @example
 * variables.derefValue(ref_as_string)
 */

/**
 * Exists checks if a smart-service instance variable exists
 * @function variables#exists
 * @param { string } name
 * @returns { boolean }
 * @example
 * variables.exists(name_as_string)
 */

/**
 * Read value of a smart-service instance variable
 * @function variables#read
 * @param { string } name
 * @returns { Object }
 * @example
 * variables.read(name_as_string)
 */

/**
 * Ref creates a reference to a variable (e.g. "my_var_name" --> "{{.my_var_name}}")
 * throws exception if variable is unknown
 * @function variables#ref
 * @param { string } name
 * @returns { string }
 * @example
 * variables.ref(name_as_string)
 */

/**
 * Write value as smart-service instance variable
 * @function variables#write
 * @param { string } name
 * @param { Object } value
 * @example
 * variables.write(name_as_string, value_as_any)
 */

/**
 * @typedef {Object} Aspect
 * @property { string } id
 * @property { string } name
 * @property { string } aspect_class_id
 * @property { Aspect[] } sub_aspects
 */

/**
 * @typedef {Object} AspectNode
 * @property { string } id
 * @property { string } name
 * @property { string } aspect_class_id
 * @property { string } root_id
 * @property { string } parent_id
 * @property { string[] } child_ids
 * @property { string[] } ancestor_ids
 * @property { string[] } descendent_ids
 */

/**
 * @typedef {Object} Attribute
 * @property { string } key
 * @property { string } value
 * @property { string } origin
 */

/**
 * @typedef {Object} Characteristic
 * @property { string } id
 * @property { string } name
 * @property { string } display_unit
 * @property { string } type
 * @property { Object|null } min_value
 * @property { Object|null } max_value
 * @property { Object|null[] } allowed_values
 * @property { Object|null } value
 * @property { Characteristic[] } sub_characteristics
 */

/**
 * @typedef {Object} Concept
 * @property { string } id
 * @property { string } name
 * @property { string[] } characteristic_ids
 * @property { string } base_characteristic_id
 * @property { ConverterExtension[] } conversions
 */

/**
 * @typedef {Object} ConceptWithCharacteristics
 * @property { string } id
 * @property { string } name
 * @property { string } base_characteristic_id
 * @property { Characteristic[] } characteristics
 * @property { ConverterExtension[] } conversions
 */

/**
 * @typedef {Object} Configurable
 * @property { string } path
 * @property { string } characteristic_id
 * @property { AspectNode } aspect_node deprecated: alias for a single element AspectNodes; holds the node with the alphabetically first id
 * @property { AspectNode[] } aspect_nodes
 * @property { string } function_id
 * @property { Object|null } value
 * @property { string } type
 */

/**
 * @typedef {Object} Content
 * @property { string } id
 * @property { ContentVariable } content_variable
 * @property { string } serialization
 * @property { string } protocol_segment_id
 */

/**
 * @typedef {Object} ContentVariable
 * @property { string } id
 * @property { string } name
 * @property { boolean } is_void
 * @property { boolean } omit_empty
 * @property { string } type
 * @property { ContentVariable[] } sub_content_variables
 * @property { string } characteristic_id
 * @property { Object|null } value
 * @property { string[] } serialization_options
 * @property { string } unit_reference
 * @property { string } function_id
 * @property { string } aspect_id deprecated: please use AspectIds
 * @property { string[] } aspect_ids
 */

/**
 * @typedef {Object} ConverterExtension
 * @property { string } from
 * @property { string } to
 * @property { number } distance
 * @property { string } formula
 * @property { string } placeholder_name
 */

/**
 * @typedef {Object} Device
 * @property { string } id
 * @property { string } local_id
 * @property { string } name
 * @property { Attribute[] } attributes
 * @property { string } device_type_id
 * @property { string } owner_id
 */

/**
 * @typedef {Object} DeviceClass
 * @property { string } id
 * @property { string } image
 * @property { string } name
 */

/**
 * @typedef {Object} DeviceGroup
 * @property { string } id
 * @property { string } name
 * @property { string } image
 * @property { DeviceGroupFilterCriteria[] } criteria
 * @property { string[] } device_ids
 * @property { string[] } criteria_short
 * @property { Attribute[] } attributes
 * @property { string } auto_generated_by_device
 */

/**
 * @typedef {Object} DeviceGroupFilterCriteria
 * @property { string } interaction
 * @property { string } function_id
 * @property { string } aspect_id deprecated: please use AspectIds
 * @property { string[] } aspect_ids
 * @property { string } device_class_id
 */

/**
 * @typedef {Object} DeviceGroupSelection
 * @property { string } id
 */

/**
 * @typedef {Object} DeviceSelection
 * @property { string } device_id
 * @property { string|null } service_id
 * @property { string|null } path
 * @property { string|null } characteristic_id
 */

/**
 * @typedef {Object} DeviceType
 * @property { string } id
 * @property { string } name
 * @property { string } description
 * @property { ServiceGroup[] } service_groups
 * @property { Service[] } services
 * @property { string } device_class_id
 * @property { Attribute[] } attributes
 */

/**
 * @typedef {Object} DeviceTypeSelectable
 * @property { string } device_type_id
 * @property { Service[] } services
 * @property { Map<string,ServicePathOption[]> } service_path_options
 */

/**
 * @typedef {Object} FilterCriteria
 * @property { string } interaction
 * @property { string } function_id
 * @property { string } device_class_id
 * @property { string } aspect_id deprecated: alias for a single element AspectIds; normalized into AspectIds at the controller boundary
 * @property { string[] } aspect_ids
 */

/**
 * @typedef {Object} FunctionType
 * @property { string } id
 * @property { string } name
 * @property { string } display_name
 * @property { string } description
 * @property { string } concept_id
 * @property { string } rdf_type
 */

/**
 * @typedef {Object} GenericEventSource
 * @property { string } filter_type
 * @property { string } filter_ids
 * @property { string } topic
 * @property { string } path
 * @property { string|null } characteristic_id
 */

/**
 * @typedef {Object} Hub
 * @property { string } id
 * @property { string } name
 * @property { string } hash
 * @property { string[] } device_local_ids
 * @property { string[] } device_ids
 * @property { string } owner_id
 * @property { Attribute[] } attributes
 */

/**
 * @typedef {Object} ImportSelection
 * @property { string } id
 * @property { string|null } path
 * @property { string|null } characteristic_id
 */

/**
 * @typedef {Object} IotOption
 * @property { DeviceSelection|null } device_selection
 * @property { DeviceGroupSelection|null } device_group_selection
 * @property { ImportSelection|null } import_selection
 * @property { GenericEventSource|null } generic_event_source
 */

/**
 * @typedef {Object} Location
 * @property { string } id
 * @property { string } name
 * @property { string } description
 * @property { string } image
 * @property { string[] } device_ids
 * @property { string[] } device_group_ids
 */

/**
 * @typedef {Object} Service
 * @property { string } id
 * @property { string } local_id
 * @property { string } name
 * @property { string } description
 * @property { string } interaction
 * @property { string } protocol_id
 * @property { Content[] } inputs
 * @property { Content[] } outputs
 * @property { Attribute[] } attributes
 * @property { string } service_group_key
 */

/**
 * @typedef {Object} ServiceGroup
 * @property { string } key
 * @property { string } name
 * @property { string } description
 */

/**
 * @typedef {Object} ServicePathOption
 * @property { string } service_id
 * @property { string } path
 * @property { string } characteristic_id
 * @property { AspectNode } aspect_node deprecated: alias for a single element AspectNodes; holds the node with the alphabetically first id
 * @property { AspectNode[] } aspect_nodes
 * @property { string } function_id
 * @property { boolean } is_void
 * @property { Object|null } value
 * @property { boolean } is_controlling_function
 * @property { Configurable[] } configurables
 * @property { string } type
 * @property { string } interaction
 */

