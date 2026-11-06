package protocols

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDnsSchemasCoverThreeOfficialKernels(t *testing.T) {
	schemas := DnsSchemas()
	if len(schemas) != 3 {
		t.Fatalf("got %d schemas want 3", len(schemas))
	}
	want := []struct{ kernel, format, doc string }{
		{"mihomo", "yaml", "https://wiki.metacubex.one/config/dns/"},
		{"sing-box", "json", "https://sing-box.sagernet.org/configuration/dns/"},
		{"xray-core", "json", "https://xtls.github.io/config/dns/"},
	}
	for i, w := range want {
		s := schemas[i]
		if s.Kernel != w.kernel || s.Format != w.format || s.DocURL != w.doc {
			t.Errorf("schema[%d] = %+v, want kernel=%s format=%s doc=%s", i, s, w.kernel, w.format, w.doc)
		}
		if s.Label == "" || s.Syntax == "" || s.DocName == "" || s.Description == "" || s.Example == "" {
			t.Errorf("schema %q has empty metadata", s.Kernel)
		}
		if len(s.Fields) == 0 {
			t.Errorf("schema %q has no fields", s.Kernel)
		}
		for _, f := range s.Fields {
			if f.Key == "" || f.Label == "" || f.Type == "" {
				t.Errorf("schema %q has incomplete field %+v", s.Kernel, f)
			}
			switch f.Type {
			case "text", "number", "bool", "select", "list":
			default:
				t.Errorf("schema %q field %q has invalid type %q", s.Kernel, f.Key, f.Type)
			}
			if f.Type == "select" && len(f.Options) == 0 {
				t.Errorf("schema %q select field %q has no options", s.Kernel, f.Key)
			}
		}
	}

	for _, s := range schemas {
		switch s.Format {
		case "json":
			if !json.Valid([]byte(s.Example)) {
				t.Errorf("%s example is not valid JSON", s.Kernel)
			}
		case "yaml":
			var v any
			if err := yaml.Unmarshal([]byte(s.Example), &v); err != nil {
				t.Errorf("%s example is not valid YAML: %v", s.Kernel, err)
			}
		}
	}

	schemas[0].Kernel = "mutated"
	if DnsSchemas()[0].Kernel == "mutated" {
		t.Fatal("DnsSchemas must return a copy")
	}
}
