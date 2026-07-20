package version

// Version is injected by release builds with -ldflags. Local builds retain a
// useful, explicitly non-release value.
var Version = "dev"
