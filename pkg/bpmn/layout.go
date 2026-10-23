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

package bpmn

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/beevik/etree"
)

const (
	NsBpmnDi = "http://www.omg.org/spec/BPMN/20100524/DI"
	NsDc     = "http://www.omg.org/spec/DD/20100524/DC"
	NsDi     = "http://www.omg.org/spec/DD/20100524/DI"
)

// distances of the generated layout, in the units of the designer
const (
	layoutLayerWidth = 170
	layoutRowHeight  = 130
	layoutMarginX    = 100
	layoutMarginY    = 80
	layoutPoolLabel  = 30
	layoutPoolGap    = 30
)

// WithDiagram adds a bpmndi:BPMNDiagram to a model that has none, so that a designer can show
// a model a client sent without one. The layout puts the flow nodes in layers along the
// sequence flows, lanes as bands and the participants of a collaboration as pools; a
// subprocess with content is shown collapsed with a diagram of its own. Annotations, data
// objects and message flows get no position. A model with a diagram, or one that is no BPMN,
// comes back unchanged with added false.
func WithDiagram(xml string) (result string, added bool, err error) {
	doc := etree.NewDocument()
	if err = doc.ReadFromString(xml); err != nil {
		return xml, false, nil
	}
	root := doc.Root()
	if root == nil || !is(root, NsBpmn, "definitions") {
		return xml, false, nil
	}
	for _, child := range root.ChildElements() {
		if is(child, NsBpmnDi, "BPMNDiagram") {
			return xml, false, nil
		}
	}
	l := &layouter{root: root, processes: map[string]*etree.Element{}}
	l.bpmndi, l.dc, l.di = l.prefix(NsBpmnDi, "bpmndi"), l.prefix(NsDc, "dc"), l.prefix(NsDi, "di")
	var collaboration *etree.Element
	var first *etree.Element
	for _, child := range root.ChildElements() {
		switch {
		case is(child, NsBpmn, "process"):
			l.processes[child.SelectAttrValue("id", "")] = child
			if first == nil || (child.SelectAttrValue("isExecutable", "") == "true" && first.SelectAttrValue("isExecutable", "") != "true") {
				first = child
			}
		case is(child, NsBpmn, "collaboration") && collaboration == nil:
			collaboration = child
		}
	}
	switch {
	case collaboration != nil:
		plane := l.diagram(collaboration.SelectAttrValue("id", ""))
		y := layoutMarginY
		for _, participant := range collaboration.ChildElements() {
			if !is(participant, NsBpmn, "participant") {
				continue
			}
			proc := l.processes[participant.SelectAttrValue("processRef", "")]
			height := l.pool(plane, participant, proc, y)
			y += height + layoutPoolGap
		}
	case first != nil:
		plane := l.diagram(first.SelectAttrValue("id", ""))
		l.container(plane, first, layoutMarginX, layoutMarginY, nil)
	default:
		return xml, false, nil
	}
	result, err = doc.WriteToString()
	if err != nil {
		return xml, false, err
	}
	return result, true, nil
}

type layouter struct {
	root             *etree.Element
	processes        map[string]*etree.Element
	bpmndi, dc, di   string
	diagrams         int
	collapsedPending []*etree.Element
}

type box struct{ x, y, w, h int }

func (this box) right() int   { return this.x + this.w }
func (this box) bottom() int  { return this.y + this.h }
func (this box) centerX() int { return this.x + this.w/2 }
func (this box) centerY() int { return this.y + this.h/2 }

// prefix returns the prefix the document binds to a namespace, declaring it if needed
func (this *layouter) prefix(ns string, preferred string) string {
	taken := map[string]bool{}
	for _, a := range this.root.Attr {
		if a.Space == "xmlns" {
			if a.Value == ns {
				return a.Key
			}
			taken[a.Key] = true
		}
	}
	name := preferred
	for i := 2; taken[name]; i++ {
		name = preferred + strconv.Itoa(i)
	}
	this.root.CreateAttr("xmlns:"+name, ns)
	return name
}

