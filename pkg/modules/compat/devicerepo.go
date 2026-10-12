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

package compat

import (
	"fmt"

	"github.com/SENERGY-Platform/device-repository/v2/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
)

type ScriptEnvDeviceRepo struct {
	env *ScriptEnv
}

func NewDeviceRepoScriptEnv(env *ScriptEnv) *ScriptEnvDeviceRepo {
	return &ScriptEnvDeviceRepo{env: env}
}

func (this *ScriptEnvDeviceRepo) ReadDevice(id string) models.Device {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.ReadDevice(id, this.env.getToken(), model.READ)
	if err != nil {
		panic(fmt.Errorf("error in ReadDevice(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) ReadDeviceByLocalId(localId string) models.Device {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	//device-repository replaces ownerId="" with the requesting user id
	result, err, _ := this.env.iotClient.ReadDeviceByLocalId("", localId, this.env.getToken(), model.READ)
	if err != nil {
		panic(fmt.Errorf("error in ReadDeviceByLocalId(%#v): %v", localId, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) ReadHub(id string) models.Hub {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.ReadHub(id, this.env.getToken(), model.READ)
	if err != nil {
		panic(fmt.Errorf("error in ReadHub(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) ListHubDeviceIds(id string, asLocalId bool) []string {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.ListHubDeviceIds(id, this.env.getToken(), model.READ, asLocalId)
	if err != nil {
		panic(fmt.Errorf("error in ListHubDeviceIds(%#v, %#v): %v", id, asLocalId, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) ReadDeviceType(id string) models.DeviceType {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.ReadDeviceType(id, this.env.getToken())
	if err != nil {
		panic(fmt.Errorf("error in ReadDeviceType(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) ListDeviceTypes(limit int64, offset int64, sort string, filter []model.FilterCriteria, includeModified bool, includeUnmodified bool) []models.DeviceType {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.ListDeviceTypesV2(this.env.getToken(), limit, offset, sort, filter, includeModified, includeUnmodified)
	if err != nil {
		panic(fmt.Errorf("error in ListDeviceTypes(): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetDeviceTypeSelectables(query []model.FilterCriteria, pathPrefix string, includeModified bool, servicesMustMatchAllCriteria bool) []model.DeviceTypeSelectable {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetDeviceTypeSelectablesV2(query, pathPrefix, includeModified, servicesMustMatchAllCriteria)
	if err != nil {
		panic(fmt.Errorf("error in GetDeviceTypeSelectables(): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) ReadDeviceGroup(id string) models.DeviceGroup {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.ReadDeviceGroup(id, this.env.getToken(), false)
	if err != nil {
		panic(fmt.Errorf("error in ReadDeviceGroup(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetService(id string) models.Service {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetService(id)
	if err != nil {
		panic(fmt.Errorf("error in GetService(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspects() []models.Aspect {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetAspects()
	if err != nil {
		panic(fmt.Errorf("error in GetAspects(): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspectsWithMeasuringFunction(ancestors bool, descendants bool) []models.Aspect {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetAspectsWithMeasuringFunction(ancestors, descendants)
	if err != nil {
		panic(fmt.Errorf("error in GetAspectsWithMeasuringFunction(): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspect(id string) models.Aspect {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetAspect(id)
	if err != nil {
		panic(fmt.Errorf("error in GetAspect(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspectNode(id string) models.AspectNode {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetAspectNode(id)
	if err != nil {
		panic(fmt.Errorf("error in GetAspectNode(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspectNodes() []models.AspectNode {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetAspectNodes()
	if err != nil {
		panic(fmt.Errorf("error in GetAspectNodes(): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspectNodesMeasuringFunctions(id string, ancestors bool, descendants bool) []models.Function {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetAspectNodesMeasuringFunctions(id, ancestors, descendants)
	if err != nil {
		panic(fmt.Errorf("error in GetAspectNodesMeasuringFunctions(%#v, %#v, %#v): %v", id, ancestors, descendants, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspectNodesWithMeasuringFunction(ancestors bool, descendants bool) []models.AspectNode {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetAspectNodesWithMeasuringFunction(ancestors, descendants)
	if err != nil {
		panic(fmt.Errorf("error in GetAspectNodesWithMeasuringFunction(%#v, %#v): %v", ancestors, descendants, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspectNodesByIdList(ids []string) []models.AspectNode {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetAspectNodesByIdList(ids)
	if err != nil {
		panic(fmt.Errorf("error in GetAspectNodesByIdList(%#v): %v", ids, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetLeafCharacteristics() []models.Characteristic {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetCharacteristics(true)
	if err != nil {
		panic(fmt.Errorf("error in GetCharacteristics(true): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetCharacteristic(id string) models.Characteristic {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetCharacteristic(id)
	if err != nil {
		panic(fmt.Errorf("error in GetCharacteristic(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetConceptWithCharacteristics(id string) models.ConceptWithCharacteristics {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetConceptWithCharacteristics(id)
	if err != nil {
		panic(fmt.Errorf("error in GetConceptWithCharacteristics(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetConceptWithoutCharacteristics(id string) models.Concept {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetConceptWithoutCharacteristics(id)
	if err != nil {
		panic(fmt.Errorf("error in GetConceptWithoutCharacteristics(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetDeviceClasses() []models.DeviceClass {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetDeviceClasses()
	if err != nil {
		panic(fmt.Errorf("error in GetDeviceClasses(): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetDeviceClassesWithControllingFunctions() []models.DeviceClass {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetDeviceClassesWithControllingFunctions()
	if err != nil {
		panic(fmt.Errorf("error in GetDeviceClassesWithControllingFunctions(): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetDeviceClassesFunctions(id string) []models.Function {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetDeviceClassesFunctions(id)
	if err != nil {
		panic(fmt.Errorf("error in GetDeviceClassesFunctions(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetDeviceClassesControllingFunctions(id string) []models.Function {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetDeviceClassesControllingFunctions(id)
	if err != nil {
		panic(fmt.Errorf("error in GetDeviceClassesControllingFunctions(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetDeviceClass(id string) models.DeviceClass {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetDeviceClass(id)
	if err != nil {
		panic(fmt.Errorf("error in GetDeviceClass(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetFunctionsByType(rdfType string) []models.Function {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetFunctionsByType(rdfType)
	if err != nil {
		panic(fmt.Errorf("error in GetFunctionsByType(%#v): %v", rdfType, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetFunction(id string) models.Function {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetFunction(id)
	if err != nil {
		panic(fmt.Errorf("error in GetFunction(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetLocation(id string) models.Location {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result, err, _ := this.env.iotClient.GetLocation(id, this.env.getToken())
	if err != nil {
		panic(fmt.Errorf("error in GetLocation(%#v): %v", id, err))
	}
	return result
}
