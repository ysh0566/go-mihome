package miot

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

const testSpecURN = "urn:miot-spec-v2:device:thermostat:0000A031:tofan-wk01:1:0000C822"

func TestSpecParserParsesFixtureURN(t *testing.T) {
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parser := newFixtureSpecParser(t, store, map[string][]string{
		testSpecURN: {"testdata/spec/tofan_wk01_instance.json"},
	})

	spec, err := parser.Parse(context.Background(), testSpecURN)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "thermostat" {
		t.Fatalf("name = %q", spec.Name)
	}
	if len(spec.Services) != 2 {
		t.Fatalf("service len = %d", len(spec.Services))
	}

	thermostat := findSpecService(t, spec, 2)
	if thermostat.DescriptionTrans != "Thermostat" {
		t.Fatalf("service translation = %q", thermostat.DescriptionTrans)
	}
	mode := findSpecProperty(t, thermostat, 2)
	if mode.DescriptionTrans != "Air Conditioner Mode" {
		t.Fatalf("property translation = %q", mode.DescriptionTrans)
	}
	on := findSpecProperty(t, thermostat, 1)
	if len(on.ValueList.Items) != 2 {
		t.Fatalf("bool value list len = %d", len(on.ValueList.Items))
	}
	if on.ValueList.Items[0].Description != "Close" || on.ValueList.Items[1].Description != "Open" {
		t.Fatalf("bool descriptions = %#v", on.ValueList.Items)
	}
	if len(thermostat.Actions) != 1 {
		t.Fatalf("action len = %d", len(thermostat.Actions))
	}
}

func TestSpecParserUsesCache(t *testing.T) {
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parser := newFixtureSpecParser(t, store, map[string][]string{
		testSpecURN: {"testdata/spec/tofan_wk01_instance.json"},
	})

	if _, err := parser.Parse(context.Background(), testSpecURN); err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Parse(context.Background(), testSpecURN); err != nil {
		t.Fatal(err)
	}
	if calls := parser.doer.instanceCalls(testSpecURN); calls != 1 {
		t.Fatalf("instance calls = %d", calls)
	}
}

func TestSpecParserRefreshBypassesCache(t *testing.T) {
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parser := newFixtureSpecParser(t, store, map[string][]string{
		testSpecURN: {
			"testdata/spec/tofan_wk01_instance.json",
			"testdata/spec/tofan_wk01_instance_v2.json",
		},
	})

	spec, err := parser.Parse(context.Background(), testSpecURN)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Description != "Fixture Thermostat" {
		t.Fatalf("description = %q", spec.Description)
	}
	refreshed, err := parser.Refresh(context.Background(), []string{testSpecURN})
	if err != nil {
		t.Fatal(err)
	}
	if refreshed != 1 {
		t.Fatalf("refresh count = %d", refreshed)
	}
	spec, err = parser.Parse(context.Background(), testSpecURN)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Description != "Fixture Thermostat v2" {
		t.Fatalf("description after refresh = %q", spec.Description)
	}
	if calls := parser.doer.instanceCalls(testSpecURN); calls != 2 {
		t.Fatalf("instance calls = %d", calls)
	}
}

type fixtureSpecParser struct {
	*SpecParser
	doer *specFixtureDoer
}

func newFixtureSpecParser(t *testing.T, store *Storage, fixtures map[string][]string) fixtureSpecParser {
	t.Helper()

	doer := newSpecFixtureDoer(t, fixtures)
	parser, err := NewSpecParser("en", store, WithSpecHTTPClient(doer))
	if err != nil {
		t.Fatal(err)
	}
	return fixtureSpecParser{SpecParser: parser, doer: doer}
}

type specFixtureDoer struct {
	t              *testing.T
	instanceBodies map[string][][]byte
	instanceCount  map[string]int
}

func newSpecFixtureDoer(t *testing.T, fixtures map[string][]string) *specFixtureDoer {
	t.Helper()

	doer := &specFixtureDoer{
		t:              t,
		instanceBodies: make(map[string][][]byte),
		instanceCount:  make(map[string]int),
	}
	for urn, paths := range fixtures {
		for _, path := range paths {
			data, err := os.ReadFile(filepath.Clean(path))
			if err != nil {
				t.Fatal(err)
			}
			doer.instanceBodies[urn] = append(doer.instanceBodies[urn], data)
		}
	}
	return doer
}

func (d *specFixtureDoer) Do(req *http.Request) (*http.Response, error) {
	switch req.URL.Host {
	case "miot-spec.org":
		switch req.URL.Path {
		case "/miot-spec-v2/instance":
			urn := req.URL.Query().Get("type")
			d.instanceCount[urn]++
			bodies := d.instanceBodies[urn]
			if len(bodies) == 0 {
				d.t.Fatalf("missing fixture for urn %s", urn)
			}
			index := d.instanceCount[urn] - 1
			if index >= len(bodies) {
				index = len(bodies) - 1
			}
			return jsonResponse(string(bodies[index])), nil
		case "/instance/v2/multiLanguage":
			return jsonResponse(`{"data":{}}`), nil
		default:
			d.t.Fatalf("unexpected miot-spec path %s", req.URL.Path)
		}
	default:
		d.t.Fatalf("unexpected host %s", req.URL.Host)
	}
	return nil, nil
}

func (d *specFixtureDoer) instanceCalls(urn string) int {
	return d.instanceCount[urn]
}

func findSpecService(t *testing.T, spec SpecInstance, iid int) SpecService {
	t.Helper()
	for _, service := range spec.Services {
		if service.IID == iid {
			return service
		}
	}
	t.Fatalf("service %d not found", iid)
	return SpecService{}
}

func findSpecProperty(t *testing.T, service SpecService, iid int) SpecProperty {
	t.Helper()
	for _, property := range service.Properties {
		if property.IID == iid {
			return property
		}
	}
	t.Fatalf("property %d not found", iid)
	return SpecProperty{}
}
