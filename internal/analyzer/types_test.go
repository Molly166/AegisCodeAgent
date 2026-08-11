package analyzer

import (
	"reflect"
	"testing"
)

func TestParseSelection(t *testing.T) {
	tests := []struct {
		value string
		want  []string
	}{
		{value: "default", want: []string{NameGoTest, NameGoVet}},
		{value: "all", want: []string{NameGoTest, NameGoVet, NameStaticcheck, NameGosec}},
		{value: "none", want: []string{}},
		{value: "gosec,go-vet,gosec", want: []string{NameGosec, NameGoVet}},
	}
	for _, test := range tests {
		got, err := ParseSelection(test.value)
		if err != nil {
			t.Errorf("ParseSelection(%q) error = %v", test.value, err)
			continue
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("ParseSelection(%q) = %v, want %v", test.value, got, test.want)
		}
	}
	if _, err := ParseSelection("unknown"); err == nil {
		t.Fatal("ParseSelection(unknown) error = nil")
	}
}
