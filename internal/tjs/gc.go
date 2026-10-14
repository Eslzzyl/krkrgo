package tjs

import (
	"errors"
	"reflect"
)

func appendGCTraceHost(hosts []Host, candidate Host) []Host {
	if candidate == nil {
		return hosts
	}
	candidateType := reflect.TypeOf(candidate)
	if candidateType.Comparable() {
		for _, host := range hosts {
			if reflect.TypeOf(host) == candidateType && host == candidate {
				return hosts
			}
		}
	}
	return append(hosts, candidate)
}

const vmGCAllocationThreshold = 8192

func markObjectID(id ObjectID, seen map[ObjectID]struct{}, pending *[]ObjectID) {
	if id == 0 {
		return
	}
	if _, ok := seen[id]; ok {
		return
	}
	seen[id] = struct{}{}
	*pending = append(*pending, id)
}
func markValue(value Value, seen map[ObjectID]struct{}, pending *[]ObjectID) {
	if value.Kind() != KindObject {
		return
	}
	ref := value.ObjectRef()
	markObjectID(ref.Object, seen, pending)
	markObjectID(ref.Context, seen, pending)
}
func (v *VM) markObject(id ObjectID, seen map[ObjectID]struct{}, pending *[]ObjectID) {
	o, ok := v.Objects.Lookup(id)
	if !ok {
		return
	}
	for _, entry := range o.Entries() {
		markValue(entry.Value, seen, pending)
	}
	if m := v.meta[id]; m != nil {
		markObjectID(m.classObject, seen, pending)
		markObjectID(m.nativeConstructor, seen, pending)
		markObjectID(m.owner, seen, pending)
		markObjectID(m.superContext, seen, pending)
		markObjectID(m.lexicalContext, seen, pending)
		markObjectID(m.superOwner, seen, pending)
		markObjectID(m.scriptFinalizer, seen, pending)
		for _, base := range m.classBases {
			markObjectID(base, seen, pending)
		}
		for _, base := range m.superclassBases {
			markObjectID(base, seen, pending)
		}
		for _, x := range m.array {
			markValue(x, seen, pending)
		}
		for _, x := range m.arrayMethods {
			markValue(x, seen, pending)
		}
		for _, x := range m.stringMethods {
			markValue(x, seen, pending)
		}
		if m.methodContextSet {
			markValue(m.methodContext, seen, pending)
		}
		if m.legacyProperty != nil {
			markValue(m.legacyProperty.getter, seen, pending)
			markValue(m.legacyProperty.setter, seen, pending)
		}
		for _, scope := range m.capturedScopes {
			for _, x := range scope {
				markValue(x, seen, pending)
			}
		}
		for _, x := range m.capturedWiths {
			markValue(x, seen, pending)
		}
	}
}
func (v *VM) Root(value Value) error {
	if _, err := v.handle(value); err != nil {
		return err
	}
	v.roots[value.ObjectRef()] = struct{}{}
	return nil
}
func (v *VM) AddRoot(value Value) error { return v.Root(value) }
func (v *VM) Unroot(value Value) {
	if value.Kind() == KindObject {
		delete(v.roots, value.ObjectRef())
	}
}
func (v *VM) RemoveRoot(value Value) { v.Unroot(value) }

// CollectGarbage traces VM globals, explicit roots, host roots and native
// outgoing references, then removes unmarked objects from the identity table.
// Explicit collection is only allowed while no interpreter frame is active.
func (v *VM) CollectGarbage(host Host) (int, error) {
	if v == nil || v.Objects == nil {
		return 0, nil
	}
	if v.depth != 0 || len(v.frameRoots) != 0 {
		return 0, errors.New("TJS collection requires an idle interpreter")
	}
	return v.collectGarbage(host)
}
func (v *VM) collectAtSafePoint(host Host) (int, error) {
	if v == nil || v.Objects == nil {
		return 0, nil
	}
	if v.depth == 0 || len(v.frameRoots) != v.depth {
		return 0, errors.New("TJS collection safe point has incomplete interpreter roots")
	}
	return v.collectGarbage(host)
}
func (v *VM) collectGarbage(host Host) (int, error) {
	if v.collecting {
		return 0, errors.New("TJS collection is already in progress")
	}
	v.collecting = true
	defer func() { v.collecting = false }()
	seen := make(map[ObjectID]struct{}, len(v.meta))
	pending := make([]ObjectID, 0, len(v.meta))
	seen[0] = struct{}{}
	pending = append(pending, 0)
	for _, x := range v.Globals {
		markValue(x, seen, &pending)
	}
	for ref := range v.roots {
		markValue(ObjectValue(ref), seen, &pending)
	}
	for _, x := range v.literals {
		markValue(x, seen, &pending)
	}
	for _, value := range v.transientRoots {
		markValue(value, seen, &pending)
	}
	for _, frameRoots := range v.frameRoots {
		for _, value := range frameRoots.instructionRoots {
			markValue(value, seen, &pending)
		}
		for _, value := range frameRoots.registers {
			markValue(value, seen, &pending)
		}
		frame := frameRoots.frame
		if frame == nil {
			continue
		}
		markObjectID(frame.context, seen, &pending)
		markObjectID(frame.owner, seen, &pending)
		for _, scope := range frame.scopes {
			for _, value := range scope {
				markValue(value, seen, &pending)
			}
		}
		for _, value := range frame.withs {
			markValue(value, seen, &pending)
		}
		for _, value := range frame.args {
			markValue(value, seen, &pending)
		}
		if frame.pending != nil {
			markValue(frame.pending.value, seen, &pending)
		}
	}
	hosts := make([]Host, 0, len(v.frameRoots)+1)
	hosts = appendGCTraceHost(hosts, host)
	for _, frameRoots := range v.frameRoots {
		hosts = appendGCTraceHost(hosts, frameRoots.host)
	}
	for _, host := range hosts {
		if r, ok := host.(HostRoots); ok {
			for _, x := range r.GCRoots() {
				markValue(x, seen, &pending)
			}
		}
	}
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		v.markObject(id, seen, &pending)
		for _, host := range hosts {
			if tr, ok := host.(HostTraceObjects); ok {
				for _, x := range tr.GCTrace(id) {
					markValue(x, seen, &pending)
				}
			}
		}
	}
	removed := 0
	for _, id := range v.allIDs() {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		v.Objects.Remove(id)
		delete(v.meta, id)
		delete(v.roots, ObjectRef{Object: id})
		removed++
	}
	v.allocationsSinceGC = 0
	return removed, nil
}
func (v *VM) GC(host Host) error             { _, e := v.CollectGarbage(host); return e }
func (v *VM) Collect(host Host) (int, error) { return v.CollectGarbage(host) }
