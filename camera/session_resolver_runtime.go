package camera

import (
	"context"
	"strings"
)

var newRuntimeCameraSessionResolverBackend = func(logger any) SessionResolverBackend {
	if !runtimeMiHomeSessionConfigured(context.Background()) {
		return nil
	}
	return NewMiHomeCameraSessionResolverBackend(logger)
}

func runtimeCameraStreamingConfigured() bool {
	if strings.TrimSpace(defaultFFmpegPath()) == "" {
		return false
	}
	return runtimeMiHomeSessionConfigured(context.Background())
}

func defaultRuntimeCameraSessionResolvers() []Resolver {
	return defaultRuntimeCameraSessionResolversWithBackend(newRuntimeCameraSessionResolverBackend(nil))
}

func defaultRuntimeCameraSessionResolversWithBackend(backend SessionResolverBackend) []Resolver {
	var resolvers []Resolver
	if backend != nil {
		resolvers = append(resolvers, NewDirectResolver(DirectResolverOptions{
			Name:    "runtime-local",
			Backend: backend,
		}))
	}
	return resolvers
}

func defaultRuntimeCameraProbeStreamClient(backend SessionResolverBackend) ProbeStreamer {
	return NewProbeStreamClient(ProbeStreamClientOptions{
		Backend: backend,
	})
}
