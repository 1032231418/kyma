package automock

// AddonsConfiguration

func NewAddonsCfgLister() *addonsCfgLister {
	return new(addonsCfgLister)
}

func NewAddonsCfgMutations() *addonsCfgMutations {
	return new(addonsCfgMutations)
}

func NewAddonsCfgUpdater() *addonsCfgUpdater {
	return new(addonsCfgUpdater)
}

// Service Binding Usage

func NewServiceBindingUsageOperations() *serviceBindingUsageOperations {
	return new(serviceBindingUsageOperations)
}

func NewStatusBindingUsageExtractor() *statusBindingUsageExtractor {
	return new(statusBindingUsageExtractor)
}

// Usage Kind

func NewUsageKindServices() *usageKindServices {
	return new(usageKindServices)
}

// Bindable Resources

func NewBindableResourcesLister() *bindableResourceLister {
	return new(bindableResourceLister)
}
