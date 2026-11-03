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

	"github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
)

type ScriptEnvDeviceRepo struct {
	env *ScriptEnv
}

func NewDeviceRepoScriptEnv(env *ScriptEnv) *ScriptEnvDeviceRepo {
	return &ScriptEnvDeviceRepo{env: env}
}

func (this *ScriptEnvDeviceRepo) ReadDevice(id string) models.Device {
	result, err, _ := this.env.iotClient.ReadDevice(this.env.ctx, id, this.env.getToken(), model.READ)
	if err != nil {
		panic(fmt.Errorf("error in ReadDevice(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) ReadDeviceByLocalId(localId string) models.Device {
	//device-repository replaces ownerId="" with the requesting user id
	result, err, _ := this.env.iotClient.ReadDeviceByLocalId(this.env.ctx, "", localId, this.env.getToken(), model.READ)
	if err != nil {
		panic(fmt.Errorf("error in ReadDeviceByLocalId(%#v): %v", localId, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) ReadHub(id string) models.Hub {
	result, err, _ := this.env.iotClient.ReadHub(this.env.ctx, id, this.env.getToken(), model.READ)
	if err != nil {
		panic(fmt.Errorf("error in ReadHub(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) ListHubDeviceIds(id string, asLocalId bool) []string {
	result, err, _ := this.env.iotClient.ListHubDeviceIds(this.env.ctx, id, this.env.getToken(), model.READ, asLocalId)
	if err != nil {
		panic(fmt.Errorf("error in ListHubDeviceIds(%#v, %#v): %v", id, asLocalId, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) ReadDeviceType(id string) models.DeviceType {
	result, err, _ := this.env.iotClient.ReadDeviceType(this.env.ctx, id, this.env.getToken())
	if err != nil {
		panic(fmt.Errorf("error in ReadDeviceType(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) ListDeviceTypes(limit int64, offset int64, sort string, filter []model.FilterCriteria, includeModified bool, includeUnmodified bool) []models.DeviceType {
	result, err, _ := this.env.iotClient.ListDeviceTypesV2(this.env.ctx, this.env.getToken(), limit, offset, sort, filter, includeModified, includeUnmodified)
	if err != nil {
		panic(fmt.Errorf("error in ListDeviceTypes(): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetDeviceTypeSelectables(query []model.FilterCriteria, pathPrefix string, includeModified bool, servicesMustMatchAllCriteria bool) []model.DeviceTypeSelectable {
	result, err, _ := this.env.iotClient.GetDeviceTypeSelectablesV2(this.env.ctx, query, pathPrefix, includeModified, servicesMustMatchAllCriteria)
	if err != nil {
		panic(fmt.Errorf("error in GetDeviceTypeSelectables(): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) ReadDeviceGroup(id string) models.DeviceGroup {
	result, err, _ := this.env.iotClient.ReadDeviceGroup(this.env.ctx, id, this.env.getToken(), false)
	if err != nil {
		panic(fmt.Errorf("error in ReadDeviceGroup(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetService(id string) models.Service {
	result, err, _ := this.env.iotClient.GetService(this.env.ctx, id)
	if err != nil {
		panic(fmt.Errorf("error in GetService(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspects() []models.Aspect {
	result, err, _ := this.env.iotClient.GetAspects(this.env.ctx)
	if err != nil {
		panic(fmt.Errorf("error in GetAspects(): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspectsWithMeasuringFunction(ancestors bool, descendants bool) []models.Aspect {
	result, err, _ := this.env.iotClient.GetAspectsWithMeasuringFunction(this.env.ctx, ancestors, descendants)
	if err != nil {
		panic(fmt.Errorf("error in GetAspectsWithMeasuringFunction(): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspect(id string) models.Aspect {
	result, err, _ := this.env.iotClient.GetAspect(this.env.ctx, id)
	if err != nil {
		panic(fmt.Errorf("error in GetAspect(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspectNode(id string) models.AspectNode {
	result, err, _ := this.env.iotClient.GetAspectNode(this.env.ctx, id)
	if err != nil {
		panic(fmt.Errorf("error in GetAspectNode(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspectNodes() []models.AspectNode {
	result, err, _ := this.env.iotClient.GetAspectNodes(this.env.ctx)
	if err != nil {
		panic(fmt.Errorf("error in GetAspectNodes(): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspectNodesMeasuringFunctions(id string, ancestors bool, descendants bool) []models.Function {
	result, err, _ := this.env.iotClient.GetAspectNodesMeasuringFunctions(this.env.ctx, id, ancestors, descendants)
	if err != nil {
		panic(fmt.Errorf("error in GetAspectNodesMeasuringFunctions(%#v, %#v, %#v): %v", id, ancestors, descendants, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspectNodesWithMeasuringFunction(ancestors bool, descendants bool) []models.AspectNode {
	result, err, _ := this.env.iotClient.GetAspectNodesWithMeasuringFunction(this.env.ctx, ancestors, descendants)
	if err != nil {
		panic(fmt.Errorf("error in GetAspectNodesWithMeasuringFunction(%#v, %#v): %v", ancestors, descendants, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetAspectNodesByIdList(ids []string) []models.AspectNode {
	result, err, _ := this.env.iotClient.GetAspectNodesByIdList(this.env.ctx, ids)
	if err != nil {
		panic(fmt.Errorf("error in GetAspectNodesByIdList(%#v): %v", ids, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetLeafCharacteristics() []models.Characteristic {
	result, err, _ := this.env.iotClient.GetCharacteristics(this.env.ctx, true)
	if err != nil {
		panic(fmt.Errorf("error in GetCharacteristics(true): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetCharacteristic(id string) models.Characteristic {
	result, err, _ := this.env.iotClient.GetCharacteristic(this.env.ctx, id)
	if err != nil {
		panic(fmt.Errorf("error in GetCharacteristic(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetConceptWithCharacteristics(id string) models.ConceptWithCharacteristics {
	result, err, _ := this.env.iotClient.GetConceptWithCharacteristics(this.env.ctx, id)
	if err != nil {
		panic(fmt.Errorf("error in GetConceptWithCharacteristics(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetConceptWithoutCharacteristics(id string) models.Concept {
	result, err, _ := this.env.iotClient.GetConceptWithoutCharacteristics(this.env.ctx, id)
	if err != nil {
		panic(fmt.Errorf("error in GetConceptWithoutCharacteristics(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetDeviceClasses() []models.DeviceClass {
	result, err, _ := this.env.iotClient.GetDeviceClasses(this.env.ctx)
	if err != nil {
		panic(fmt.Errorf("error in GetDeviceClasses(): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetDeviceClassesWithControllingFunctions() []models.DeviceClass {
	result, err, _ := this.env.iotClient.GetDeviceClassesWithControllingFunctions(this.env.ctx)
	if err != nil {
		panic(fmt.Errorf("error in GetDeviceClassesWithControllingFunctions(): %v", err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetDeviceClassesFunctions(id string) []models.Function {
	result, err, _ := this.env.iotClient.GetDeviceClassesFunctions(this.env.ctx, id)
	if err != nil {
		panic(fmt.Errorf("error in GetDeviceClassesFunctions(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetDeviceClassesControllingFunctions(id string) []models.Function {
	result, err, _ := this.env.iotClient.GetDeviceClassesControllingFunctions(this.env.ctx, id)
	if err != nil {
		panic(fmt.Errorf("error in GetDeviceClassesControllingFunctions(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetDeviceClass(id string) models.DeviceClass {
	result, err, _ := this.env.iotClient.GetDeviceClass(this.env.ctx, id)
	if err != nil {
		panic(fmt.Errorf("error in GetDeviceClass(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetFunctionsByType(rdfType string) []models.Function {
	result, err, _ := this.env.iotClient.GetFunctionsByType(this.env.ctx, rdfType)
	if err != nil {
		panic(fmt.Errorf("error in GetFunctionsByType(%#v): %v", rdfType, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetFunction(id string) models.Function {
	result, err, _ := this.env.iotClient.GetFunction(this.env.ctx, id)
	if err != nil {
		panic(fmt.Errorf("error in GetFunction(%#v): %v", id, err))
	}
	return result
}

func (this *ScriptEnvDeviceRepo) GetLocation(id string) models.Location {
	result, err, _ := this.env.iotClient.GetLocation(this.env.ctx, id, this.env.getToken())
	if err != nil {
		panic(fmt.Errorf("error in GetLocation(%#v): %v", id, err))
	}
	return result
}
