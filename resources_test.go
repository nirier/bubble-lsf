package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const resourceFixture = `RESOURCE_NAME: ncpus
DESCRIPTION: Number of CPUs
TYPE    ORDER INTERVAL BUILTIN DYNAMIC RELEASE CONSUMABLE
Numeric Dec   0        Yes     No      N/A     No

RESOURCE_NAME: osrel
DESCRIPTION: Operating system release
             configured by the site
TYPE    ORDER INTERVAL BUILTIN DYNAMIC RELEASE CONSUMABLE
String  N/A   0        No      No      N/A     No

RESOURCE_NAME: gpu_ready
DESCRIPTION: Host has a GPU
TYPE    ORDER INTERVAL BUILTIN DYNAMIC RELEASE CONSUMABLE
Boolean N/A   0        No      No      N/A     No

RESOURCE_NAME: license_app
DESCRIPTION: Floating application licenses
TYPE    ORDER INTERVAL BUILTIN DYNAMIC RELEASE CONSUMABLE
Numeric Dec   30       No      Yes     Yes     Yes
`

func TestResourceCatalog(t *testing.T) {
	resources, err := parseResourceCatalog(resourceFixture)
	if err != nil || len(resources) != 4 {
		t.Fatalf("resources=%+v err=%v", resources, err)
	}
	byName := make(map[string]resourceDescriptor)
	for _, resource := range resources {
		byName[resource.name] = resource
	}
	if byName["osrel"].kind != "String" || !strings.Contains(byName["osrel"].description, "configured by the site") || byName["license_app"].consumable != "Yes" || byName["license_app"].release != "Yes" {
		t.Fatalf("descriptor fields lost: %+v", byName)
	}
	resources, err = parseResourceCatalog("RESOURCE_NAME: custom\nDESCRIPTION: description\nTYPE: Boolean\nDYNAMIC: No\n")
	if err != nil || resources[0].kind != "Boolean" || resources[0].dynamic != "No" {
		t.Fatalf("key-value format: %+v %v", resources, err)
	}
	for _, text := range []string{"", "permission denied", "RESOURCE_NAME: bad\nTYPE: Unknown", "RESOURCE_NAME: missing\nDESCRIPTION: incomplete", "RESOURCE_NAME: -bad\nTYPE: Numeric", resourceFixture + resourceFixture} {
		if _, err := parseResourceCatalog(text); err == nil {
			t.Errorf("accepted malformed resource catalog %q", text)
		}
	}
	fakeCommands(t, map[string]string{"lsinfo": `test "$*" = '-r -l' || exit 2
printf '%s' "$RESOURCE_FIXTURE"`})
	t.Setenv("RESOURCE_FIXTURE", resourceFixture)
	if resources, err := listResourceCatalog(context.Background()); err != nil || len(resources) != 4 {
		t.Fatalf("catalog command: %+v %v", resources, err)
	}
}

func TestSelectionRequirement(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""},
		{"  ", ""},
		{"ncpus>=16 && (osrel=='7.9' || gpu_ready)", "select[ncpus>=16 && (osrel=='7.9' || gpu_ready)]"},
		{"select[ncpus>=16]", "select[ncpus>=16]"},
		{"select[osrel=='Ubuntu22.04']", "select[osrel=='Ubuntu22.04']"},
		{"select[custom=='contains]bracket']", "select[custom=='contains]bracket']"},
	} {
		got, err := selectionRequirement(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("%q => %q %v, want %q", tc.input, got, err, tc.want)
		}
	}
	for _, input := range []string{"select[]", "select[ncpus>8", "select[ncpus>8] rusage[mem=100]", "rusage[mem=100]", "select[osrel=='7]", "(ncpus>8", "ncpus>8)", "ncpus>8\n", strings.Repeat("n", 4097)} {
		if _, err := selectionRequirement(input); err == nil {
			t.Errorf("accepted invalid selection %q", input)
		}
	}
}

const resourceJobsFixture = `41|user|RUN|normal|compute-01|first
42|user|RUN|batch|4*compute-01:8*compute-02.example.test|parallel job
43|user|PEND|normal|-|pending
44[2]|user|DONE|batch|compute-02.example.test|array job
`

