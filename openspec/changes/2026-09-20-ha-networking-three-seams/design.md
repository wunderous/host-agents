# Design

See ADR-0016. Catalog descriptors carry capabilityId. On provider activation,
DisplaceCapabilityFamilies retires other providers ops for claimed families
before ReplaceGeneration publishes the new ops.


### mesh-runtime.v1
Install/configure agent + control-plane prerequisites as a separate Service Definition; membership enroll must not silently install.
