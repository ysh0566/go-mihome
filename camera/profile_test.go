package camera

import "testing"

func TestMatchProfilePrefersExactMatchOverEarlierPrefix(t *testing.T) {
	t.Parallel()

	original := defaultProfiles
	t.Cleanup(func() {
		defaultProfiles = original
	})

	defaultProfiles = []Profile{
		{
			Name:     "broad-prefix",
			Prefixes: []string{"xiaomi.camera"},
		},
		{
			Name:        "exact-override",
			ExactModels: []string{"xiaomi.camera.v1"},
		},
	}

	profile := MatchProfile("xiaomi.camera.v1")
	if got, want := profile.Name, "exact-override"; got != want {
		t.Fatalf("profile.Name = %q, want %q", got, want)
	}
}

func TestMatchProfileUsesBuiltInFamiliesAndFallback(t *testing.T) {
	t.Parallel()

	cases := []struct {
		model string
		want  string
	}{
		{model: "mijia.camera.aw200", want: "mijia.camera.family"},
		{model: "xiaomi.camera.v1", want: "mijia.camera.family"},
		{model: "chuangmi.camera.060a02", want: "chuangmi.camera.family"},
		{model: "unknown.camera.v1", want: genericProfile.Name},
	}
	for _, tc := range cases {
		profile := MatchProfile(tc.model)
		if got := profile.Name; got != tc.want {
			t.Fatalf("MatchProfile(%q).Name = %q, want %q", tc.model, got, tc.want)
		}
	}
}
