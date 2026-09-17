package ratio_setting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// These tests mutate package-global rules and must not use t.Parallel.
func preserveModelDiscountRules(t *testing.T) {
	t.Helper()
	previous := currentModelDiscountSnapshot.Load()
	t.Cleanup(func() { currentModelDiscountSnapshot.Store(previous) })
}

func setModelDiscountRules(t *testing.T, raw string) *ModelDiscountSnapshot {
	t.Helper()
	if err := UpdateModelDiscountRulesJSON(raw); err != nil {
		t.Fatalf("update rules: %v", err)
	}
	return CaptureModelDiscountRules()
}

func TestModelDiscountDefault(t *testing.T) {
	preserveModelDiscountRules(t)
	currentModelDiscountSnapshot.Store(nil)
	if got := ModelDiscountRulesJSONString(); got != `{"groups":{},"users":{}}` {
		t.Fatalf("default JSON = %q", got)
	}
	var nilSnapshot *ModelDiscountSnapshot
	for _, snapshot := range []*ModelDiscountSnapshot{CaptureModelDiscountRules(), nilSnapshot, {}} {
		got := snapshot.Lookup(123, "vip", "doubao-seedance-2.0")
		if got.Factor != 1 || got.Source != "default" || got.Model != "doubao-seedance-2.0" {
			t.Fatalf("default lookup = %+v", got)
		}
		if got.Revision != emptyModelDiscountSnapshot.revision || len(got.Revision) != 64 {
			t.Fatalf("invalid default revision %q", got.Revision)
		}
	}
}

func TestModelDiscountPrecedenceAndExactNames(t *testing.T) {
	preserveModelDiscountRules(t)
	snapshot := setModelDiscountRules(t, `{
		"groups":{"vip":{"doubao-seedance-2.0":0.8,"free":0,"full":1,"user-full":0.4,"gpt-*":0.2,"name@thinking:on":0.3}},
		"users":{"123":{"doubao-seedance-2.0":0.7,"free":1,"full":0,"user-full":1,"personal-only":0.5}}
	}`)
	tests := []struct {
		name, group, model, source string
		user                       int
		factor                     float64
	}{
		{"personal wins", "vip", "doubao-seedance-2.0", "user", 123, 0.7},
		{"group fallback", "vip", "doubao-seedance-2.0", "group", 456, 0.8},
		{"user without group", "missing", "doubao-seedance-2.0", "user", 123, 0.7},
		{"personal one beats group zero", "vip", "free", "user", 123, 1},
		{"personal zero", "vip", "full", "user", 123, 0},
		{"personal one beats discounted group", "vip", "user-full", "user", 123, 1},
		{"group zero", "vip", "free", "group", 456, 0},
		{"group one", "vip", "full", "group", 456, 1},
		{"personal only", "vip", "personal-only", "user", 123, 0.5},
		{"no rule", "vip", "missing", "default", 123, 1},
		{"no group", "missing", "free", "default", 456, 1},
		{"zero user falls back", "vip", "free", "group", 0, 0},
		{"negative user falls back", "vip", "free", "group", -1, 0},
		{"alias isolated", "vip", "seedance-alias", "default", 123, 1},
		{"suffix isolated", "vip", "doubao-seedance-2.0@thinking:on", "default", 123, 1},
		{"case isolated", "vip", "Doubao-seedance-2.0", "default", 123, 1},
		{"no lookup trimming", "vip", " doubao-seedance-2.0", "default", 123, 1},
		{"no group trimming", " vip", "free", "default", 456, 1},
		{"no wildcard expansion", "vip", "gpt-4", "default", 123, 1},
		{"literal star ID", "vip", "gpt-*", "group", 123, 0.2},
		{"no suffix inheritance", "vip", "name", "default", 123, 1},
		{"complete suffix ID", "vip", "name@thinking:on", "group", 123, 0.3},
		{"empty lookup", "", "", "default", 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := snapshot.Lookup(tt.user, tt.group, tt.model)
			if got.Factor != tt.factor || got.Source != tt.source || got.Model != tt.model || got.Revision != snapshot.revision {
				t.Fatalf("lookup = %+v, want factor=%v source=%s model=%q", got, tt.factor, tt.source, tt.model)
			}
		})
	}
}

