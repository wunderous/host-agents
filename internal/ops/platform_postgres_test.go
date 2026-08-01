package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPlatformPostgresSystemdUnitUsesPersistentUserService(t *testing.T) {
	paths := platformPostgresPaths{
		DataDir:  "/home/opute/.local/share/opute/platform-postgres",
		BindHost: "127.0.0.1",
		Port:     5433,
	}
	unit := RenderPlatformPostgresSystemdUnit("/usr/lib/postgresql/16/bin/postgres", "/usr/lib/postgresql/16/bin/pg_ctl", paths)
	for _, expected := range []string{
		"Description=Opute platform PostgreSQL",
		"ExecStart='/usr/lib/postgresql/16/bin/postgres'",
		"-D '/home/opute/.local/share/opute/platform-postgres'",
		"Restart=on-failure",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, expected) {
			t.Fatalf("unit missing %q:\n%s", expected, unit)
		}
	}
}

func TestDefaultPlatformPostgresPathsRejectsUnsafeNetworkInput(t *testing.T) {
	if _, err := defaultPlatformPostgresPaths(PlatformPostgresArgs{BindHost: "127.0.0.1;touch /tmp/pwned"}); err == nil {
		t.Fatal("expected unsafe bind host to be rejected")
	}
	if _, err := defaultPlatformPostgresPaths(PlatformPostgresArgs{AllowedCIDRs: []string{"10.0.0.0/24\n"}}); err == nil {
		t.Fatal("expected unsafe allowed CIDR to be rejected")
	}
}

func TestPlatformPostgresCredentialsAreLocalOnly(t *testing.T) {
	root := t.TempDir()
	paths := platformPostgresPaths{
		ConfigDir:     filepath.Join(root, "config"),
		CredentialRef: filepath.Join(root, "config", "credentials.json"),
		Username:      "opute",
	}
	credentials, err := writePlatformPostgresCredentials(paths)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.Password == "" {
		t.Fatal("expected generated password")
	}
	info, err := os.Stat(paths.CredentialRef)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential file mode = %o, want 600", info.Mode().Perm())
	}
	loaded, err := readPlatformPostgresCredentials(paths)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Password != credentials.Password || loaded.Username != "opute" {
		t.Fatalf("loaded credentials did not round-trip: %#v", loaded)
	}
}

func TestEnsurePlatformPostgresCredentialsCreatesMissingReference(t *testing.T) {
	root := t.TempDir()
	paths := platformPostgresPaths{
		ConfigDir:     filepath.Join(root, "config"),
		CredentialRef: filepath.Join(root, "config", "credentials.json"),
		Username:      "opute",
	}
	credentials, err := ensurePlatformPostgresCredentials(paths)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.Username != "opute" || credentials.Password == "" {
		t.Fatalf("expected generated local credentials, got %#v", credentials)
	}
	if _, err := os.Stat(paths.CredentialRef); err != nil {
		t.Fatalf("credential reference was not created: %v", err)
	}
}

func TestPlatformPostgresConfigUsesUserWritableSocketDirectory(t *testing.T) {
	root := t.TempDir()
	paths := platformPostgresPaths{
		DataDir:      filepath.Join(root, "data"),
		ConfigDir:    filepath.Join(root, "config"),
		BindHost:     "127.0.0.1",
		Port:         5433,
		AllowedCIDRs: []string{},
	}
	if err := writePlatformPostgresConfig(paths); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(paths.DataDir, "postgresql.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "unix_socket_directories = '") || !strings.Contains(string(config), paths.ConfigDir) {
		t.Fatalf("config does not use the user-writable socket directory: %s", config)
	}
}
