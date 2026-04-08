package camera

import "testing"

func TestLookupSupportRecognizesDualCameraModel(t *testing.T) {
	t.Parallel()

	info, ok, err := LookupSupport("xiaomi.camera.082ac1")
	if err != nil {
		t.Fatalf("LookupSupport() error = %v", err)
	}
	if !ok {
		t.Fatal("LookupSupport() ok = false, want true")
	}
	if got, want := info.ChannelCount, 2; got != want {
		t.Fatalf("info.ChannelCount = %d, want %d", got, want)
	}
}

func TestLookupSupportDefaultsUnknownSupportedCameraToSingleChannel(t *testing.T) {
	t.Parallel()

	info, ok, err := LookupSupport("mijia.camera.aw200")
	if err != nil {
		t.Fatalf("LookupSupport() error = %v", err)
	}
	if !ok {
		t.Fatal("LookupSupport() ok = false, want true")
	}
	if got, want := info.ChannelCount, 1; got != want {
		t.Fatalf("info.ChannelCount = %d, want %d", got, want)
	}
}

func TestLookupSupportRejectsBlacklistedAndNonCameraModels(t *testing.T) {
	t.Parallel()

	for _, model := range []string{"isa.camera.virtual", "yeelight.light.ceiling1"} {
		info, ok, err := LookupSupport(model)
		if err != nil {
			t.Fatalf("LookupSupport(%q) error = %v", model, err)
		}
		if ok {
			t.Fatalf("LookupSupport(%q) = %+v, true; want unsupported", model, info)
		}
	}
}