func TestModelDiscountSnapshotAndJSONIsolation(t *testing.T) {
	preserveModelDiscountRules(t)
	old := setModelDiscountRules(t, `{"groups":{"vip":{"m":0.8}},"users":{"123":{"m":0.7}}}`)
	oldSelection := old.Lookup(123, "vip", "m")
	raw := ModelDiscountRulesJSONString()
	var detached ModelDiscountRules
	if err := json.Unmarshal([]byte(raw), &detached); err != nil {
		t.Fatal(err)
	}
	detached.Groups["vip"]["m"] = 0
	detached.Users["123"]["m"] = 0
	delete(detached.Users, "123")
	bytes := []byte(raw)
	bytes[0] = '['
	if ModelDiscountRulesJSONString() != raw || old.Lookup(123, "vip", "m") != oldSelection || old.Lookup(456, "vip", "m").Factor != 0.8 {
		t.Fatal("mutating exported JSON affected live rules")
	}
	oldSelection.Factor = 0
	if old.Lookup(123, "vip", "m").Factor != 0.7 {
		t.Fatal("mutating selection affected live rules")
	}
	newSnapshot := setModelDiscountRules(t, `{"groups":{"vip":{"m":0.2}}}`)
	if old.Lookup(123, "vip", "m").Factor != 0.7 || old.Lookup(456, "vip", "m").Factor != 0.8 {
		t.Fatal("old snapshot changed after publication")
	}
	if got := newSnapshot.Lookup(123, "vip", "m"); got.Factor != 0.2 || got.Source != "group" || got.Revision == old.revision {
		t.Fatalf("new snapshot lookup = %+v", got)
	}
	setModelDiscountRules(t, `{}`)
	if got := CaptureModelDiscountRules().Lookup(123, "vip", "m"); got.Factor != 1 || got.Source != "default" {
		t.Fatalf("empty replacement did not clear rules: %+v", got)
	}
	if old.Lookup(123, "vip", "m").Factor != 0.7 || newSnapshot.Lookup(123, "vip", "m").Factor != 0.2 {
		t.Fatal("clearing current rules changed retained snapshots")
	}
}

func TestModelDiscountCanonicalRevision(t *testing.T) {
	preserveModelDiscountRules(t)
	sets := [][]string{
		{`{}`, ` {"users":{}} `, `{"groups":{}}`, `{"users":{},"groups":{}}`, `{"groups":{"vip":{}},"users":{"123":{}}}`},
		{
			`{"groups":{"vip":{"b":1,"a":0.8}},"users":{"123":{"m":0}}}`,
			`{"users":{"123":{"m":-0.0}},"groups":{"vip":{"a":8e-1,"b":1.00}}}`,
			`{"groups":{"empty":{},"vip":{"\u0061":0.8000,"b":1}},"users":{"123":{"m":0e5}}}`,
		},
		{
			`{"users":{"2":{"m":0.2},"123":{"m":0.7}},"groups":{"z":{"m":0.4},"a":{"m":0.3}}}`,
			`{"groups":{"a":{"m":0.3},"z":{"m":0.4}},"users":{"123":{"m":0.7},"2":{"m":0.2}}}`,
		},
	}
	for _, equivalents := range sets {
		var revision, canonical string
		for i, raw := range equivalents {
			snapshot := setModelDiscountRules(t, raw)
			gotJSON := ModelDiscountRulesJSONString()
			if i == 0 {
				revision, canonical = snapshot.revision, gotJSON
			}
			if snapshot.revision != revision || gotJSON != canonical {
				t.Fatalf("noncanonical %s: JSON=%s revision=%s", raw, gotJSON, snapshot.revision)
			}
			digest := sha256.Sum256([]byte(gotJSON))
			if snapshot.revision != hex.EncodeToString(digest[:]) {
				t.Fatal("revision is not SHA256 of canonical JSON")
			}
		}
	}
	before := setModelDiscountRules(t, `{"groups":{"vip":{"m":0.7}}}`).revision
	if after := setModelDiscountRules(t, `{"groups":{"vip":{"m":0.8}}}`).revision; after == before {
		t.Fatal("changed factor retained old revision")
	}
}

