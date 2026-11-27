package main

import (
	"reflect"
	"testing"
)

func TestMBClaimForNamesEachPlanAndServerOnce(t *testing.T) {
	a := &mbPlan{ID: "a", Name: "Wave 1", Server: "vc01.example.com"}
	b := &mbPlan{ID: "b", Name: "Wave 2", Server: "VC01.example.com"}
	claim := mbClaimFor([]mbWork{{Plan: a, VM: &mbVM{ID: "1"}}, {Plan: a, VM: &mbVM{ID: "2"}}, {Plan: b, VM: &mbVM{ID: "3"}, Cutover: true}})
	want := mbClaim{Plans: []string{"Wave 1", "Wave 2"}, Servers: []string{"vc01.example.com"}, VMs: 3, Cutover: true}
	if !reflect.DeepEqual(claim, want) {
		t.Fatalf("claim = %+v, want %+v", claim, want)
	}
}
