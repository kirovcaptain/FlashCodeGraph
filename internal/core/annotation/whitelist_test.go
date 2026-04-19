package annotation

import "testing"

func TestBuildWhitelist_Spring(t *testing.T) {
	wl := BuildWhitelist([]string{"spring"}, nil, nil)
	for _, name := range []string{"RestController", "Service", "Transactional", "Deprecated"} {
		if _, ok := wl[name]; !ok {
			t.Errorf("expected %s in whitelist", name)
		}
	}
	if wl["Service"].Framework != "spring" {
		t.Error("expected framework=spring for Service")
	}
}

func TestBuildWhitelist_MultiFramework(t *testing.T) {
	wl := BuildWhitelist([]string{"spring", "mybatis"}, nil, nil)
	if _, ok := wl["Mapper"]; !ok {
		t.Error("expected Mapper from mybatis")
	}
	if _, ok := wl["Service"]; !ok {
		t.Error("expected Service from spring")
	}
}

func TestBuildWhitelist_Include(t *testing.T) {
	wl := BuildWhitelist([]string{"spring"}, []string{"ApiOperation"}, nil)
	def, ok := wl["ApiOperation"]
	if !ok {
		t.Fatal("expected ApiOperation in whitelist")
	}
	if def.Framework != "custom" {
		t.Errorf("expected framework=custom, got %s", def.Framework)
	}
}

func TestBuildWhitelist_Exclude(t *testing.T) {
	wl := BuildWhitelist([]string{"spring"}, nil, []string{"Value"})
	if _, ok := wl["Value"]; ok {
		t.Error("Value should be excluded")
	}
	if _, ok := wl["Service"]; !ok {
		t.Error("Service should still be present")
	}
}

func TestBuildWhitelist_Empty(t *testing.T) {
	wl := BuildWhitelist(nil, nil, nil)
	if _, ok := wl["Deprecated"]; !ok {
		t.Error("_always annotations should be present even with no frameworks")
	}
	if len(wl) < 1 {
		t.Error("should have at least _always + _test annotations")
	}
}

func TestExtractBaseName(t *testing.T) {
	tests := []struct{ input, want string }{
		{"@Service", "Service"},
		{"@Transactional(readOnly=true)", "Transactional"},
		{"@org.springframework.stereotype.Service", "Service"},
		{"@strawberry.field", "strawberry.field"},
		{"@app.route(\"/api\")", "app.route"},
		{"@DubboReference", "DubboReference"},
		{"Service", "Service"},
	}
	for _, tt := range tests {
		got := ExtractBaseName(tt.input)
		if got != tt.want {
			t.Errorf("ExtractBaseName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractParams(t *testing.T) {
	tests := []struct{ input, want string }{
		{`@Cacheable(value="users", key="#id")`, `value="users", key="#id"`},
		{"@Service", ""},
		{"@RequestMapping(\"/api\")", "\"/api\""},
	}
	for _, tt := range tests {
		got := ExtractParams(tt.input)
		if got != tt.want {
			t.Errorf("ExtractParams(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildWhitelist_FrameworkField(t *testing.T) {
	wl := BuildWhitelist([]string{"spring", "dubbo"}, nil, nil)
	for _, def := range wl {
		if def.Framework == "" {
			t.Errorf("annotation %s has empty framework", def.Name)
		}
	}
}
