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
 * the globals of scripts and expressions in BPMN models: script tasks, conditions of sequence
 * flows, input and output mappings, timer values, senergy:messageName and the conditions of
 * device and import events. the variables of the instance and the local variables of the
 * element are globals as well; their names depend on the model.
 */


/**
 * Writes the arguments, separated by spaces, to the history of the instance at the element
 * that runs the script (DBG-11). Strings are written as they are, other values as JSON.
 * At most 100 entries per evaluation are kept.
 */
declare function log(...args: any[]): void;

/**
 * console.log is the same function as log.
 */
interface Console {
    log(...args: any[]): void;
}
declare var console: Console;

/**
 * Sets a variable: the local variable of the element if it has one of this name, otherwise
 * the variable of the instance. The value has to be representable as JSON; it is also
 * visible to the rest of the script under its name.
 */
declare function setVariable(name: string, value: any): void;

