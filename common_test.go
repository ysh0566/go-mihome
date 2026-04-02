package miot

import "testing"

func TestCalcGroupID(t *testing.T) {
	got := CalcGroupID("100000001", "home-1")
	if got != "3ca66192999f0c3e" {
		t.Fatalf("CalcGroupID() = %q", got)
	}
}

func TestSlugifyDID(t *testing.T) {
	got := SlugifyDID("cn", "demo-device-1.s2")
	if got != "cn_demo_device_1_s2" {
		t.Fatalf("SlugifyDID() = %q", got)
	}
}

func TestSlugifyName(t *testing.T) {
	tests := map[string]struct {
		in   string
		want string
	}{
		"collapse punctuation": {
			in:   "--Hello!!! world---again__",
			want: "hello_world_again",
		},
		"trim separators": {
			in:   "__miot__",
			want: "miot",
		},
		"keep unicode letters": {
			in:   "你好，MIoT",
			want: "你好_miot",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := SlugifyName(tc.in)
			if got != tc.want {
				t.Fatalf("SlugifyName(%q) = %q", tc.in, got)
			}
		})
	}
}
