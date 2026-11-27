package main

import (
	"encoding/json"
	"reflect"
	"strings"
)

// The plans file is shared by every version the owner runs: the stable and
// Test channels, and a rollback. A version must keep fields a newer one
// wrote that it does not know (2026-11-27: a stable build rewrote the file
// without the test branch's testedAt, and two tested VMs read as untested).
// mbKeepUnknownFields copies into the file about to be written every plan,
// settings and VM field of the old file whose name this version does not
// know, matching plans and VMs by ID. Fields this version knows are always
// its own, so a value it cleared is never brought back.
func mbKeepUnknownFields(newRaw, oldRaw []byte) []byte {
	var fresh, old []map[string]json.RawMessage
	if len(oldRaw) == 0 || json.Unmarshal(newRaw, &fresh) != nil || json.Unmarshal(oldRaw, &old) != nil {
		return newRaw
	}
	planKnown, settingsKnown, vmKnown := mbJSONNames(mbPlan{}), mbJSONNames(mbSettings{}), mbJSONNames(mbVM{})
	oldPlans := map[string]map[string]json.RawMessage{}
	for _, plan := range old {
		if id := mbRawID(plan); id != "" {
			oldPlans[id] = plan
		}
	}
	changed := false
	for _, plan := range fresh {
		before := oldPlans[mbRawID(plan)]
		if before == nil {
			continue
		}
		changed = mbCopyUnknown(plan, before, planKnown) || changed
		if s, ok := mbMergeObject(plan["settings"], before["settings"], settingsKnown); ok {
			plan["settings"], changed = s, true
		}
		var vms, oldVMs []map[string]json.RawMessage
		if json.Unmarshal(plan["vms"], &vms) != nil || json.Unmarshal(before["vms"], &oldVMs) != nil {
			continue
		}
		byID := map[string]map[string]json.RawMessage{}
		for _, vm := range oldVMs {
			byID[mbRawID(vm)] = vm
		}
		vmChanged := false
		for _, vm := range vms {
			if prior := byID[mbRawID(vm)]; prior != nil && mbRawID(vm) != "" {
				vmChanged = mbCopyUnknown(vm, prior, vmKnown) || vmChanged
			}
		}
		if vmChanged {
			if raw, err := json.Marshal(vms); err == nil {
				plan["vms"], changed = raw, true
			}
		}
	}
	if !changed {
		return newRaw
	}
	out, err := json.MarshalIndent(fresh, "", "  ")
	if err != nil {
		return newRaw
	}
	return out
}

func mbRawID(object map[string]json.RawMessage) string {
	var id string
	if json.Unmarshal(object["id"], &id) != nil {
		return ""
	}
	return id
}

// mbCopyUnknown copies from old the fields whose names known lacks.
func mbCopyUnknown(into, old map[string]json.RawMessage, known map[string]bool) bool {
	changed := false
	for key, value := range old {
		if _, present := into[key]; !present && !known[key] {
			into[key], changed = value, true
		}
	}
	return changed
}

func mbMergeObject(fresh, old json.RawMessage, known map[string]bool) (json.RawMessage, bool) {
	var a, b map[string]json.RawMessage
	if json.Unmarshal(fresh, &a) != nil || json.Unmarshal(old, &b) != nil || a == nil || b == nil {
		return nil, false
	}
	if !mbCopyUnknown(a, b, known) {
		return nil, false
	}
	raw, err := json.Marshal(a)
	return raw, err == nil
}

// mbJSONNames lists the JSON field names of a struct, embedded ones too.
func mbJSONNames(v interface{}) map[string]bool {
	names := map[string]bool{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			tag := field.Tag.Get("json")
			name := strings.Split(tag, ",")[0]
			if field.Anonymous && tag == "" && field.Type.Kind() == reflect.Struct {
				walk(field.Type)
				continue
			}
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			names[name] = true
		}
	}
	walk(reflect.TypeOf(v))
	return names
}
