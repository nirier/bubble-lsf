package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

const hostInfoFixture = `HOST_NAME type model cpuf ncpus maxmem maxswp server RESOURCES
compute-01 X86_64 model 60.0 16 65536M 2G Yes (mg osrel=7.9 resource)
compute-02.example.test X86_64 model 60.0 32 128G 2G Yes (osrel_8.6)
`

const queueFixture = `QUEUE: normal
  -- default queue
PARAMETERS/STATISTICS
HOSTS: all ~compute-02.example.test
RES_REQ: select[type==any]

QUEUE: batch
HOSTS: workers/ compute-01+2
       compute-02.example.test+1
PRE_EXEC: /bin/true

QUEUE: disabled
HOSTS: none

QUEUE: preferred
HOSTS: compute-01+1 others+2
`

func TestParseHostInfo(t *testing.T) {
	info, err := parseHostInfo(hostInfoFixture)
	if err != nil || info["compute-01"].cpu != "16" || info["compute-01"].memory != "65536M" || info["compute-01"].osrel != "7.9" || info["compute-02.example.test"].osrel != "8.6" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	info, err = parseHostInfo("HOST_NAME ncpus maxmem OSREL RESOURCES\ncompute-01 8 32G 9.2 ()")
	if err != nil || info["compute-01"].osrel != "9.2" {
		t.Fatalf("OSREL column: %+v %v", info, err)
	}
	info, err = parseHostInfo("HOST_NAME ncpus maxmem RESOURCES\ncompute-01 - - (mg)")
	if err != nil || info["compute-01"].osrel != "" {
		t.Fatalf("missing resource must remain unknown: %+v %v", info, err)
	}
	for _, text := range []string{"", "permission denied", "HOST_NAME ncpus maxmem\ncompute-01 8", "HOST_NAME ncpus maxmem\n-bad 8 32G"} {
		if _, err := parseHostInfo(text); err == nil {
			t.Errorf("accepted malformed host info %q", text)
		}
	}
}

func TestQueueMembership(t *testing.T) {
	queues, err := parseQueueHosts(queueFixture)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := parseHostGroups("GROUP_NAME HOSTS\nworkers compute-02.example.test\n        compute-01\n")
	if err != nil {
		t.Fatal(err)
	}
	hosts, _ := parseHosts(hostFixture)
	if err := assignQueues(hosts, queues, groups); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hosts[0].queues, []string{"batch", "normal", "preferred"}) || !reflect.DeepEqual(hosts[1].queues, []string{"batch", "preferred"}) {
		t.Fatalf("incorrect memberships: %+v", hosts)
	}
	for _, text := range []string{"", "QUEUE: missing\n", "QUEUE: missing\nQUEUE: normal\nHOSTS: all"} {
		if _, err := parseQueueHosts(text); err == nil {
			t.Errorf("accepted incomplete queue config %q", text)
		}
	}
	if _, err := parseHostGroups("unexpected output"); err == nil {
		t.Fatal("accepted invalid host groups")
	}
}

func TestListHostsEnrichment(t *testing.T) {
	fakeCommands(t, map[string]string{
		"bhosts":  `case "$*" in '-w') printf '%s' "$HOSTS_FIXTURE";; '-l') printf '%s' "$BATCH_LONG";; *) exit 2;; esac`,
		"lshosts": `case "$*" in '-w') printf '%s' "$INFO_FIXTURE";; '-l') printf '%s' "$STATIC_LONG";; '-s') printf '%s' "$STATIC_SHARED";; *) exit 2;; esac`,
		"lsload":  `case "$*" in '-l') printf '%s' "$LOAD_FIXTURE";; '-s') printf '%s' "$DYNAMIC_SHARED";; *) exit 2;; esac`,
		"bqueues": `test "$*" = '-l' || exit 2
printf '%s' "$QUEUE_FIXTURE"`,
		"bmgroup": `test "$*" = '-w -r' || exit 2
printf 'GROUP_NAME HOSTS\nworkers compute-01 compute-02.example.test\n'`,
	})
	t.Setenv("BATCH_LONG", batchLongFixture)
	t.Setenv("STATIC_LONG", staticLongFixture)
	t.Setenv("STATIC_SHARED", "RESOURCE VALUE LOCATION\n")
	t.Setenv("LOAD_FIXTURE", loadFixture)
	t.Setenv("DYNAMIC_SHARED", dynamicSharedFixture)
	t.Setenv("HOSTS_FIXTURE", hostFixture)
	t.Setenv("INFO_FIXTURE", hostInfoFixture)
	t.Setenv("QUEUE_FIXTURE", queueFixture)
	hosts, err := listHosts(context.Background())
	if err != nil || len(hosts) != 2 || hosts[0].cpu != "16" || hosts[1].memory != "128G" || len(hosts[0].queues) != 3 {
		t.Fatalf("hosts=%+v err=%v", hosts, err)
	}
}

func TestListHostsKeepsPartialInformation(t *testing.T) {
	fakeCommands(t, map[string]string{
		"bhosts":  `printf '%s' "$HOSTS_FIXTURE"`,
		"lshosts": `printf '%s' "$INFO_FIXTURE"`,
		"bqueues": `printf '%s' "$QUEUE_FIXTURE"`,
		"bmgroup": `printf 'group permission denied' >&2; exit 1`,
	})
	t.Setenv("BATCH_LONG", batchLongFixture)
	t.Setenv("STATIC_LONG", staticLongFixture)
	t.Setenv("STATIC_SHARED", "RESOURCE VALUE LOCATION\n")
	t.Setenv("LOAD_FIXTURE", loadFixture)
	t.Setenv("DYNAMIC_SHARED", dynamicSharedFixture)
	t.Setenv("HOSTS_FIXTURE", hostFixture)
	t.Setenv("INFO_FIXTURE", hostInfoFixture)
	t.Setenv("QUEUE_FIXTURE", queueFixture)
	hosts, err := listHosts(context.Background())
	if len(hosts) != 2 || hosts[0].osrel != "7.9" || err == nil || !strings.Contains(err.Error(), "group permission denied") {
		t.Fatalf("partial information lost: %+v %v", hosts, err)
	}
	if !reflect.DeepEqual(hosts[0].queues, []string{"normal", "preferred"}) {
		t.Fatalf("unresolved group produced incorrect membership: %+v", hosts[0])
	}
}
