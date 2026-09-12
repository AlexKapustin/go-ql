package walker

import "testing"

func TestArgBuilderNamedPassthrough(t *testing.T) {
	b := newArgBuilder()
	ph := b.Named("sku", "abc-123")
	if ph != "@sku" {
		t.Errorf("placeholder = %q, want @sku", ph)
	}
	if b.args["sku"] != "abc-123" {
		t.Errorf("args[sku] = %v", b.args["sku"])
	}
}

func TestArgBuilderPositionalNumbering(t *testing.T) {
	b := newArgBuilder()
	ph1 := b.Positional("1", "a")
	ph2 := b.Positional("2", "b")
	if ph1 != "@ql_pos_1" || ph2 != "@ql_pos_2" {
		t.Errorf("got %q, %q", ph1, ph2)
	}
	if b.args["ql_pos_1"] != "a" || b.args["ql_pos_2"] != "b" {
		t.Errorf("args = %v", b.args)
	}
}

func TestArgBuilderLiteralSequential(t *testing.T) {
	b := newArgBuilder()
	var got []string
	for i := 0; i < 5; i++ {
		got = append(got, b.Literal(i))
	}
	want := []string{"@ql_lit_1", "@ql_lit_2", "@ql_lit_3", "@ql_lit_4", "@ql_lit_5"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("literal %d = %q, want %q", i, got[i], w)
		}
	}
	for i := 0; i < 5; i++ {
		key := want[i][1:]
		if b.args[key] != i {
			t.Errorf("args[%s] = %v, want %d", key, b.args[key], i)
		}
	}
}

func TestArgBuilderNoCollisionAcrossKinds(t *testing.T) {
	b := newArgBuilder()
	b.Named("lit_1", "user-supplied") // a user could plausibly name a param this way
	b.Literal("synthesized")
	if b.args["lit_1"] != "user-supplied" {
		t.Fatalf("named arg clobbered: %v", b.args)
	}
	if b.args["ql_lit_1"] != "synthesized" {
		t.Fatalf("literal arg missing/clobbered: %v", b.args)
	}
}
