package main

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

const staticLongFixture = `HOST_NAME: compute-01
 type model cpuf ncpus maxmem maxswp maxtmp
 X86_64 HYGON 60.0 16 65536M 2G 100G

 nprocs ncores nthreads
 2 8 2

 ostype: Linux_6.1
 RESOURCES: (ssd ib rack_id='rack 01' switch_id=sw1)
            (room=SH)

HOST_NAME: compute-02.example.test
 type model cpuf ncpus maxmem maxswp maxtmp
 X86_64 Xeon 70.0 32 128G 4G 200G
 RESOURCES: (nvme rack_id=R2)
`
const batchLongFixture = `HOST  compute-01
 STATUS CPUF JL/U JL/P MAX NJOBS RUN SSUSP USUSP RSV
 ok 60.0 8 2 16 3 2 1 0 0

 CURRENT LOAD USED FOR SCHEDULING:
 r15s r1m r15m ut pg io ls it tmp swp mem
 Total 0.1 0.2 0.3 20% 0 1 0 10 80G 2G 32G
 Reserved 0 0 0 0% 0 0 0 0 0G 0G 1G

 LOAD THRESHOLDS USED FOR SCHEDULING:
 r15s r1m r15m ut pg io ls it tmp swp mem
 loadSched 4.0 4.0 4.0 90% - - - - 1G 1G 4G
 loadStop 8.0 8.0 8.0 99% - - - - 500M 500M 2G

 Migration threshold: 30 min
`
const loadFixture = `HOST_NAME status r15s r1m r15m ut pg io ls it tmp swp mem GPU_TEMP GPU_UTIL
compute-01 busy *0.1 0.2 0.3 20% 0 1 0 10 80G 2G 32G 55 12%
compute-02.example.test ok 0 0 0 0% 0 0 0 100 180G 4G 120G 30 0%
`
const staticSharedFixture = `RESOURCE VALUE LOCATION
OSREL KLV10 (compute-01)
      Ubuntu22 (compute-02.example.test)
rack_no R01 (compute-01)
`
const dynamicSharedFixture = `RESOURCE VALUE LOCATION
license_xxx 4 (compute-01)
            0 (compute-02.example.test)
GPU_FREE 16 (compute-01
             compute-02.example.test)
`

func TestAllHostAttributeSources(t *testing.T) {
	fakeCommands(t, map[string]string{
		"bhosts":  `case "$*" in '-w') printf '%s' "$HOSTS_FIXTURE";; '-l') printf '%s' "$BATCH_LONG";; *) exit 2;; esac`,
		"lshosts": `case "$*" in '-w') printf '%s' "$INFO_FIXTURE";; '-l') printf '%s' "$STATIC_LONG";; '-s') printf '%s' "$STATIC_SHARED";; *) exit 2;; esac`,
		"lsload":  `case "$*" in '-l') printf '%s' "$LOAD_FIXTURE";; '-s') printf '%s' "$DYNAMIC_SHARED";; *) exit 2;; esac`,
		"bqueues": `printf '%s' "$QUEUE_FIXTURE"`,
		"bmgroup": `printf 'GROUP_NAME HOSTS\nworkers compute-01 compute-02.example.test\n'`,
	})
	t.Setenv("HOSTS_FIXTURE", hostFixture)
	t.Setenv("INFO_FIXTURE", hostInfoFixture)
	t.Setenv("QUEUE_FIXTURE", queueFixture)
	t.Setenv("STATIC_LONG", staticLongFixture)
	t.Setenv("BATCH_LONG", batchLongFixture)
	t.Setenv("LOAD_FIXTURE", loadFixture)
	t.Setenv("STATIC_SHARED", staticSharedFixture)
	t.Setenv("DYNAMIC_SHARED", dynamicSharedFixture)
	hosts, err := listHosts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h := hosts[0]
	for field, want := range map[string]string{"cpu": "16", "memory": "65536M", "mem": "32G", "ostype": "Linux_6.1", "nprocs": "2", "ncores": "8", "nthreads": "2", "maxtmp": "100G", "ssd": "1", "rack_id": "rack 01", "room": "SH", "osrel": "KLV10", "gpu_temp": "55", "license_xxx": "4", "gpu_free": "16", "status": "ok", "load.status": "busy", "jl/u": "8", "jl/p": "2", "ssusp": "1", "ususp": "0", "loadsched.mem": "4G", "loadstop.mem": "2G", "reserved.mem": "1G", "groups": "workers"} {
		value, ok := hostField(h, field)
		if !ok || value != want {
			t.Errorf("%s=%q, want %q", field, value, want)
		}
	}
	if h.resources["GPU_TEMP"] != "55" || h.resources["OSREL"] != "KLV10" {
		t.Fatal("native resource case was lost")
	}
	for _, query := range []string{"ncores>=8 nthreads=2 cpuf>=60 maxswp=2G maxtmp>=100G", "ssd=1 rack_id='rack 01' room=SH", "GPU_TEMP<60 GPU_UTIL<20% license_xxx>0 OSREL=KLV10", "r15s<1 r1m<1 r15m<1 ut=20% mem>=32G swp=2G tmp>=80G pg=0 io=1 it=10", "jl/u=8 jl/p=2 ssusp=1 ususp=0 loadsched.mem=4G loadstop.mem=2G group=workers queue=normal", "load.status=busy batch.status=ok", "rack 01 KLV10"} {
		filters, err := compileHostFilter(query, hosts...)
		if err != nil || !matchesHost(h, filters) {
			t.Errorf("filter %q: %v", query, err)
		}
	}
	for _, query := range []string{"ssd=1", "license_xxx>0", "OSREL=KLV10", "mem<64G"} {
		filters, err := compileHostFilter(query, hosts...)
		if err != nil || matchesHost(hosts[1], filters) {
			t.Errorf("resource leaked to another host: %q, %v", query, err)
		}
	}
	m := loadedModel(t)
	m.Update(listMsg{pane: hostsPane, hosts: hosts})
	for _, key := range []string{"gpu_temp", "license_xxx", "ssd", "mem", "loadsched.mem", "groups"} {
		found := false
		for i, column := range m.table.Columns() {
			if column.Title == key {
				found = true
				value, _ := hostField(h, key)
				if m.table.Rows()[0][i] != value {
					t.Errorf("column %s has wrong value", key)
				}
			}
		}
		if !found {
			t.Errorf("missing column %s", key)
		}
	}
	press(m, '/')
	m.filterInput.SetValue("gpu_te")
	m.filterInput.CursorEnd()
	press(m, tea.KeyTab)
	if m.filterInput.Value() != "gpu_temp:" {
		t.Fatalf("custom completion=%q", m.filterInput.Value())
	}
	m.filterInput.SetValue("license_xxx>0 mem<64G")
	press(m, tea.KeyEnter)
	if len(m.table.Rows()) != 1 || m.selectedID() != "compute-01" {
		t.Fatal("TUI did not apply all-resource filter")
	}
	snapshot := hostSnapshot(h)
	if !strings.Contains(snapshot, "gpu_temp: 55") || !strings.Contains(snapshot, "rack_id: rack 01") {
		t.Fatal("full snapshot omitted custom fields")
	}
}

