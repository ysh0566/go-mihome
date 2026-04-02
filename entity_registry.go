package miot

// EntityRegistry builds generic devices and entities from parsed specs.
type EntityRegistry struct {
	backend EntityBackend
}

// NewDevice builds a generic device view directly from device info and a parsed spec.
func NewDevice(info DeviceInfo, spec SpecInstance, backend EntityBackend) (Device, error) {
	return NewEntityRegistry(backend).Build(info, spec)
}

// NewEntityRegistry creates a generic entity registry.
func NewEntityRegistry(backend EntityBackend) *EntityRegistry {
	return &EntityRegistry{backend: backend}
}

// Build constructs a generic device view from device info and a parsed spec.
func (r *EntityRegistry) Build(info DeviceInfo, spec SpecInstance) (Device, error) {
	device := Device{
		Descriptor: describeDevice(spec),
		Info:       info,
		Spec:       spec,
		Entities:   []*Entity{},
	}
	for _, service := range spec.Services {
		device.Entities = append(device.Entities, &Entity{
			deviceID: info.DID,
			desc:     describeServiceEntity(service),
			backend:  r.backend,
		})
		for _, property := range service.Properties {
			device.Entities = append(device.Entities, &Entity{
				deviceID: info.DID,
				desc:     describePropertyEntity(service, property),
				backend:  r.backend,
			})
		}
		for _, event := range service.Events {
			device.Entities = append(device.Entities, &Entity{
				deviceID: info.DID,
				desc:     describeEventEntity(service, event),
				backend:  r.backend,
			})
		}
		for _, action := range service.Actions {
			device.Entities = append(device.Entities, &Entity{
				deviceID: info.DID,
				desc:     describeActionEntity(service, action),
				backend:  r.backend,
			})
		}
	}
	return device, nil
}

// EntityByKey finds one entity by its stable key.
func (d Device) EntityByKey(key string) *Entity {
	for _, entity := range d.Entities {
		if entity != nil && entity.desc.Key == key {
			return entity
		}
	}
	return nil
}
