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