func TestJobsOnResourceHosts(t *testing.T) {
	jobs, _ := parseJobs(resourceJobsFixture)
	filtered := jobsOnHosts(jobs, []host{{name: "compute-02.example.test"}})
	if len(filtered) != 2 || filtered[0].id != "42" || filtered[1].id != "44[2]" {
		t.Fatalf("parallel host matching: %+v", filtered)
	}
	if got := executionHosts("4*compute-01:8*compute-02.example.test"); !reflect.DeepEqual(got, []string{"compute-01", "compute-02.example.test"}) {
		t.Fatalf("execution hosts=%v", got)
	}
	if len(executionHosts("-")) != 0 {
		t.Fatal("pending job acquired a synthetic execution host")
	}
	fakeCommands(t, map[string]string{
		"bjobs": `printf '%s' "$JOBS_FIXTURE"`,
		"bhosts": `test "$1" = '-w' && test "$2" = '-R' && test "$3" = "$REQUIREMENT" || exit 2
printf '%s' "$SELECTED_HOSTS"
printf '%s' "$SELECT_ERROR" >&2
exit "${SELECT_CODE:-0}"`,
	})
	t.Setenv("JOBS_FIXTURE", resourceJobsFixture)
	t.Setenv("REQUIREMENT", "select[ncpus>=32 && osrel=='Ubuntu22.04']")
	t.Setenv("SELECTED_HOSTS", "HOST_NAME STATUS JL/U MAX NJOBS RUN SSUSP USUSP RSV\ncompute-02.example.test ok - 32 2 2 0 0 0\n")
	filtered, err := listJobsWithRequirement(context.Background(), "select[ncpus>=32 && osrel=='Ubuntu22.04']")
	if err != nil || len(filtered) != 2 || filtered[0].id != "42" {
		t.Fatalf("query result=%+v err=%v", filtered, err)
	}
	t.Setenv("SELECTED_HOSTS", "")
	t.Setenv("SELECT_ERROR", "Unknown resource name\n")
	t.Setenv("SELECT_CODE", "1")
	if _, err := listJobsWithRequirement(context.Background(), "select[ncpus>=32 && osrel=='Ubuntu22.04']"); err == nil || !strings.Contains(err.Error(), "Unknown resource name") {
		t.Fatalf("resource query error hidden: %v", err)
	}
	t.Setenv("JOBS_FIXTURE", "")
	if _, err := listJobsWithRequirement(context.Background(), "select[ncpus>=32 && osrel=='Ubuntu22.04']"); err == nil {
		t.Fatal("empty jobs hid invalid resource query")
	}
	t.Setenv("SELECT_ERROR", "No matching host found")
	if hosts, err := selectHosts(context.Background(), "select[ncpus>=32 && osrel=='Ubuntu22.04']"); err != nil || len(hosts) != 0 {
		t.Fatalf("no matching hosts: %+v %v", hosts, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := selectHosts(ctx, "select[ncpus>=32 && osrel=='Ubuntu22.04']"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resource query: %v", err)
	}
}

func TestJobFilters(t *testing.T) {
	jobs, _ := parseJobs(resourceJobsFixture)
	for _, tc := range []struct {
		query string
		ids   []string
	}{
		{"status=RUN", []string{"41", "42"}},
		{"host=compute-02.example.test", []string{"42", "44[2]"}},
		{"queue=batch status!=DONE", []string{"42"}},
		{"id=44[2]", []string{"44[2]"}},
		{"name='parallel job'", []string{"42"}},
		{"parallel user", []string{"42"}},
	} {
		filters, err := compileJobFilter(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, j := range jobs {
			if matchesJob(j, filters) {
				ids = append(ids, j.id)
			}
		}
		if !reflect.DeepEqual(ids, tc.ids) {
			t.Errorf("query=%q got=%v want=%v", tc.query, ids, tc.ids)
		}
	}
	for _, query := range []string{"memory>=64G", "id>42", "status=", "name='unclosed"} {
		if _, err := compileJobFilter(query); err == nil {
			t.Errorf("accepted invalid Jobs query %q", query)
		}
	}
}
