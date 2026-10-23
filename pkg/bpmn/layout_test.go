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

package bpmn_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/beevik/etree"
)

type diShape struct {
	x, y, w, h int
	attrs      map[string]string
}

// diagramOf reads the planes of a generated diagram: per plane element, the shapes by element
// and the waypoints of the edges by element
func diagramOf(t *testing.T, xml string) (planes map[string]map[string]diShape, edges map[string][][2]int) {
	t.Helper()
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		t.Fatal(err)
	}
	planes = map[string]map[string]diShape{}
	edges = map[string][][2]int{}
	for _, diagram := range doc.Root().ChildElements() {
		if diagram.Tag != "BPMNDiagram" || diagram.NamespaceURI() != bpmn.NsBpmnDi {
			continue
		}
		for _, plane := range diagram.ChildElements() {
			shapes := map[string]diShape{}
			planes[plane.SelectAttrValue("bpmnElement", "")] = shapes
			for _, e := range plane.ChildElements() {
				switch e.Tag {
				case "BPMNShape":
					bounds := e.ChildElements()[0]
					number := func(name string) int {
						n, _ := strconv.Atoi(bounds.SelectAttrValue(name, ""))
						return n
					}
					attrs := map[string]string{}
					for _, a := range e.Attr {
						attrs[a.Key] = a.Value
					}
					shapes[e.SelectAttrValue("bpmnElement", "")] = diShape{number("x"), number("y"), number("width"), number("height"), attrs}
				case "BPMNEdge":
					var points [][2]int
					for _, w := range e.ChildElements() {
						x, _ := strconv.Atoi(w.SelectAttrValue("x", ""))
						y, _ := strconv.Atoi(w.SelectAttrValue("y", ""))
						points = append(points, [2]int{x, y})
					}
					edges[e.SelectAttrValue("bpmnElement", "")] = points
				}
			}
		}
	}
	return planes, edges
}

func overlap(a, b diShape) bool {
	return a.x < b.x+b.w && b.x < a.x+a.w && a.y < b.y+b.h && b.y < a.y+a.h
}

// A client may send a model without bpmndi:BPMNDiagram; a designer cannot show it then.
func TestWithDiagramLaysOutAModelWithout(t *testing.T) {
	xml := b.Process(
		b.Start("start"), b.Task("work"), b.Gateway("exclusive", "check", ""), b.Task("left"), b.Task("right"), b.End("end"),
		b.Flow("start", "work"), b.Flow("work", "check"), b.Flow("check", "left"), b.Flow("check", "right"),
		b.Flow("left", "end"), b.Flow("right", "end"), b.Flow("right", "work"),
	)
	result, added, err := bpmn.WithDiagram(xml)
	if err != nil || !added {
		t.Fatalf("%v %v", added, err)
	}
	planes, edges := diagramOf(t, result)
	shapes := planes["test_process"]
	ids := []string{"start", "work", "check", "left", "right", "end"}
	for _, id := range ids {
		if _, ok := shapes[id]; !ok {
			t.Errorf("no shape for %v", id)
		}
	}
	for i, a := range ids {
		for _, other := range ids[i+1:] {
			if overlap(shapes[a], shapes[other]) {
				t.Errorf("%v overlaps %v: %+v %+v", a, other, shapes[a], shapes[other])
			}
		}
	}
	if !(shapes["start"].x < shapes["work"].x && shapes["work"].x < shapes["check"].x && shapes["check"].x < shapes["left"].x && shapes["left"].x < shapes["end"].x) {
		t.Errorf("the layers follow the flows: %+v", shapes)
	}
	if shapes["left"].x != shapes["right"].x || shapes["left"].y == shapes["right"].y {
		t.Errorf("parallel branches share a layer in separate rows: %+v %+v", shapes["left"], shapes["right"])
	}
	for _, flow := range []string{"start_work", "work_check", "check_left", "check_right", "left_end", "right_end", "right_work"} {
		points := edges["f_"+flow]
		if len(points) < 2 {
			t.Errorf("no edge for %v", flow)
			continue
		}
		for i := 1; i < len(points); i++ {
			if points[i][0] != points[i-1][0] && points[i][1] != points[i-1][1] {
				t.Errorf("%v has a diagonal segment: %v", flow, points)
			}
		}
	}
	if back := edges["f_right_work"]; back[1][1] <= shapes["right"].y+shapes["right"].h {
		t.Errorf("a flow back runs below the nodes: %v", back)
	}
	if shapes["check"].attrs["isMarkerVisible"] != "true" {
		t.Errorf("exclusive gateways show their marker: %+v", shapes["check"])
	}
	process, issues, err := bpmn.Parse(result)
	if err != nil || len(issues) != 0 || process.Element("work") == nil {
		t.Fatalf("the model stays the same: %v %v", issues, err)
	}
}

