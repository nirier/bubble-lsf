package main

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
)

func TestResourceAndFieldCompletion(t *testing.T) {
	descriptors, _ := parseResourceCatalog(resourceFixture)
	values := map[string][]string{"osrel": {"Ubuntu22.04", "Rocky9"}, "queue": {"normal", "night"}, "name": {"parallel job"}, "id": {"44[2]"}, "maxmem": {"64G"}, "jl/u": {"8"}}
	fields := map[string]string{"cpu>=": "CPU count", "queue:": "Queue", "jl/u>=": "User job limit"}
	for _, tc := range []struct {
		input, choice, want string
		native              bool
		cursor              int
	}{
		{"select[ncp", "ncpus>=", "select[ncpus>=", true, -1},
		{"select[ncpus>=16 && gp", "gpu_ready", "select[ncpus>=16 && gpu_ready", true, -1},
		{"select[osrel==U", "'Ubuntu22.04'", "select[osrel=='Ubuntu22.04'", true, -1},
		{"select[osrel=='U']", "'Ubuntu22.04'", "select[osrel=='Ubuntu22.04']", true, 16},
		{"select[ncpus>=16]", "ncpus", "select[ncpus>=16]", true, 10},
		{"sel", "select[", "select[", true, -1},
		{"select[ncpus>=16]", "select", "select[ncpus>=16]", true, 3},
		{"cp", "cpu>=", "cpu>=", false, -1},
		{"jl/", "jl/u>=", "jl/u>=", false, -1},
		{"jl/u=", "8", "jl/u=8", false, -1},
		{"cpu>=16 queue:normal", "cpu", "cpu>=16 queue:normal", false, 2},
		{"queue:n", "normal", "queue:normal", false, -1},
		{"QUEUE:N", "normal", "QUEUE:normal", false, -1},
		{"name:p", "'parallel job'", "name:'parallel job'", false, -1},
		{"id=44[2]", "44[2]", "id=44[2]", false, 5},
	} {
		t.Run(tc.input+tc.choice, func(t *testing.T) {
			input := textinput.New()
			input.SetValue(tc.input)
			input.CursorEnd()
			if tc.cursor >= 0 {
				input.SetCursor(tc.cursor)
			}
			items := completeInput(input, tc.native, descriptors, values, fields)
			for _, item := range items {
				if item.value == tc.choice {
					applyCompletion(&input, item)
					if input.Value() != tc.want {
						t.Fatalf("got %q, want %q", input.Value(), tc.want)
					}
					return
				}
			}
			t.Fatalf("no %q in %+v", tc.choice, items)
		})
	}
	input := textinput.New()
	input.SetValue("maxmem>=")
	input.CursorEnd()
	for _, item := range completeInput(input, true, builtinResources(), values, nil) {
		if strings.Contains(item.value, "64G") {
			t.Fatal("display unit was inserted into native numeric resource expression")
		}
	}
}