func (this *layouter) diagram(elementId string) *etree.Element {
	this.diagrams++
	diagram := this.root.CreateElement(this.bpmndi + ":BPMNDiagram")
	diagram.CreateAttr("id", fmt.Sprintf("BPMNDiagram_%v", this.diagrams))
	plane := diagram.CreateElement(this.bpmndi + ":BPMNPlane")
	plane.CreateAttr("id", fmt.Sprintf("BPMNPlane_%v", this.diagrams))
	plane.CreateAttr("bpmnElement", elementId)
	return plane
}

func (this *layouter) shape(plane *etree.Element, elementId string, b box, extra ...string) {
	shape := plane.CreateElement(this.bpmndi + ":BPMNShape")
	shape.CreateAttr("id", elementId+"_di")
	shape.CreateAttr("bpmnElement", elementId)
	for i := 0; i+1 < len(extra); i += 2 {
		shape.CreateAttr(extra[i], extra[i+1])
	}
	bounds := shape.CreateElement(this.dc + ":Bounds")
	bounds.CreateAttr("x", strconv.Itoa(b.x))
	bounds.CreateAttr("y", strconv.Itoa(b.y))
	bounds.CreateAttr("width", strconv.Itoa(b.w))
	bounds.CreateAttr("height", strconv.Itoa(b.h))
}

func (this *layouter) edge(plane *etree.Element, elementId string, points [][2]int) {
	edge := plane.CreateElement(this.bpmndi + ":BPMNEdge")
	edge.CreateAttr("id", elementId+"_di")
	edge.CreateAttr("bpmnElement", elementId)
	for _, p := range points {
		waypoint := edge.CreateElement(this.di + ":waypoint")
		waypoint.CreateAttr("x", strconv.Itoa(p[0]))
		waypoint.CreateAttr("y", strconv.Itoa(p[1]))
	}
}

// pool draws a participant around the layout of its process and returns its height
func (this *layouter) pool(plane *etree.Element, participant *etree.Element, proc *etree.Element, y int) int {
	x := layoutMarginX
	width, height := 600, 250
	if proc != nil {
		var lanes []*etree.Element
		for _, child := range proc.ChildElements() {
			if is(child, NsBpmn, "laneSet") {
				for _, lane := range child.ChildElements() {
					if is(lane, NsBpmn, "lane") {
						lanes = append(lanes, lane)
					}
				}
			}
		}
		content := this.container(plane, proc, x+layoutPoolLabel, y, lanes)
		width = max(width, content.w+layoutPoolLabel)
		height = max(height, content.h)
		if n := len(content.lanes); n > 0 {
			last := &content.lanes[n-1]
			last.height = height - last.top
		}
		for _, lane := range content.lanes {
			this.shape(plane, lane.id, box{x + layoutPoolLabel, y + lane.top, width - layoutPoolLabel, lane.height}, "isHorizontal", "true")
		}
	}
	this.shape(plane, participant.SelectAttrValue("id", ""), box{x, y, width, height}, "isHorizontal", "true")
	return height
}

type laneBand struct {
	id     string
	top    int
	height int
}

type extent struct {
	w, h  int
	lanes []laneBand
}

var gatewayTags = map[string]bool{"exclusiveGateway": true, "parallelGateway": true, "inclusiveGateway": true, "eventBasedGateway": true, "complexGateway": true}
var eventTags = map[string]bool{"startEvent": true, "endEvent": true, "intermediateCatchEvent": true, "intermediateThrowEvent": true}
var activityTags = map[string]bool{"task": true, "serviceTask": true, "scriptTask": true, "userTask": true, "sendTask": true, "receiveTask": true,
	"manualTask": true, "businessRuleTask": true, "callActivity": true, "subProcess": true, "transaction": true, "adHocSubProcess": true}

func nodeSize(tag string) (int, int, bool) {
	switch {
	case eventTags[tag]:
		return 36, 36, true
	case gatewayTags[tag]:
		return 50, 50, true
	case activityTags[tag]:
		return 100, 80, true
	}
	return 0, 0, false
}

