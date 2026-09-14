package xpath_test

import (
	"os"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// fixedEnvironment exposes exactly the variables it was given, and nothing of
// the process's own -- the shape a caller who needs these functions at all
// should be reaching for.
type fixedEnvironment map[string]string

func (e fixedEnvironment) LookupEnvironment(name string) (string, bool) {
	v, ok := e[name]
	return v, ok
}

func (e fixedEnvironment) EnvironmentNames() []string {
	names := make([]string, 0, len(e))
	for k := range e {
		names = append(names, k)
	}
	return names
}

func evalEnv(t *testing.T, ctx *xpath.Context, expr string) []string {
	t.Helper()
	seq, err := mustCompileEnv(t, expr).Eval(ctx)
	if err != nil {
		t.Fatalf("evaluating %q: %v", expr, err)
	}
	out := make([]string, 0, len(seq))
	for _, it := range seq {
		a, ok := it.(*xdm.Atomic)
		if !ok {
			t.Fatalf("evaluating %q: item %T is not atomic", expr, it)
		}
		out = append(out, a.Str())
	}
	return out
}

// A context with no EnvironmentResolver withholds the process environment
// from both functions. Not an error -- the empty sequence, which is what a
// stylesheet would see on a machine where the variable is genuinely unset, and
// what makes the gate cost no conformance.
func TestEnvironmentWithheldByDefault(t *testing.T) {
	t.Setenv("GOXML_TEST_SECRET", "sk-live-DO-NOT-LEAK")

	ctx := xpath.NewContext(nil, xpath.Builtins())
	ctx.Version = xpath.XPath31

	if got := evalEnv(t, ctx, `environment-variable('GOXML_TEST_SECRET')`); len(got) != 0 {
		t.Errorf("environment-variable leaked the process environment: %q", got)
	}
	if got := evalEnv(t, ctx, `available-environment-variables()`); len(got) != 0 {
		t.Errorf("available-environment-variables enumerated %d names, want none: %q",
			len(got), got)
	}
	// The withheld answer must be indistinguishable from an unset variable,
	// so it is the empty sequence and not an error or a zero-length string.
	if got := evalEnv(t, ctx, `string(count(environment-variable('GOXML_TEST_SECRET')))`); got[0] != "0" {
		t.Errorf("count() = %q, want 0 -- the answer must be the empty sequence", got[0])
	}
	// The type check on the argument survives the gate, so the QT3 cases
	// asserting XPTY0004 still get their error rather than the empty sequence.
	if _, err := mustCompileEnv(t, `environment-variable(1)`).Eval(ctx); err == nil ||
		!strings.Contains(err.Error(), "XPTY0004") {
		t.Errorf("environment-variable(1) error = %v, want XPTY0004", err)
	}
}

func mustCompileEnv(t *testing.T, expr string) *xpath.Compiled {
	t.Helper()
	c, err := xpath.CompileVersion(expr, nil, xpath.XPath31)
	if err != nil {
		t.Fatalf("compiling %q: %v", expr, err)
	}
	return c
}

// A caller that opts in gets exactly what the resolver exposes, and nothing
// else: a variable the process holds but the resolver does not name stays
// invisible, which is the point of making the grant a set of names rather than
// a boolean.
func TestEnvironmentOptIn(t *testing.T) {
	t.Setenv("GOXML_TEST_SECRET", "sk-live-DO-NOT-LEAK")

	ctx := xpath.NewContext(nil, xpath.Builtins())
	ctx.Version = xpath.XPath31
	ctx.Environment = fixedEnvironment{"REPORT_MODE": "summary"}

	if got := evalEnv(t, ctx, `environment-variable('REPORT_MODE')`); len(got) != 1 || got[0] != "summary" {
		t.Errorf("environment-variable('REPORT_MODE') = %q, want [summary]", got)
	}
	if got := evalEnv(t, ctx, `available-environment-variables()`); len(got) != 1 || got[0] != "REPORT_MODE" {
		t.Errorf("available-environment-variables() = %q, want [REPORT_MODE]", got)
	}
	if got := evalEnv(t, ctx, `environment-variable('GOXML_TEST_SECRET')`); len(got) != 0 {
		t.Errorf("a resolver that does not name the variable still leaked it: %q", got)
	}
}

// OSEnvironment is the widest grant and has to be asked for by name. It is
// tested because it is the implementation a caller reaching for "just make it
// work" will pick, and it must actually work.
func TestOSEnvironmentReadsTheProcess(t *testing.T) {
	t.Setenv("GOXML_TEST_SECRET", "sk-live-DO-NOT-LEAK")

	ctx := xpath.NewContext(nil, xpath.Builtins())
	ctx.Version = xpath.XPath31
	ctx.Environment = xpath.OSEnvironment{}

	if got := evalEnv(t, ctx, `environment-variable('GOXML_TEST_SECRET')`); len(got) != 1 ||
		got[0] != "sk-live-DO-NOT-LEAK" {
		t.Errorf("OSEnvironment lookup = %q, want the planted value", got)
	}
	names := evalEnv(t, ctx, `available-environment-variables()`)
	if len(names) < len(os.Environ()) {
		t.Errorf("OSEnvironment enumerated %d names, process has %d",
			len(names), len(os.Environ()))
	}
	var found bool
	for _, n := range names {
		if n == "GOXML_TEST_SECRET" {
			found = true
		}
	}
	if !found {
		t.Error("OSEnvironment did not enumerate the planted variable")
	}
}
