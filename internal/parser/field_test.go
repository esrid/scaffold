package parser

import (
	"testing"
)

func TestParseField_nn(t *testing.T) {
	fields, err := ParseFields([]string{"name:string{nn}"})
	if err != nil {
		t.Fatal(err)
	}
	f := fields[0]
	if !f.NotNull {
		t.Error("expected NotNull=true from nn modifier")
	}
	if f.GoType != "string" {
		t.Errorf("expected GoType=string, got %s", f.GoType)
	}
	for _, m := range f.Modifiers {
		if m == "nn" {
			t.Error("nn should be removed from Modifiers")
		}
	}
}

func TestParseField_nn_combined(t *testing.T) {
	fields, err := ParseFields([]string{"email:string{nn,unique}"})
	if err != nil {
		t.Fatal(err)
	}
	f := fields[0]
	if !f.NotNull {
		t.Error("expected NotNull=true")
	}
	if len(f.Modifiers) != 1 || f.Modifiers[0] != "unique" {
		t.Errorf("expected Modifiers=[unique], got %v", f.Modifiers)
	}
}

func TestParseField_check(t *testing.T) {
	fields, err := ParseFields([]string{"age:int{check=age>0}!"})
	if err != nil {
		t.Fatal(err)
	}
	f := fields[0]
	if len(f.Modifiers) != 1 || f.Modifiers[0] != "check=age>0" {
		t.Errorf("expected check modifier preserved, got %v", f.Modifiers)
	}
}

func TestParseField_cascade(t *testing.T) {
	fields, err := ParseFields([]string{"user_id:string{fk=users,cascade}!"})
	if err != nil {
		t.Fatal(err)
	}
	f := fields[0]
	found := false
	for _, m := range f.Modifiers {
		if m == "cascade" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected cascade in Modifiers, got %v", f.Modifiers)
	}
}

func TestParseField_setnull(t *testing.T) {
	fields, err := ParseFields([]string{"org_id:string{fk=orgs,setnull}"})
	if err != nil {
		t.Fatal(err)
	}
	f := fields[0]
	found := false
	for _, m := range f.Modifiers {
		if m == "setnull" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected setnull in Modifiers, got %v", f.Modifiers)
	}
}

func TestParseField_cascade_without_fk_errors(t *testing.T) {
	_, err := ParseFields([]string{"name:string{cascade}"})
	if err == nil {
		t.Error("expected error: cascade without fk=")
	}
}

func TestParseField_setnull_without_fk_errors(t *testing.T) {
	_, err := ParseFields([]string{"name:string{setnull}"})
	if err == nil {
		t.Error("expected error: setnull without fk=")
	}
}

func TestParseField_cascade_and_setnull_errors(t *testing.T) {
	_, err := ParseFields([]string{"user_id:string{fk=users,cascade,setnull}"})
	if err == nil {
		t.Error("expected error: cascade and setnull are mutually exclusive")
	}
}

func TestParseField_invalid_field_name_errors(t *testing.T) {
	invalidNames := []string{"my-field:string", "1field:string", "field name:string"}
	for _, in := range invalidNames {
		_, err := ParseFields([]string{in})
		if err == nil {
			t.Errorf("expected error for invalid field name %q", in)
		}
	}
}

func TestParseField_unknown_modifier_errors(t *testing.T) {
	_, err := ParseFields([]string{"name:string{cascad}"})
	if err == nil {
		t.Error("expected error for unknown modifier 'cascad'")
	}
}

func TestParseField_array_string(t *testing.T) {
	fields, err := ParseFields([]string{"tags:[]string!"})
	if err != nil {
		t.Fatal(err)
	}
	f := fields[0]
	if f.GoType != "[]string" {
		t.Errorf("expected GoType=[]string, got %s", f.GoType)
	}
	if f.SQLType != "TEXT" {
		t.Errorf("expected SQLType=TEXT, got %s", f.SQLType)
	}
	if !f.NotNull {
		t.Error("expected NotNull=true")
	}
}

func TestParseField_array_nullable_is_not_pointer(t *testing.T) {
	// A slice is already nilable, so a nullable array field must not become **[]T.
	fields, err := ParseFields([]string{"scores:[]int"})
	if err != nil {
		t.Fatal(err)
	}
	if got := fields[0].GoType; got != "[]int" {
		t.Errorf("expected GoType=[]int (no pointer), got %s", got)
	}
}

func TestParseField_array_element_types(t *testing.T) {
	cases := map[string]string{
		"a:[]string":  "[]string",
		"a:[]text":    "[]string",
		"a:[]int":     "[]int",
		"a:[]int64":   "[]int64",
		"a:[]float":   "[]float64",
		"a:[]float64": "[]float64",
		"a:[]bool":    "[]bool",
	}
	for in, want := range cases {
		fields, err := ParseFields([]string{in})
		if err != nil {
			t.Errorf("%s: unexpected error %v", in, err)
			continue
		}
		if got := fields[0].GoType; got != want {
			t.Errorf("%s: expected GoType=%s, got %s", in, want, got)
		}
	}
}

func TestParseField_array_with_index_modifier(t *testing.T) {
	fields, err := ParseFields([]string{"tags:[]string{index}!"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fields[0].Modifiers) != 1 || fields[0].Modifiers[0] != "index" {
		t.Errorf("expected Modifiers=[index], got %v", fields[0].Modifiers)
	}
}

func TestParseField_array_rejects_time_and_json(t *testing.T) {
	for _, in := range []string{"a:[]time", "a:[]json", "a:[]datetime"} {
		if _, err := ParseFields([]string{in}); err == nil {
			t.Errorf("expected error for invalid array element type %q", in)
		}
	}
}

func TestParseField_array_rejects_size_modifier(t *testing.T) {
	if _, err := ParseFields([]string{"a:[]string{40}"}); err == nil {
		t.Error("expected error: size modifier invalid for array")
	}
}