// container lays out the flow nodes of a process or subprocess with its top left corner at
// (x, y) and returns the size it takes
func (this *layouter) container(plane *etree.Element, container *etree.Element, x, y int, lanes []*etree.Element) extent {
	type node struct {
		id    string
		tag   string
		el    *etree.Element
		w, h  int
		layer int
		lane  int
		row   int
		b     box
	}
	var nodes []*node
	byId := map[string]*node{}
	var boundaries []*etree.Element
	var flows []*etree.Element
	for _, child := range container.ChildElements() {
		if child.NamespaceURI() != NsBpmn {
			continue
		}
		switch {
		case child.Tag == "boundaryEvent":
			boundaries = append(boundaries, child)
		case child.Tag == "sequenceFlow":
			flows = append(flows, child)
		default:
			if w, h, ok := nodeSize(child.Tag); ok {
				n := &node{id: child.SelectAttrValue("id", ""), tag: child.Tag, el: child, w: w, h: h}
				nodes = append(nodes, n)
				byId[n.id] = n
			}
		}
	}
	// a boundary event leaves its host: for the layers, its flows start at the host
	host := map[string]string{}
	for _, b := range boundaries {
		host[b.SelectAttrValue("id", "")] = b.SelectAttrValue("attachedToRef", "")
	}
	endpoint := func(id string) string {
		if h, ok := host[id]; ok {
			return h
		}
		return id
	}
	successors := map[string][]string{}
	incoming := map[string]int{}
	for _, f := range flows {
		from, to := endpoint(f.SelectAttrValue("sourceRef", "")), f.SelectAttrValue("targetRef", "")
		if byId[from] == nil || byId[to] == nil || from == to {
			continue
		}
		successors[from] = append(successors[from], to)
		incoming[to]++
	}
	// back edges are those closing a cycle in a depth-first walk from the nodes without
	// incoming flows, in document order
	const (
		unvisited = iota
		onStack
		done
	)
	state := map[string]int{}
	back := map[[2]string]bool{}
	var walk func(id string)
	walk = func(id string) {
		state[id] = onStack
		for _, next := range successors[id] {
			switch state[next] {
			case onStack:
				back[[2]string{id, next}] = true
			case unvisited:
				walk(next)
			}
		}
		state[id] = done
	}
	for _, n := range nodes {
		if incoming[n.id] == 0 && state[n.id] == unvisited {
			walk(n.id)
		}
	}
	for _, n := range nodes {
		if state[n.id] == unvisited {
			walk(n.id)
		}
	}
	// longest path over the forward edges
	forwardIn := map[string]int{}
	for from, list := range successors {
		for _, to := range list {
			if !back[[2]string{from, to}] {
				forwardIn[to]++
			}
		}
	}
	var queue []string
	for _, n := range nodes {
		if forwardIn[n.id] == 0 {
			queue = append(queue, n.id)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, next := range successors[id] {
			if back[[2]string{id, next}] {
				continue
			}
			byId[next].layer = max(byId[next].layer, byId[id].layer+1)
			if forwardIn[next]--; forwardIn[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	// lanes are bands; a node in no lane goes to the first band
	laneOf := map[string]int{}
	for i, lane := range lanes {
		for _, ref := range lane.ChildElements() {
			if is(ref, NsBpmn, "flowNodeRef") {
				id := strings.TrimSpace(ref.Text())
				if _, known := laneOf[id]; !known {
					laneOf[id] = i
				}
			}
		}
	}
	bandCount := max(1, len(lanes))
	rows := make([]map[int]int, bandCount)
	for i := range rows {
		rows[i] = map[int]int{}
	}
	maxLayer := 0
	for _, n := range nodes {
		n.lane = laneOf[n.id]
		n.row = rows[n.lane][n.layer]
		rows[n.lane][n.layer]++
		maxLayer = max(maxLayer, n.layer)
	}
	result := extent{w: layoutMarginX + (maxLayer+1)*layoutLayerWidth}
	top := 0
	for i := 0; i < bandCount; i++ {
		count := 1
		for _, c := range rows[i] {
			count = max(count, c)
		}
		height := count*layoutRowHeight + 40
		if i < len(lanes) {
			result.lanes = append(result.lanes, laneBand{id: lanes[i].SelectAttrValue("id", ""), top: top, height: height})
		}
		for _, n := range nodes {
			if n.lane != i {
				continue
			}
			cx := x + layoutMarginX/2 + n.layer*layoutLayerWidth + 50
			cy := y + top + 20 + n.row*layoutRowHeight + layoutRowHeight/2
			n.b = box{cx - n.w/2, cy - n.h/2, n.w, n.h}
		}
		top += height
	}
	result.h = top
	bounds := map[string]box{}
	for _, n := range nodes {
		bounds[n.id] = n.b
		switch {
		case n.tag == "subProcess" || n.tag == "transaction" || n.tag == "adHocSubProcess":
			if hasFlowNodes(n.el) {
				this.shape(plane, n.id, n.b, "isExpanded", "false")
				this.collapsedPending = append(this.collapsedPending, n.el)
			} else {
				this.shape(plane, n.id, n.b)
			}
		case n.tag == "exclusiveGateway":
			this.shape(plane, n.id, n.b, "isMarkerVisible", "true")
		default:
			this.shape(plane, n.id, n.b)
		}
	}
	perHost := map[string]int{}
	for _, b := range boundaries {
		id, hostId := b.SelectAttrValue("id", ""), b.SelectAttrValue("attachedToRef", "")
		h, ok := bounds[hostId]
		if !ok {
			continue
		}
		i := perHost[hostId]
		perHost[hostId]++
		cx := h.right() - 18 - i*40
		bounds[id] = box{cx - 18, h.bottom() - 18, 36, 36}
		this.shape(plane, id, bounds[id])
	}
	lowest := 0
	for _, b := range bounds {
		lowest = max(lowest, b.bottom())
	}
	for _, f := range flows {
		id := f.SelectAttrValue("id", "")
		from, to := f.SelectAttrValue("sourceRef", ""), f.SelectAttrValue("targetRef", "")
		s, okFrom := bounds[from]
		t, okTo := bounds[to]
		if !okFrom || !okTo {
			continue
		}
		_, fromBoundary := host[from]
		switch {
		case fromBoundary:
			this.edge(plane, id, [][2]int{{s.centerX(), s.bottom()}, {s.centerX(), t.centerY()}, {t.x, t.centerY()}})
		case t.x > s.right():
			if s.centerY() == t.centerY() {
				this.edge(plane, id, [][2]int{{s.right(), s.centerY()}, {t.x, t.centerY()}})
			} else {
				mid := (s.right() + t.x) / 2
				this.edge(plane, id, [][2]int{{s.right(), s.centerY()}, {mid, s.centerY()}, {mid, t.centerY()}, {t.x, t.centerY()}})
			}
		default:
			// back to an earlier layer or within one: below the nodes
			below := lowest + 30
			this.edge(plane, id, [][2]int{{s.centerX(), s.bottom()}, {s.centerX(), below}, {t.centerX(), below}, {t.centerX(), t.bottom()}})
		}
	}
	if lowest+50 > y+result.h {
		result.h = lowest + 50 - y
	}
	// collapsed subprocesses get a diagram of their own, after the plane that shows them
	pending := this.collapsedPending
	this.collapsedPending = nil
	for _, sub := range pending {
		subPlane := this.diagram(sub.SelectAttrValue("id", ""))
		this.container(subPlane, sub, layoutMarginX, layoutMarginY, nil)
	}
	return result
}

func hasFlowNodes(e *etree.Element) bool {
	for _, child := range e.ChildElements() {
		if _, _, ok := nodeSize(child.Tag); ok && child.NamespaceURI() == NsBpmn {
			return true
		}
	}
	return false
}