func TestModelDiscountRejectsInvalidWithoutPublishing(t *testing.T) {
	preserveModelDiscountRules(t)
	before := setModelDiscountRules(t, `{"groups":{"vip":{"m":0.8}},"users":{"123":{"m":0.7}}}`)
	beforeJSON := ModelDiscountRulesJSONString()
	invalid := map[string]string{
		"empty input": "", "whitespace input": " \n\t ",
		"top null": `null`, "top array": `[]`, "top bool": `true`, "top string": `"x"`, "top number": `1`,
		"unknown key": `{"other":{}}`, "wrong key case": `{"Groups":{}}`,
		"trailing object": `{} {}`, "trailing null": `{} null`, "trailing junk": `{} junk`,
		"truncated": `{"groups":`, "unclosed": `{"groups":{}`, "trailing comma": `{"groups":{},}`,
		"groups null": `{"groups":null}`, "users null": `{"users":null}`,
		"groups array": `{"groups":[]}`, "users array": `{"users":[]}`,
		"groups bool": `{"groups":true}`, "users number": `{"users":1}`,
		"group map null": `{"groups":{"vip":null}}`, "user map null": `{"users":{"123":null}}`,
		"group map array": `{"groups":{"vip":[]}}`, "user map scalar": `{"users":{"123":0.7}}`,
		"factor null": `{"groups":{"vip":{"m":null}}}`, "user factor null": `{"users":{"123":{"m":null}}}`,
		"factor bool": `{"groups":{"vip":{"m":false}}}`, "factor string": `{"groups":{"vip":{"m":"0.8"}}}`,
		"factor object": `{"groups":{"vip":{"m":{}}}}`, "factor array": `{"groups":{"vip":{"m":[]}}}`,
		"negative": `{"groups":{"vip":{"m":-0.01}}}`, "over one": `{"users":{"123":{"m":1.01}}}`,
		"nan": `{"groups":{"vip":{"m":NaN}}}`, "infinity": `{"groups":{"vip":{"m":Infinity}}}`,
		"negative infinity": `{"groups":{"vip":{"m":-Infinity}}}`, "overflow": `{"groups":{"vip":{"m":1e309}}}`,
		"negative underflow":         `{"groups":{"vip":{"m":-1e-10000}}}`,
		"rounded above one":          `{"groups":{"vip":{"m":1.00000000000000001}}}`,
		"rounded above one exponent": `{"groups":{"vip":{"m":100000000000000001e-17}}}`,
		"non JSON number":            `{"groups":{"vip":{"m":.8}}}`,
		"duplicate top":              `{"groups":{},"groups":{}}`, "duplicate users": `{"users":{},"users":{}}`,
		"escaped top duplicate": `{"groups":{},"\u0067roups":{}}`,
		"duplicate group":       `{"groups":{"vip":{},"vip":{}}}`, "duplicate user": `{"users":{"123":{},"123":{}}}`,
		"duplicate group model":   `{"groups":{"vip":{"m":0.8,"m":0.7}}}`,
		"duplicate user model":    `{"users":{"123":{"m":0.8,"m":0.7}}}`,
		"escaped duplicate model": `{"groups":{"vip":{"m":0.8,"\u006d":0.7}}}`,
		"invalid UTF8":            "{\"groups\":{\"\xff\":{}}}",
		"too many bytes":          strings.Repeat(" ", modelDiscountMaxJSONBytes-1) + `{}`,
	}
	for _, name := range []string{"", " ", " vip", "vip ", "\tvip", "vip\n", "\u00a0vip", "vip\u3000"} {
		encoded, _ := json.Marshal(name)
		invalid["group name "+strconv.Quote(name)] = `{"groups":{` + string(encoded) + `:{}}}`
		invalid["model name "+strconv.Quote(name)] = `{"groups":{"vip":{` + string(encoded) + `:0.7}}}`
	}
	for _, name := range []string{strings.Repeat("m", 201), strings.Repeat("模", 67)} {
		encoded, _ := json.Marshal(name)
		invalid["long model "+strconv.Itoa(len(name))] = `{"users":{"123":{` + string(encoded) + `:0.7}}}`
	}
	for _, id := range []string{"", "0", "-1", "+1", "01", " 1", "1 ", "1.0", "1e2", "abc", "１２３", "18446744073709551616", "9223372036854775808"} {
		encoded, _ := json.Marshal(id)
		invalid["user ID "+strconv.Quote(id)] = `{"users":{` + string(encoded) + `:{"m":0.7}}}`
	}
	for name, raw := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := ValidateModelDiscountRulesJSON(raw); err == nil {
				t.Fatal("validation accepted invalid JSON")
			}
			if err := UpdateModelDiscountRulesJSON(raw); err == nil {
				t.Fatal("update accepted invalid JSON")
			}
			if CaptureModelDiscountRules() != before || ModelDiscountRulesJSONString() != beforeJSON {
				t.Fatal("failed update changed active snapshot")
			}
		})
	}
	if err := ValidateModelDiscountRulesJSON(`{"groups":{"vip":{"m":0.1}}}`); err != nil {
		t.Fatal(err)
	}
	if CaptureModelDiscountRules() != before || ModelDiscountRulesJSONString() != beforeJSON {
		t.Fatal("validation published rules")
	}
}