func TestWithDiagramKeepsAnExistingDiagram(t *testing.T) {
	xml := strings.Replace(b.Process(b.Start("start"), b.End("end"), b.Flow("start", "end")), "</bpmn:definitions>",
		`<bpmndi:BPMNDiagram xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="d"><bpmndi:BPMNPlane id="p" bpmnElement="test_process"/></bpmndi:BPMNDiagram></bpmn:definitions>`, 1)
	if result, added, err := bpmn.WithDiagram(xml); err != nil || added || result != xml {
		t.Fatalf("%v %v", added, err)
	}
	if result, added, err := bpmn.WithDiagram("no xml"); err != nil || added || result != "no xml" {
		t.Fatalf("what is no BPMN is left to validation: %v %v", added, err)
	}
}

// Lanes of a collaboration become bands of a pool, with each node in the band of its lane.
func TestWithDiagramDrawsPoolsAndLanes(t *testing.T) {
	xml := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="d">
<bpmn:collaboration id="c"><bpmn:participant id="pool" processRef="p"/></bpmn:collaboration>
<bpmn:process id="p" isExecutable="true">
<bpmn:laneSet id="ls"><bpmn:lane id="upper"><bpmn:flowNodeRef>start</bpmn:flowNodeRef><bpmn:flowNodeRef>a</bpmn:flowNodeRef></bpmn:lane>
<bpmn:lane id="lower"><bpmn:flowNodeRef>b</bpmn:flowNodeRef><bpmn:flowNodeRef>end</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet>
<bpmn:startEvent id="start"/><bpmn:task id="a"/><bpmn:task id="b"/><bpmn:endEvent id="end"/>
<bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="a"/><bpmn:sequenceFlow id="f2" sourceRef="a" targetRef="b"/><bpmn:sequenceFlow id="f3" sourceRef="b" targetRef="end"/>
</bpmn:process></bpmn:definitions>`
	result, added, err := bpmn.WithDiagram(xml)
	if err != nil || !added {
		t.Fatalf("%v %v", added, err)
	}
	planes, _ := diagramOf(t, result)
	shapes := planes["c"]
	pool, upper, lower := shapes["pool"], shapes["upper"], shapes["lower"]
	if pool.w == 0 || upper.h == 0 || lower.h == 0 || upper.y+upper.h != lower.y || lower.y+lower.h != pool.y+pool.h {
		t.Fatalf("lanes fill the pool: %+v %+v %+v", pool, upper, lower)
	}
	inside := func(s, band diShape) bool {
		return s.y >= band.y && s.y+s.h <= band.y+band.h && s.x >= band.x && s.x+s.w <= band.x+band.w
	}
	for id, band := range map[string]diShape{"start": upper, "a": upper, "b": lower, "end": lower} {
		if !inside(shapes[id], band) {
			t.Errorf("%v lies outside its lane: %+v in %+v", id, shapes[id], band)
		}
	}
}

// A subprocess with content is shown collapsed, with its content in a diagram of its own; a
// boundary event sits on the lower edge of its host.
func TestWithDiagramDrawsSubprocessesAndBoundaryEvents(t *testing.T) {
	xml := b.Process(
		b.Start("start"),
		`<bpmn:subProcess id="sub"><bpmn:startEvent id="inner_start"/><bpmn:endEvent id="inner_end"/><bpmn:sequenceFlow id="fi" sourceRef="inner_start" targetRef="inner_end"/></bpmn:subProcess>`,
		`<bpmn:boundaryEvent id="timeout" attachedToRef="sub"><bpmn:timerEventDefinition><bpmn:timeDuration>PT1M</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:boundaryEvent>`,
		b.End("end"), b.End("late"),
		b.Flow("start", "sub"), b.Flow("sub", "end"), b.Flow("timeout", "late"),
	)
	result, added, err := bpmn.WithDiagram(xml)
	if err != nil || !added {
		t.Fatalf("%v %v", added, err)
	}
	planes, edges := diagramOf(t, result)
	main, sub := planes["test_process"], planes["sub"]
	if main["sub"].attrs["isExpanded"] != "false" || sub["inner_start"].w == 0 || sub["inner_end"].w == 0 {
		t.Fatalf("collapsed with a plane of its own: %+v %+v", main["sub"], sub)
	}
	host, timeout := main["sub"], main["timeout"]
	if timeout.y+timeout.h/2 != host.y+host.h || timeout.x < host.x || timeout.x+timeout.w > host.x+host.w {
		t.Errorf("the boundary event sits on the lower edge: %+v on %+v", timeout, host)
	}
	if len(edges["f_timeout_late"]) < 2 || main["late"].w == 0 {
		t.Errorf("the flow from the boundary event is drawn: %v", edges["f_timeout_late"])
	}
}
