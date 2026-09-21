# Design

See ADR-0016. Catalog descriptors carry capabilityId. On provider activation,
DisplaceCapabilityFamilies retires other providers ops for claimed families
before ReplaceGeneration publishes the new ops.