func TestModelDiscountDecimalRange(t *testing.T) {
	for _, raw := range []string{"0", "-0", "-0.000e9999999", "1", "1.000", "0.1e1", "10e-1", "1000e-3", "0.99999999999999999", "1e-10000", "0.01e+1", "1e+0000000000"} {
		if !modelDiscountNumberInRange(raw) {
			t.Errorf("rejected in-range decimal %s", raw)
		}
		if err := ValidateModelDiscountRulesJSON(`{"groups":{"vip":{"m":` + raw + `}}}`); err != nil {
			t.Errorf("rejected valid factor %s: %v", raw, err)
		}
	}
	for _, raw := range []string{"-0.1", "-1e-10000", "1.00000000000000001", "100000000000000001e-17", "0.100000000000000001e1", "2", "10", "1e99999999"} {
		if modelDiscountNumberInRange(raw) {
			t.Errorf("accepted out-of-range decimal %s", raw)
		}
	}
}

func TestModelDiscountLimits(t *testing.T) {
	preserveModelDiscountRules(t)
	for _, model := range []string{strings.Repeat("m", 200), strings.Repeat("模", 66) + "ab"} {
		encoded, _ := json.Marshal(model)
		raw := fmt.Sprintf(`{"groups":{"vip":{%s:0}},"users":{"%d":{%s:1}}}`, encoded, int(^uint(0)>>1), encoded)
		if err := ValidateModelDiscountRulesJSON(raw); err != nil {
			t.Fatalf("valid boundary model/user rejected: %v", err)
		}
	}
	if err := ValidateModelDiscountRulesJSON(strings.Repeat(" ", modelDiscountMaxJSONBytes-2) + `{}`); err != nil {
		t.Fatalf("exact byte limit rejected: %v", err)
	}
	rules := ModelDiscountRules{
		Groups: map[string]map[string]float64{"vip": {}},
		Users:  map[string]map[string]float64{"123": {}},
	}
	for i := 0; i < modelDiscountMaxRules; i++ {
		models := rules.Groups["vip"]
		if i%2 == 1 {
			models = rules.Users["123"]
		}
		models[fmt.Sprintf("m-%d", i)] = 0.8
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	before := setModelDiscountRules(t, string(raw))
	rules.Users["123"]["one-too-many"] = 0.7
	raw, err = json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateModelDiscountRulesJSON(string(raw)); err == nil {
		t.Fatal("accepted more than 10000 total rules")
	}
	if err := UpdateModelDiscountRulesJSON(string(raw)); err == nil || CaptureModelDiscountRules() != before {
		t.Fatal("oversized update was accepted or published")
	}
}

func TestModelDiscountConcurrentCaptureAndUpdate(t *testing.T) {
	preserveModelDiscountRules(t)
	inputs := []string{
		`{}`,
		`{"groups":{"vip":{"m":0.8}},"users":{"123":{"m":0.7}}}`,
		`{"groups":{"vip":{"m":0}},"users":{"123":{"m":1}}}`,
	}
	// Expected pairs share a revision, detecting partial/mixed publications.
	expected := make(map[string][2]ModelDiscountSelection)
	for _, raw := range inputs {
		snapshot := setModelDiscountRules(t, raw)
		expected[snapshot.revision] = [2]ModelDiscountSelection{
			snapshot.Lookup(123, "vip", "m"), snapshot.Lookup(456, "vip", "m"),
		}
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for writer := 0; writer < 2; writer++ {
		workers.Add(1)
		go func(offset int) {
			defer workers.Done()
			<-start
			for i := 0; i < 200; i++ {
				if err := UpdateModelDiscountRulesJSON(inputs[(i+offset)%len(inputs)]); err != nil {
					t.Errorf("concurrent update: %v", err)
					return
				}
			}
		}(writer)
	}
	for reader := 0; reader < 6; reader++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for i := 0; i < 300; i++ {
				snapshot := CaptureModelDiscountRules()
				personal := snapshot.Lookup(123, "vip", "m")
				group := snapshot.Lookup(456, "vip", "m")
				pair, exists := expected[personal.Revision]
				if !exists || personal != pair[0] || group != pair[1] {
					t.Errorf("inconsistent snapshot: %+v / %+v", personal, group)
					return
				}
				if err := ValidateModelDiscountRulesJSON(ModelDiscountRulesJSONString()); err != nil {
					t.Errorf("invalid concurrent JSON: %v", err)
					return
				}
				if snapshot.Lookup(123, "vip", "m") != personal {
					t.Error("captured snapshot changed across concurrent reads")
					return
				}
			}
		}()
	}
	close(start)
	workers.Wait()
}