func TestHostAttributeParsingAndUnknownValues(t *testing.T) {
	for _, text := range []string{"permission denied", "HOST_NAME mem ut\ncompute-01 2G", "HOST_NAME mem\n-bad 2G"} {
		if _, err := parseHostAttributes(text, "load"); err == nil {
			t.Errorf("accepted malformed output %q", text)
		}
	}
	h := host{name: "compute-01"}
	h.setResource("external", "GPU_TEMP", "-")
	for _, query := range []string{"GPU_TEMP!=2", "GPU_TEMP>0", "mem!=32G"} {
		filters, err := compileHostFilter(query, h)
		if err != nil || matchesHost(h, filters) {
			t.Errorf("unknown field matched %s: %v", query, err)
		}
	}
	hosts := []host{{name: "compute-01"}, {name: "compute-02.example.test"}}
	if err := parseSharedResources("RESOURCE VALUE LOCATION\nOSREL 'Ubuntu 22' (compute-01)\n", hosts, "external"); err != nil {
		t.Fatal(err)
	}
	if value, _ := hostField(hosts[0], "osrel"); value != "Ubuntu 22" {
		t.Fatal("quoted shared resource was split")
	}
	if err := parseSharedResources("No static shared resources are defined.", hosts, "resource"); err != nil {
		t.Fatal(err)
	}
	if err := parseSharedResources("bad output", hosts, "resource"); err == nil {
		t.Fatal("invalid shared resource output was hidden")
	}
}

func TestAdditionalHostTablesAndSharedLocations(t *testing.T) {
	info, err := parseHostAttributes("HOST_NAME: compute-01\nncores\n8\n", "static")
	if err != nil || info["compute-01"].attributes["ncores"] != "8" {
		t.Fatalf("single-column static table: %+v, %v", info, err)
	}
	info, err = parseHostAttributes("HOST_NAME status mem\ncompute-01 ok 32G\n\nExternal / ELIM Resources\nHOST_NAME GPU_TEMP OSREL\ncompute-01 55 KLV10\n", "load")
	if err != nil || info["compute-01"].attributes["mem"] != "32G" || info["compute-01"].resources["GPU_TEMP"] != "55" {
		t.Fatalf("separate external table: %+v, %v", info, err)
	}
	hosts := []host{{name: "compute-01"}, {name: "compute-02.example.test"}, {name: "compute-03"}}
	text := "RESOURCE VALUE LOCATION\nlicense_x 5 compute-01\n                 compute-02.example.test compute-03\n"
	if err := parseSharedResources(text, hosts, "external"); err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if value, _ := hostField(h, "license_x"); value != "5" {
			t.Errorf("continued location %s missing resource", h.name)
		}
	}
	h := &hosts[0]
	h.setAttribute("load", "mem", "32G")
	h.setAttribute("batch", "loadsched.mem", "32G")
	h.setResource("external", "gpu.temp", "55")
	fields := strings.Join(hostTableFields(hosts), " ")
	if !strings.Contains(fields, "loadsched.mem") || !strings.Contains(fields, "gpu.temp") {
		t.Fatal("threshold or dotted custom name was hidden")
	}
	m := loadedModel(t)
	m.hosts = hosts
	m.resources = []resourceDescriptor{{name: "license_x", kind: "Numeric"}}
	filters, err := compileHostFilter("license_x=5.0", m.filterHosts()...)
	if err != nil || !matchesHost(hosts[0], filters) {
		t.Fatal("catalog numeric type was ignored for equality")
	}
}
