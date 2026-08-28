package hostruntime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEveryExportedMemberHasTwoDomainConsumers is §9.2 rule 2: a member earns
// its place here by being needed by two or more domains. One consumer means it
// belongs in that domain; zero means it belongs nowhere.
//
// Rule 1 (names no domain type) is enforced by the compiler and the
// hostruntime-knows-no-domains depguard rule. Rule 3 (identity, config, or an
// execution handle rather than an operation) is a judgement call and is
// recorded per raise at budgetLines. Rule 2 is countable, so it is tested.
// compositionRootMembers are exempt from rule 2 because no domain is supposed
// to consume them: they are how the composition root builds the runtime, or
// they are reached through a member that already passed the rule. A domain
// naming one of these directly is the bug rule 2 is looking for -- but that is
// a depguard question, not a consumer count.
var compositionRootMembers = map[string]string{
	"Config":                      "hostagent resolves it once at construction",
	"ResolveConfig":               "hostagent resolves it once at construction",
	"NewRuntime":                  "hostagent builds the runtime handle",
	"Runtime":                     "domains reach it through Shared.Runtime, never by name",
	"ID":                          "provider identity, chosen by config before any domain exists",
	"IDIncus":                     "provider identity, chosen by config before any domain exists",
	"DefaultProviderID":           "provider identity, chosen by config before any domain exists",
	"NormalizeProviderID":         "provider identity, chosen by config before any domain exists",
	"RequireSupportedPlatform":    "the process refuses to start; checked before domains",
	"ResourceRegistry":            "the persistence port hostagent injects into Shared",
	"Adopter":                     "the seam type of ResolveResource, supplied by hostagent",
	"NewInMemoryResourceRegistry": "the fallback registry hostagent installs when none is given",
	"SharedHostOwnershipError":    "produced by Shared.RequireSharedHostOwner; part of a member that passed",
}

func TestEveryExportedMemberHasTwoDomainConsumers(t *testing.T) {
	members := exportedMembers(t)
	if len(members) == 0 {
		t.Fatal("found no exported hostruntime members; the rule cannot pass on an empty scope")
	}

	consumers := map[string]map[string]bool{}
	for _, member := range members {
		if _, exempt := compositionRootMembers[member]; exempt {
			continue
		}
		consumers[member] = map[string]bool{}
	}

	domainRoot := filepath.Join("..", "domain")
	domains, err := os.ReadDir(domainRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range domains {
		if !domain.IsDir() {
			continue
		}
		body := packageText(t, filepath.Join(domainRoot, domain.Name()))
		for member := range consumers {
			if strings.Contains(body, "hostruntime."+member) || strings.Contains(body, "."+member+"(") {
				consumers[member][domain.Name()] = true
			}
		}
	}

	var lonely []string
	for member, seen := range consumers {
		if len(seen) < 2 {
			lonely = append(lonely, member+" ("+strings.Join(sortedKeys(seen), ",")+")")
		}
	}
	sort.Strings(lonely)
	if len(lonely) > 0 {
		t.Errorf("these hostruntime members have fewer than two domain consumers, "+
			"so §9.2 rule 2 says they belong in a domain package rather than here:\n  %s",
			strings.Join(lonely, "\n  "))
	}
}

// exportedMembers lists the exported top-level names hostruntime offers, which
// is what a domain can depend on.
func exportedMembers(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Clean(name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil || !d.Name.IsExported() {
					continue
				}
				names = append(names, d.Name.Name)
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							names = append(names, s.Name.Name)
						}
					case *ast.ValueSpec:
						for _, name := range s.Names {
							if name.IsExported() {
								names = append(names, name.Name)
							}
						}
					}
				}
			}
		}
	}
	sort.Strings(names)
	return names
}

func packageText(t *testing.T, dir string) string {
	t.Helper()
	var body strings.Builder
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		raw, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return err
		}
		body.Write(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return body.String()
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
